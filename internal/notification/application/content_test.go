package application

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/skill/exchange/internal/notification/domain"
	"github.com/skill/exchange/internal/notification/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

type memContent struct {
	mu       sync.Mutex
	articles []domain.Article
}

func (s *memContent) Articles(_ context.Context, section string, visibleAt time.Time) ([]domain.Article, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Article
	for _, a := range s.articles {
		if a.Section == section && (visibleAt.IsZero() || a.Visible(visibleAt)) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *memContent) PublishedPage(ctx context.Context, section string, at time.Time, offset, limit int) ([]domain.Article, error) {
	list, _ := s.Articles(ctx, section, at)
	slices.SortStableFunc(list, func(a, b domain.Article) int { return b.PublishAt.Compare(a.PublishAt) })
	list = list[min(offset, len(list)):]
	return list[:min(limit, len(list))], nil
}

func (s *memContent) Withdrawn(_ context.Context, section string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for _, a := range s.articles {
		if a.Section == section && a.Status == domain.ArticleArchived {
			out = append(out, a.Slug)
		}
	}
	return out, nil
}

func (s *memContent) find(match func(domain.Article) bool) (*domain.Article, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.articles {
		if match(a) {
			return &a, nil
		}
	}
	return nil, nil
}

func (s *memContent) Article(_ context.Context, section, slug string) (*domain.Article, error) {
	return s.find(func(a domain.Article) bool { return a.Section == section && a.Slug == slug })
}

func (s *memContent) ArticleByID(_ context.Context, id string) (*domain.Article, error) {
	return s.find(func(a domain.Article) bool { return a.ID == id })
}

func (s *memContent) CreateArticle(_ context.Context, a domain.Article) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.articles {
		if x.Section == a.Section && x.Slug == a.Slug {
			return domain.ErrArticleExists
		}
	}
	s.articles = append(s.articles, a)
	return nil
}

func (s *memContent) UpdateArticle(_ context.Context, a domain.Article, version int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.articles {
		if x.ID == a.ID {
			if x.Version != version {
				return apperr.New(apperr.KindConflict, apperr.CodeConflict, "changed")
			}
			s.articles[i] = a
			return nil
		}
	}
	return apperr.NotFound("no such article")
}

func TestArticlesArePublishedForTheSites(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	c := &Content{Store: &memContent{}, Now: func() time.Time { return now }}
	ctx := context.Background()
	zh := domain.ArticleText{Locale: domain.LocaleZH, Title: "新币上线", Body: "# LINK\n\n今天上线。"}
	in := ArticleInput{Section: "announcement", Slug: "New-Listing", Category: "product", Texts: []domain.ArticleText{zh}}
	a, err := c.Create(ctx, in, "ops@example.com")
	if err != nil || a.Status != domain.ArticleDraft || a.Slug != "new-listing" || a.Section != domain.SectionAnnouncement || a.Version != 1 {
		t.Fatalf("draft %+v %v", a, err)
	}
	if _, err := c.Create(ctx, in, "ops@example.com"); !apperr.Is(err, "NOTIFY_ARTICLE_EXISTS") {
		t.Fatalf("the same slug twice: %v", err)
	}
	for _, bad := range []ArticleInput{
		{Section: "BLOG", Slug: "x", Texts: []domain.ArticleText{zh}},
		{Section: "HELP", Slug: "Not A Slug", Texts: []domain.ArticleText{zh}},
		{Section: "HELP", Slug: "en-only", Texts: []domain.ArticleText{{Locale: domain.LocaleEN, Title: "x", Body: "y"}}},
		{Section: "HELP", Slug: "no-body", Texts: []domain.ArticleText{{Locale: domain.LocaleZH, Title: "x"}}},
	} {
		if _, err := c.Create(ctx, bad, "ops@example.com"); !apperr.Is(err, apperr.CodeInvalidArgument) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	if list, _, err := c.Published(ctx, domain.SectionAnnouncement, "", 0); err != nil || len(list) != 0 {
		t.Fatalf("a draft is not shown: %+v %v", list, err)
	}
	// Scheduled an hour ahead: shown from then on.
	a, err = c.Publish(ctx, a.ID, 1, now.Add(time.Hour), "ops@example.com")
	if err != nil || a.Status != domain.ArticlePublished || a.Version != 2 || !a.PublishAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("scheduled %+v %v", a, err)
	}
	if _, err := c.PublishedArticle(ctx, domain.SectionAnnouncement, "new-listing"); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("not yet: %v", err)
	}
	now = now.Add(time.Hour)
	got, err := c.PublishedArticle(ctx, domain.SectionAnnouncement, "NEW-LISTING")
	if err != nil || got.Slug != "new-listing" {
		t.Fatalf("published %+v %v", got, err)
	}
	if text, ok := got.Text(domain.LocaleEN); ok || text.Title != "新币上线" {
		t.Fatalf("English falls back to Chinese: %+v %v", text, ok)
	}
	// Someone else saved it meanwhile: the stale version is refused.
	in.Texts = append(in.Texts, domain.ArticleText{Locale: domain.LocaleEN, Title: "New listing", Body: "LINK today."})
	in.Slug = "new-listing"
	if _, err := c.Update(ctx, a.ID, 1, in, "ops@example.com"); !apperr.Is(err, apperr.CodeConflict) {
		t.Fatalf("a stale version: %v", err)
	}
	a, err = c.Update(ctx, a.ID, 2, in, "ops@example.com")
	if err != nil || a.Status != domain.ArticlePublished || len(a.Texts) != 2 {
		t.Fatalf("edited, still published %+v %v", a, err)
	}
	if text, ok := a.Text(domain.LocaleEN); !ok || text.Title != "New listing" {
		t.Fatalf("English %+v", text)
	}
	if a, err = c.Archive(ctx, a.ID, a.Version, "ops@example.com"); err != nil || a.Status != domain.ArticleArchived {
		t.Fatalf("archived %+v %v", a, err)
	}
	if list, _, _ := c.Published(ctx, domain.SectionAnnouncement, "", 0); len(list) != 0 {
		t.Fatal("an archived article is shown")
	}
	// Taken off: the sites hide their own file of the slug too.
	if slugs, err := c.Withdrawn(ctx, domain.SectionAnnouncement); err != nil || len(slugs) != 1 || slugs[0] != "new-listing" {
		t.Fatalf("withdrawn %v %v", slugs, err)
	}
	if _, err := c.PublishedArticle(ctx, domain.SectionAnnouncement, "new-listing"); !apperr.Is(err, "NOTIFY_ARTICLE_WITHDRAWN") {
		t.Fatalf("an archived article: %v", err)
	}
	if all, err := c.All(ctx, domain.SectionAnnouncement); err != nil || len(all) != 1 {
		t.Fatalf("the console sees every status: %+v %v", all, err)
	}
}

type memBroadcasts struct {
	mu   sync.Mutex
	list []domain.Broadcast
}

func (s *memBroadcasts) CreateBroadcast(_ context.Context, b domain.Broadcast) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, b)
	return nil
}

func (s *memBroadcasts) Broadcast(_ context.Context, id string) (*domain.Broadcast, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.list {
		if b.ID == id {
			return &b, nil
		}
	}
	return nil, nil
}

func (s *memBroadcasts) Broadcasts(_ context.Context, beforeID string, limit int) ([]domain.Broadcast, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Broadcast
	for i := len(s.list) - 1; i >= 0 && len(out) < limit; i-- {
		if beforeID == "" || s.list[i].ID < beforeID {
			out = append(out, s.list[i])
		}
	}
	return out, nil
}

func (s *memBroadcasts) Sending(_ context.Context, now time.Time) ([]domain.Broadcast, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Broadcast
	for _, b := range s.list {
		if b.Status == domain.BroadcastSending && !b.RetryAt.After(now) {
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *memBroadcasts) Advance(_ context.Context, id, cursor string, recipients int, done bool, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID == id {
			s.list[i].Cursor, s.list[i].Recipients, s.list[i].Status = cursor, recipients, domain.BroadcastSending
			s.list[i].Failures, s.list[i].LastError, s.list[i].RetryAt = 0, "", time.Time{}
			if done {
				s.list[i].Status, s.list[i].FinishedAt = domain.BroadcastSent, at
			}
		}
	}
	return nil
}

func (s *memBroadcasts) FailRound(_ context.Context, id string, failures int, lastError string, retryAt time.Time, failed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID == id && s.list[i].Status == domain.BroadcastSending {
			s.list[i].Failures, s.list[i].LastError, s.list[i].RetryAt = failures, lastError, retryAt
			if failed {
				s.list[i].Status = domain.BroadcastFailed
			}
		}
	}
	return nil
}

func (s *memBroadcasts) Resume(_ context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID == id && s.list[i].Status == domain.BroadcastFailed {
			s.list[i].Status, s.list[i].Failures, s.list[i].LastError, s.list[i].RetryAt = domain.BroadcastSending, 0, "", time.Time{}
			return true, nil
		}
	}
	return false, nil
}

// directory pages through users two at a time.
type directory struct{ users []string }

func (d directory) UserIDs(_ context.Context, cursor string, limit int) ([]string, string, error) {
	from := slices.Index(d.users, cursor) + 1
	if cursor == "" {
		from = 0
	}
	to := min(from+min(limit, 2), len(d.users))
	next := ""
	if to < len(d.users) {
		next = d.users[to-1]
	}
	return d.users[from:to], next, nil
}

func TestBroadcastsReachEveryoneOnceInTheirLanguage(t *testing.T) {
	mail := &scriptedProvider{name: "mail"}
	deliveries := newMemDeliveries()
	d := NewDispatcher(Routes{Email: []ports.Provider{mail}}, deliveries, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	queue := newMemQueue(deliveries)
	notices := &memNotices{handled: map[string]bool{}, queue: queue}
	people := map[string]ports.Recipient{}
	contacts := map[string][]ports.Contact{}
	var users []string
	for i := range 5 {
		id := uuid.Must(uuid.NewV7()).String()
		users = append(users, id)
		people[id] = ports.Recipient{Language: map[bool]string{true: "en", false: "zh-CN"}[i%2 == 0]}
		contacts[id] = []ports.Contact{{Channel: domain.ChannelEmail, Value: "u" + id[:8] + "@example.com"}}
	}
	n := &Notices{
		Store: notices, Dispatcher: d, Log: slog.New(slog.DiscardHandler), Now: time.Now,
		Recipients: fakeRecipients{recipients: people, contacts: contacts},
	}
	b := &Broadcasts{
		Store: &memBroadcasts{}, Notices: n, Directory: directory{users: users}, Log: slog.New(slog.DiscardHandler),
		Now: time.Now, Batch: 2,
	}
	ctx := context.Background()
	msg := BroadcastInput{
		Audience: "all", Title: map[string]string{"zh-CN": "维护通知", "en": "Maintenance"},
		Body: map[string]string{"zh-CN": "今晚维护一小时。", "en": "One hour tonight."}, Link: "/announcements/maintenance",
	}
	for _, bad := range []BroadcastInput{
		{Audience: "SOME", Title: msg.Title, Body: msg.Body},
		{Audience: "USERS", Title: msg.Title, Body: msg.Body},
		{Audience: "ALL", Title: map[string]string{"en": "x"}, Body: map[string]string{"en": "y"}},
		{Audience: "ALL", Title: msg.Title, Body: msg.Body, Link: "https://evil.example.com"},
		{Audience: "ALL", Title: msg.Title, Body: msg.Body, Link: "//evil.example.com"},
		{Audience: "USERS", UserIDs: []string{"bob"}, Title: msg.Title, Body: msg.Body},
		{Audience: "ALL", Title: msg.Title, Body: map[string]string{"zh-CN": "x", "fr": "y"}},
	} {
		if _, err := b.Send(ctx, bad, "ops@example.com"); !apperr.Is(err, apperr.CodeInvalidArgument) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	br, err := b.Send(ctx, msg, "ops@example.com")
	if err != nil || br.Status != domain.BroadcastSending || br.Audience != domain.AudienceAll {
		t.Fatalf("sent %+v %v", br, err)
	}
	for range 4 { // two a round, the last round finds nobody left
		if _, err := b.Round(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got, err := b.Get(ctx, br.ID)
	if err != nil || got.Status != domain.BroadcastSent || got.Recipients != 5 || len(notices.notices) != 5 || mail.count() != 0 {
		t.Fatalf("everyone once %+v %v (%d notices, %d mails)", got, err, len(notices.notices), mail.count())
	}
	for _, x := range notices.notices {
		want := "维护通知"
		if people[x.UserID].Language == "en" {
			want = "Maintenance"
		}
		if x.Title != want || x.Type != domain.NoticeBroadcast || x.Data["broadcast_id"] != br.ID || x.Data["link"] != "/announcements/maintenance" {
			t.Fatalf("notice %+v", x)
		}
	}
	// To two named users, mailed too; a repeated round gives nobody a second one.
	to := BroadcastInput{Audience: "USERS", UserIDs: []string{users[1], users[3], users[1]}, Title: msg.Title, Body: msg.Body, Email: true}
	br, err = b.Send(ctx, to, "ops@example.com")
	if err != nil || len(br.UserIDs) != 2 {
		t.Fatalf("named %+v %v", br, err)
	}
	if _, err := b.Round(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Store.Advance(ctx, br.ID, "", 0, false, time.Now()); err != nil { // as if the round had not been recorded
		t.Fatal(err)
	}
	if _, err := b.Round(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = b.Get(ctx, br.ID)
	if got.Status != domain.BroadcastSent || got.Recipients != 2 || len(notices.notices) != 7 || mail.count() != 0 || queue.waiting() != 2 {
		t.Fatalf("named once each, their mails queued %+v (%d notices, %d mails, %d queued)", got, len(notices.notices), mail.count(), queue.waiting())
	}
	// The queue sends them (C5.5 ⑫).
	q := &MailQueue{Queue: queue, Notices: notices, Recipients: n.Recipients, Dispatcher: d, Log: slog.New(slog.DiscardHandler), Now: time.Now}
	if sent, err := q.Round(ctx); err != nil || sent != 2 || mail.count() != 2 || !strings.Contains(mail.sent[0].Text, "今晚维护一小时。") || queue.waiting() != 0 {
		t.Fatalf("mailed %d %v (%d mails, %d queued)", sent, err, mail.count(), queue.waiting())
	}
	list, next, err := b.List(ctx, "", 1)
	if err != nil || len(list) != 1 || list[0].ID != br.ID || next == "" {
		t.Fatalf("list %+v %q %v", list, next, err)
	}

	// Under the console's ID: sent again it is the same message; another message under it is refused.
	keyed := msg
	keyed.ID = uuid.Must(uuid.NewV7()).String()
	first, err := b.Send(ctx, keyed, "ops@example.com")
	if err != nil || first.ID != keyed.ID {
		t.Fatalf("under its ID %+v %v", first, err)
	}
	if again, err := b.Send(ctx, keyed, "ops@example.com"); err != nil || again.ID != keyed.ID || len(b.Store.(*memBroadcasts).list) != 3 {
		t.Fatalf("sent again %+v %v", again, err)
	}
	other := keyed
	other.Link = "/markets"
	if _, err := b.Send(ctx, other, "ops@example.com"); !apperr.Is(err, apperr.CodeIdempotencyConflict) {
		t.Fatalf("another message under the ID: %v", err)
	}
	if _, err := b.Send(ctx, BroadcastInput{ID: "nope", Audience: "ALL", Title: msg.Title, Body: msg.Body}, "ops@example.com"); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("a bad ID: %v", err)
	}
}
