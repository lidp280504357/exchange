package application

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lidp280504357/exchange/internal/notification/domain"
	"github.com/lidp280504357/exchange/internal/notification/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

type memNotices struct {
	mu      sync.Mutex
	handled map[string]bool
	notices []domain.Notice
}

func (s *memNotices) CreateNotice(_ context.Context, consumer, eventID string, n domain.Notice) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handled[consumer+eventID] {
		return false, nil
	}
	s.handled[consumer+eventID] = true
	s.notices = append(s.notices, n)
	return true, nil
}

func (s *memNotices) ListNotices(_ context.Context, userID, beforeID string, limit int) ([]domain.Notice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Notice
	for i := len(s.notices) - 1; i >= 0 && len(out) < limit; i-- {
		n := s.notices[i]
		if n.UserID == userID && (beforeID == "" || n.ID < beforeID) {
			out = append(out, n)
		}
	}
	return out, nil
}

func (s *memNotices) UnreadCount(_ context.Context, userID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, x := range s.notices {
		if x.UserID == userID && x.ReadAt.IsZero() {
			n++
		}
	}
	return n, nil
}

func (s *memNotices) MarkRead(_ context.Context, userID string, ids []string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for i, x := range s.notices {
		if x.UserID != userID || !x.ReadAt.IsZero() {
			continue
		}
		if len(ids) == 0 || slices.Contains(ids, x.ID) {
			s.notices[i].ReadAt = time.Now()
			n++
		}
	}
	return n, nil
}

type fakeRecipients struct {
	recipients map[string]ports.Recipient
	contacts   map[string][]ports.Contact
}

func (f fakeRecipients) Recipient(_ context.Context, userID string) (ports.Recipient, error) {
	r, ok := f.recipients[userID]
	if !ok {
		return ports.Recipient{}, apperr.NotFound("no such user")
	}
	return r, nil
}

func (f fakeRecipients) Contacts(_ context.Context, userID string) ([]ports.Contact, error) {
	return f.contacts[userID], nil
}

func TestNotify(t *testing.T) {
	mail := &scriptedProvider{name: "mail"}
	sms := &scriptedProvider{name: "sms"}
	d := NewDispatcher(Routes{Email: []ports.Provider{mail}, SMS: []ports.Provider{sms}}, newMemDeliveries(),
		slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	store := &memNotices{handled: map[string]bool{}}
	n := &Notices{
		Store: store, Dispatcher: d, Log: slog.New(slog.DiscardHandler), Now: time.Now,
		Recipients: fakeRecipients{
			recipients: map[string]ports.Recipient{
				"u-en": {Language: "en", Timezone: "Asia/Singapore", AntiPhishingCode: "Blue42"},
				"u-zh": {Language: "zh-CN", Timezone: "Bad/Zone"},
			},
			contacts: map[string][]ports.Contact{
				"u-en": {{Channel: domain.ChannelSMS, Value: "+6591234567"}, {Channel: domain.ChannelEmail, Value: "en@example.com"}},
				"u-zh": {{Channel: domain.ChannelSMS, Value: "+8613800138000"}},
			},
		},
	}
	ctx := context.Background()
	at := time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)

	login := Event{
		ID: "ev-1", UserID: "u-en", Type: domain.NoticeNewDeviceLogin, At: at, Mail: true,
		Data: map[string]string{"ip": "203.0.113.*", "user_agent": "Firefox"},
	}
	if err := n.Notify(ctx, login); err != nil {
		t.Fatal(err)
	}
	if err := n.Notify(ctx, login); err != nil { // redelivered
		t.Fatal(err)
	}
	if len(store.notices) != 1 || mail.count() != 1 || sms.count() != 0 {
		t.Fatalf("once per event, by email when there is one: %d notices, %d mails, %d sms", len(store.notices), mail.count(), sms.count())
	}
	got := mail.sent[0]
	if got.To != "en@example.com" || !strings.Contains(got.Text, "Anti-phishing code: Blue42") || !strings.Contains(got.Text, "12:00:00 +08") {
		t.Fatalf("mail: %+v", got)
	}

	// No email: the phone gets a short SMS; a bad time zone falls back to UTC.
	if err := n.Notify(ctx, Event{
		ID: "ev-2", UserID: "u-zh", Type: domain.NoticePasswordChanged, At: at, Mail: true,
		Data: map[string]string{"via_reset": "true"},
	}); err != nil {
		t.Fatal(err)
	}
	if sms.count() != 1 || sms.sent[0].Text != "【Astras】密码已重置" || !strings.Contains(store.notices[1].Body, "04:00:00 UTC") {
		t.Fatalf("sms: %+v, body %q", sms.sent, store.notices[1].Body)
	}

	// In-app only, and unknown users are dropped instead of retried.
	if err := n.Notify(ctx, Event{ID: "ev-3", UserID: "u-en", Type: domain.NoticeWelcome, At: at}); err != nil {
		t.Fatal(err)
	}
	if err := n.Notify(ctx, Event{ID: "ev-4", UserID: "ghost", Type: domain.NoticeWelcome, At: at}); err != nil {
		t.Fatal(err)
	}
	if len(store.notices) != 3 || mail.count() != 1 {
		t.Fatalf("welcome is in-app only: %d notices, %d mails", len(store.notices), mail.count())
	}

	items, next, unread, err := n.List(ctx, "u-en", "", 1)
	if err != nil || len(items) != 1 || items[0].Type != domain.NoticeWelcome || next == "" || unread != 2 {
		t.Fatalf("list: %+v %q %d %v", items, next, unread, err)
	}
	if _, _, _, err := n.List(ctx, "u-en", "not-a-uuid", 1); err == nil {
		t.Fatal("bad cursor accepted")
	}
	if updated, err := n.MarkRead(ctx, "u-en", nil); err != nil || updated != 2 {
		t.Fatalf("mark all read: %d %v", updated, err)
	}
	if _, err := n.MarkRead(ctx, "u-en", []string{"nope"}); err == nil {
		t.Fatal("bad id accepted")
	}
}
