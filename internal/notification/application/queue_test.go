package application

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lidp280504357/exchange/internal/notification/domain"
	"github.com/lidp280504357/exchange/internal/notification/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// memQueue is the deliveries table's queue; the attempts come from the
// dispatcher's records.
type memQueue struct {
	mu     sync.Mutex
	del    *memDeliveries
	rows   map[string]domain.Delivery
	due    map[string]time.Time
	rounds map[string]int
}

func newMemQueue(del *memDeliveries) *memQueue {
	return &memQueue{del: del, rows: map[string]domain.Delivery{}, due: map[string]time.Time{}, rounds: map[string]int{}}
}

func (q *memQueue) add(d domain.Delivery, at time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.rows[d.ID]; ok {
		return
	}
	d.CreatedAt = at
	q.rows[d.ID], q.due[d.ID] = d, at
}

func (q *memQueue) waiting() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.due)
}

func (q *memQueue) TakeDue(_ context.Context, now time.Time, lease time.Duration, limit int) ([]domain.Delivery, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []domain.Delivery
	for id, at := range q.due {
		if len(out) < limit && !at.After(now) {
			d := q.rows[id]
			_, _, d.Attempts, _ = q.del.get(id)
			d.Rounds = q.rounds[id]
			out = append(out, d)
			q.due[id] = now.Add(lease)
		}
	}
	return out, nil
}

func (q *memQueue) Retry(_ context.Context, id string, at time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.due[id] = at
	q.rounds[id]++
	return nil
}

func (q *memQueue) Settle(_ context.Context, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.due, id)
	return nil
}

// flakyRecipients fails for the users down.
type flakyRecipients struct {
	fakeRecipients
	mu   sync.Mutex
	down map[string]bool
}

func (f *flakyRecipients) set(user string, down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down[user] = down
}

func (f *flakyRecipients) Recipient(ctx context.Context, userID string) (ports.Recipient, error) {
	f.mu.Lock()
	down := f.down[userID]
	f.mu.Unlock()
	if down {
		return ports.Recipient{}, errors.New("user-service unavailable")
	}
	return f.fakeRecipients.Recipient(ctx, userID)
}

// TestAFailingBroadcastHoldsNoOtherUp: a broadcast whose rounds fail waits
// longer each time while the others go on, is FAILED after ten in a row
// and goes on from where it stopped once resumed (C5.5 ⑫).
func TestAFailingBroadcastHoldsNoOtherUp(t *testing.T) {
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	ok, bad := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	people := &flakyRecipients{
		fakeRecipients: fakeRecipients{recipients: map[string]ports.Recipient{ok: {Language: "zh-CN"}, bad: {Language: "zh-CN"}}},
		down:           map[string]bool{bad: true},
	}
	notices := &memNotices{handled: map[string]bool{}}
	n := &Notices{Store: notices, Recipients: people, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }}
	store := &memBroadcasts{}
	b := &Broadcasts{Store: store, Notices: n, Directory: directory{}, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }}
	ctx := context.Background()
	msg := func(user string) BroadcastInput {
		return BroadcastInput{Audience: "USERS", UserIDs: []string{user}, Title: map[string]string{"zh-CN": "通知"}, Body: map[string]string{"zh-CN": "正文"}}
	}
	stuck, err := b.Send(ctx, msg(bad), "ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	fine, err := b.Send(ctx, msg(ok), "ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	created, err := b.Round(ctx)
	if created != 1 || err == nil {
		t.Fatalf("the other one delivered, the failure returned: %d %v", created, err)
	}
	if got, _ := b.Get(ctx, fine.ID); got.Status != domain.BroadcastSent {
		t.Fatalf("not held up %+v", got)
	}
	got, _ := b.Get(ctx, stuck.ID)
	if got.Status != domain.BroadcastSending || got.Failures != 1 || got.LastError == "" || !got.RetryAt.Equal(now.Add(3*time.Second)) {
		t.Fatalf("the first failure %+v", got)
	}
	if _, err := b.Round(ctx); err != nil {
		t.Fatalf("not due yet: %v", err)
	}
	for i := 2; i <= domain.MaxRoundFailures; i++ {
		now = now.Add(domain.RoundBackoff(i - 1))
		if _, err := b.Round(ctx); err == nil {
			t.Fatalf("round %d did not fail", i)
		}
	}
	got, _ = b.Get(ctx, stuck.ID)
	if got.Status != domain.BroadcastFailed || got.Failures != domain.MaxRoundFailures {
		t.Fatalf("failed after ten %+v", got)
	}
	now = now.Add(time.Hour)
	if _, err := b.Round(ctx); err != nil {
		t.Fatalf("a FAILED one waits: %v", err)
	}
	if _, err := b.Resume(ctx, fine.ID); !apperr.Is(err, apperr.CodeConflict) {
		t.Fatalf("a sent one is not resumed: %v", err)
	}
	if _, err := b.Resume(ctx, uuid.NewString()); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("an unknown one: %v", err)
	}
	people.set(bad, false)
	if got, err := b.Resume(ctx, stuck.ID); err != nil || got.Status != domain.BroadcastSending || got.Failures != 0 {
		t.Fatalf("resumed %+v %v", got, err)
	}
	if created, err := b.Round(ctx); err != nil || created != 1 {
		t.Fatalf("delivered once resumed: %d %v", created, err)
	}
	if got, _ := b.Get(ctx, stuck.ID); got.Status != domain.BroadcastSent {
		t.Fatalf("sent %+v", got)
	}
	if d := domain.RoundBackoff(30); d != 10*time.Minute {
		t.Fatalf("the longest wait %v", d)
	}
}

// TestQueuedMailsAreTriedAgainLater: a queued mail that fails waits 1, 5,
// 15 and 60 minutes and then fails for good; one that cannot go (no
// address any more) fails at once (C5.5 ⑫).
func TestQueuedMailsAreTriedAgainLater(t *testing.T) {
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	down := errors.New("down")
	mail := &scriptedProvider{name: "mail", errs: []error{timeout, down, timeout, down, timeout}}
	deliveries := newMemDeliveries()
	d := NewDispatcher(Routes{Email: []ports.Provider{mail}}, deliveries, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	queue := newMemQueue(deliveries)
	notices := &memNotices{handled: map[string]bool{}, queue: queue}
	user, other := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	people := fakeRecipients{
		recipients: map[string]ports.Recipient{user: {Language: "en"}, other: {Language: "en"}},
		contacts:   map[string][]ports.Contact{user: {{Channel: domain.ChannelEmail, Value: "user@example.com"}}},
	}
	q := &MailQueue{Queue: queue, Notices: notices, Recipients: people, Dispatcher: d, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }}
	ctx := context.Background()
	queued := func(userID string) string {
		notice := domain.Notice{ID: uuid.Must(uuid.NewV7()).String(), UserID: userID, Type: domain.NoticeBroadcast, Title: "Hi", Body: "There", CreatedAt: now}
		del := mailDelivery(notice, ports.Contact{Channel: domain.ChannelEmail, Value: "user@example.com"})
		if _, err := notices.CreateNotice(ctx, BroadcastConsumer, notice.ID, notice, &del); err != nil {
			t.Fatal(err)
		}
		return notice.ID
	}
	id := queued(user)
	for i, wait := range queueRetries {
		if sent, err := q.Round(ctx); err != nil || sent != 0 {
			t.Fatalf("attempt %d: %d %v", i+1, sent, err)
		}
		if status, _, attempts, _ := deliveries.get(id); status != domain.StatusFailedRetrying || attempts != i+1 {
			t.Fatalf("attempt %d recorded as %s, %d attempts", i+1, status, attempts)
		}
		if sent, _ := q.Round(ctx); sent != 0 || mail.count() != 0 {
			t.Fatalf("waits %v after attempt %d", wait, i+1)
		}
		now = now.Add(wait)
	}
	if _, err := q.Round(ctx); err != nil {
		t.Fatal(err)
	}
	if status, _, attempts, class := deliveries.get(id); status != domain.StatusFailed || attempts != len(queueRetries)+1 || class != domain.FailureTimeout {
		t.Fatalf("failed for good: %s %d %s", status, attempts, class)
	}
	if queue.waiting() != 0 {
		t.Fatal("still queued")
	}
	// Sent now (by a provider whose circuit those failures did not open); a
	// user without the address any more fails at once.
	mail = &scriptedProvider{name: "mail"}
	q.Dispatcher = NewDispatcher(Routes{Email: []ports.Provider{mail}}, deliveries, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	id = queued(user)
	gone := queued(other)
	if sent, err := q.Round(ctx); err != nil || sent != 1 || mail.count() != 1 || mail.sent[0].IdempotencyKey != id {
		t.Fatalf("sent %d %v", sent, err)
	}
	if status, _, _, class := deliveries.get(gone); status != domain.StatusFailed || class != domain.FailureInvalidTarget || queue.waiting() != 0 {
		t.Fatalf("nowhere to go: %s %s", status, class)
	}
}

func TestThePublishedListPages(t *testing.T) {
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	store := &memContent{}
	for i := range 5 {
		store.articles = append(store.articles, domain.Article{
			ID: uuid.Must(uuid.NewV7()).String(), Section: domain.SectionHelp, Slug: string(rune('a' + i)), Status: domain.ArticlePublished,
			PublishAt: now.Add(-time.Duration(i) * time.Hour),
		})
	}
	c := &Content{Store: store, Now: func() time.Time { return now }}
	ctx := context.Background()
	var slugs []string
	cursor := ""
	for range 3 {
		page, next, err := c.Published(ctx, domain.SectionHelp, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range page {
			slugs = append(slugs, a.Slug)
		}
		if cursor = next; cursor == "" {
			break
		}
	}
	if !slices.Equal(slugs, []string{"a", "b", "c", "d", "e"}) || cursor != "" {
		t.Fatalf("pages %v %q", slugs, cursor)
	}
	for _, bad := range []string{"x", "-1", "10001"} {
		if _, _, err := c.Published(ctx, domain.SectionHelp, bad, 2); !apperr.Is(err, apperr.CodeInvalidArgument) {
			t.Fatalf("cursor %q: %v", bad, err)
		}
	}
	if page, _, err := c.Published(ctx, domain.SectionHelp, "", 1000); err != nil || len(page) != 5 {
		t.Fatalf("at most 100 a page %d %v", len(page), err)
	}
}

// memRetention records what it is asked to delete.
type memRetention struct {
	notices    int
	cutoffs    []time.Time
	broadcasts int64
}

func (m *memRetention) PurgeNotices(_ context.Context, before time.Time, limit int) (int64, error) {
	m.cutoffs = append(m.cutoffs, before)
	n := min(m.notices, limit)
	m.notices -= n
	return int64(n), nil
}

func (m *memRetention) PurgeBroadcasts(_ context.Context, before time.Time, limit int) (int64, error) {
	m.cutoffs = append(m.cutoffs, before)
	n := min(m.broadcasts, int64(limit))
	m.broadcasts -= n
	return n, nil
}

func (m *memRetention) PurgeDeliveries(_ context.Context, before time.Time, _ int) (int64, error) {
	m.cutoffs = append(m.cutoffs, before)
	return 3, nil
}

func TestRetentionDeletesInBatches(t *testing.T) {
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	store := &memRetention{notices: 2*retentionBatch + 7, broadcasts: retentionBatch + 2}
	r := &Retention{Store: store, Now: func() time.Time { return now }}
	notices, broadcasts, deliveries, err := r.Purge(context.Background())
	if err != nil || notices != 2*retentionBatch+7 || broadcasts != retentionBatch+2 || deliveries != 3 {
		t.Fatalf("deleted %d %d %d %v", notices, broadcasts, deliveries, err)
	}
	// Three batches of notices, two of broadcasts (C5.5 ㉓), one of deliveries.
	keep, records := now.Add(-180*24*time.Hour), now.Add(-90*24*time.Hour)
	if len(store.cutoffs) != 6 || !store.cutoffs[0].Equal(keep) || !store.cutoffs[4].Equal(keep) || !store.cutoffs[5].Equal(records) {
		t.Fatalf("the cutoffs %v", store.cutoffs)
	}
}

// TestQueuedMailRoundsNotAttempts: with two providers each round makes two
// attempts, and the mail still waits 1, 5, 15 and 60 minutes before it
// fails; a mail that waited in a backed-up queue for hours gets its rounds
// too (C5.5 ㉓).
func TestQueuedMailRoundsNotAttempts(t *testing.T) {
	queuedAt := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	now := queuedAt.Add(3 * time.Hour) // the queue was backed up
	errs := func() []error {
		var out []error
		for range 2 * (len(queueRetries) + 1) {
			out = append(out, timeout)
		}
		return out
	}
	first, second := &scriptedProvider{name: "first", errs: errs()}, &scriptedProvider{name: "second", errs: errs()}
	deliveries := newMemDeliveries()
	d := NewDispatcher(Routes{Email: []ports.Provider{first, second}}, deliveries, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	queue := newMemQueue(deliveries)
	notices := &memNotices{handled: map[string]bool{}, queue: queue}
	user := uuid.Must(uuid.NewV7()).String()
	people := fakeRecipients{
		recipients: map[string]ports.Recipient{user: {Language: "en"}},
		contacts:   map[string][]ports.Contact{user: {{Channel: domain.ChannelEmail, Value: "user@example.com"}}},
	}
	q := &MailQueue{Queue: queue, Notices: notices, Recipients: people, Dispatcher: d, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }}
	ctx := context.Background()
	notice := domain.Notice{ID: uuid.Must(uuid.NewV7()).String(), UserID: user, Type: domain.NoticeBroadcast, Title: "Hi", Body: "There", CreatedAt: queuedAt}
	del := mailDelivery(notice, ports.Contact{Channel: domain.ChannelEmail, Value: "user@example.com"})
	if _, err := notices.CreateNotice(ctx, BroadcastConsumer, notice.ID, notice, &del); err != nil {
		t.Fatal(err)
	}
	for i, wait := range queueRetries {
		if _, err := q.Round(ctx); err != nil {
			t.Fatal(err)
		}
		if status, _, attempts, _ := deliveries.get(notice.ID); status != domain.StatusFailedRetrying || attempts != 2*(i+1) || queue.waiting() != 1 {
			t.Fatalf("round %d: %s after %d attempts, %d queued", i+1, status, attempts, queue.waiting())
		}
		now = now.Add(wait)
	}
	if _, err := q.Round(ctx); err != nil {
		t.Fatal(err)
	}
	if status, _, attempts, _ := deliveries.get(notice.ID); status != domain.StatusFailed || attempts != 2*(len(queueRetries)+1) || queue.waiting() != 0 {
		t.Fatalf("failed after the last round: %s %d", status, attempts)
	}
}
