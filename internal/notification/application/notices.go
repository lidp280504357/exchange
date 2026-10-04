package application

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/notification/domain"
	"github.com/skill/exchange/internal/notification/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/pii"
)

// Consumer names notification-service's inbox entries.
const Consumer = "notification-service"

// Notices turns account events into in-app notifications and security
// mails (requirements §5.3: 用户通知模板与站内信).
type Notices struct {
	Store      ports.NoticeStore
	Recipients ports.Recipients
	Dispatcher *Dispatcher
	Log        *slog.Logger
	Now        func() time.Time
}

// Event is an account event worth telling the user about.
type Event struct {
	ID     string
	UserID string
	Type   string
	At     time.Time
	Data   map[string]string
	// Mail also sends the notice to the user's mailbox (or phone).
	Mail bool
}

// Notify stores the notice once per event and, when asked, mails it. A
// failure before the notice is stored is returned so the event is
// retried; the mail is best effort, like every notice delivery.
func (n *Notices) Notify(ctx context.Context, e Event) error {
	r, err := n.Recipients.Recipient(ctx, e.UserID)
	if apperr.Is(err, apperr.CodeNotFound) {
		n.Log.WarnContext(ctx, "notice for an unknown user dropped", "user_id", e.UserID, "type", e.Type)
		return nil
	}
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(r.Timezone)
	if err != nil {
		loc = time.UTC
	}
	title, body := domain.RenderNotice(domain.NoticeInput{
		Type: e.Type, Language: r.Language, At: e.At, Location: loc, Data: e.Data, Brand: n.Dispatcher.brand(ctx),
	})
	notice := domain.Notice{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: e.UserID, Type: e.Type, Title: title, Body: body, Data: e.Data, CreatedAt: n.Now(),
	}
	created, err := n.Store.CreateNotice(ctx, Consumer, e.ID, notice, nil)
	if err != nil || !created || !e.Mail {
		return err
	}
	n.mail(ctx, notice, r)
	return nil
}

// contact is where a notice's mail goes: the user's email, or phone when
// there is none; nil for neither.
func contact(contacts []ports.Contact) *ports.Contact {
	var to *ports.Contact
	for i := range contacts {
		if to == nil || contacts[i].Channel == domain.ChannelEmail {
			to = &contacts[i]
		}
	}
	return to
}

// mailDelivery is the delivery of a notice's mail to to.
func mailDelivery(notice domain.Notice, to ports.Contact) domain.Delivery {
	return domain.Delivery{
		ID: notice.ID, Kind: domain.KindNotice, Channel: to.Channel, Template: "notice." + strings.ToLower(notice.Type),
		TargetMask: pii.MaskIdentifier(to.Value), UserID: notice.UserID,
	}
}

// mail sends the notice to the user's email, or phone when there is none.
func (n *Notices) mail(ctx context.Context, notice domain.Notice, r ports.Recipient) {
	contacts, err := n.Recipients.Contacts(ctx, notice.UserID)
	if err != nil {
		n.Log.WarnContext(ctx, "notice mail skipped: contacts unavailable", "notice_id", notice.ID, "error", err)
		return
	}
	to := contact(contacts)
	if to == nil {
		return
	}
	m := domain.NoticeMail(to.Channel, to.Value, notice.Title, notice.Body, r.AntiPhishingCode, r.Language, n.Dispatcher.brand(ctx))
	m.IdempotencyKey = notice.ID
	if _, err := n.Dispatcher.Deliver(ctx, mailDelivery(notice, *to), m); err != nil {
		n.Log.WarnContext(ctx, "notice mail failed", "notice_id", notice.ID, "error", err)
	}
}

// List returns a page of the user's notices, the cursor of the next page
// ("" at the end) and the unread count.
func (n *Notices) List(ctx context.Context, userID, cursor string, limit int) ([]domain.Notice, string, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			return nil, "", 0, apperr.Invalid("invalid cursor")
		}
	}
	items, err := n.Store.ListNotices(ctx, userID, cursor, limit+1)
	if err != nil {
		return nil, "", 0, err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = items[limit-1].ID
	}
	unread, err := n.Store.UnreadCount(ctx, userID)
	if err != nil {
		return nil, "", 0, err
	}
	return items, next, unread, nil
}

// MarkRead marks notices read; no IDs means all of them.
func (n *Notices) MarkRead(ctx context.Context, userID string, ids []string) (int64, error) {
	if len(ids) > 100 {
		return 0, apperr.Invalid("at most 100 ids at a time")
	}
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			return 0, apperr.Invalid("invalid notification id")
		}
	}
	return n.Store.MarkRead(ctx, userID, ids)
}
