// Package ports declares what notification-service's application layer
// needs from the outside world.
package ports

import (
	"context"
	"time"

	"github.com/lidp280504357/exchange/internal/notification/domain"
)

// Provider sends messages through one vendor on one channel. Business code
// never sees vendor SDKs (§5.3).
type Provider interface {
	Name() string
	// Send returns the vendor's message ID. Failures are *domain.SendError.
	Send(ctx context.Context, m domain.Message) (string, error)
}

// DeliveryStore persists deliveries and their outcomes.
type DeliveryStore interface {
	CreateDelivery(ctx context.Context, d domain.Delivery) error
	// RecordAttempt stores the outcome of one provider attempt.
	RecordAttempt(ctx context.Context, id string, status domain.Status, provider string, class domain.FailureClass, providerMessageID string) error
	// FailDelivery marks d FAILED and queues its DeliveryFailed event in the
	// same transaction.
	FailDelivery(ctx context.Context, d domain.Delivery, class domain.FailureClass) error
}

// NoticeStore keeps the in-app inbox.
type NoticeStore interface {
	// CreateNotice stores n and queues NotificationCreated in one
	// transaction, unless consumer already handled eventID; it reports
	// whether it stored n. A mail, when given, is queued in the same
	// transaction, due at n's creation (MailQueue, C5.5 ⑫).
	CreateNotice(ctx context.Context, consumer, eventID string, n domain.Notice, mail *domain.Delivery) (bool, error)
	// ListNotices returns up to limit notices older than beforeID (""
	// for the newest), newest first.
	ListNotices(ctx context.Context, userID, beforeID string, limit int) ([]domain.Notice, error)
	UnreadCount(ctx context.Context, userID string) (int, error)
	// MarkRead marks the given notices, or all when ids is empty, and
	// returns how many changed.
	MarkRead(ctx context.Context, userID string, ids []string) (int64, error)
	// Notice reads one by its ID; nil when unknown (or deleted).
	Notice(ctx context.Context, id string) (*domain.Notice, error)
}

// MailQueue keeps the mails sent later (a broadcast's, C5.5 ⑫) in the
// deliveries table; NoticeStore.CreateNotice queues them.
type MailQueue interface {
	// TakeDue returns up to limit queued deliveries due by now, oldest
	// first, each held from the other takers until lease is over.
	TakeDue(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]domain.Delivery, error)
	// Retry makes a queued delivery due again at at, counting a failed
	// round (Delivery.Rounds).
	Retry(ctx context.Context, id string, at time.Time) error
	// Settle ends a queued delivery's wait (sent, or failed for good).
	Settle(ctx context.Context, id string) error
}

// RetentionStore deletes what is past its keep (C5.5 ⑫).
type RetentionStore interface {
	// PurgeNotices deletes up to limit notices created before before.
	PurgeNotices(ctx context.Context, before time.Time, limit int) (int64, error)
	// PurgeBroadcasts deletes up to limit broadcasts created before before
	// that are not sending.
	PurgeBroadcasts(ctx context.Context, before time.Time, limit int) (int64, error)
	// PurgeDeliveries deletes up to limit delivery records created before
	// before that wait for nothing.
	PurgeDeliveries(ctx context.Context, before time.Time, limit int) (int64, error)
}

// Recipient is what messages to a user need to know.
type Recipient struct {
	Language         string
	Timezone         string
	AntiPhishingCode string
}

// Contact is a verified address of a user.
type Contact struct {
	Channel domain.Channel
	Value   string
}

// Recipients looks users up in user-service and auth-service.
type Recipients interface {
	// Recipient returns the user's preferences; unknown users fail with
	// COMMON_NOT_FOUND.
	Recipient(ctx context.Context, userID string) (Recipient, error)
	Contacts(ctx context.Context, userID string) ([]Contact, error)
}

// Directory pages through the accounts (user-service), for a message to
// everyone.
type Directory interface {
	// UserIDs returns a page of the open accounts' IDs (closed ones left
	// out) and the next page's cursor ("" after the last).
	UserIDs(ctx context.Context, cursor string, limit int) ([]string, string, error)
}

// ContentStore keeps the announcements and help articles (design
// 2026-10-02 §4.5).
type ContentStore interface {
	// Articles returns a section's articles with their texts; with
	// visibleAt set only those published by then.
	Articles(ctx context.Context, section string, visibleAt time.Time) ([]domain.Article, error)
	// PublishedPage returns limit of a section's articles published by at,
	// from offset in the sites' order; their texts carry the body's first
	// HeadLength characters only, enough for a summary (C5.5 ⑫).
	PublishedPage(ctx context.Context, section string, at time.Time, offset, limit int) ([]domain.Article, error)
	// Withdrawn returns the slugs of a section's archived articles.
	Withdrawn(ctx context.Context, section string) ([]string, error)
	// Article and ArticleByID read one with its texts; nil when unknown.
	Article(ctx context.Context, section, slug string) (*domain.Article, error)
	ArticleByID(ctx context.Context, id string) (*domain.Article, error)
	// CreateArticle fails with domain.ErrArticleExists for a section's
	// slug taken.
	CreateArticle(ctx context.Context, a domain.Article) error
	// UpdateArticle writes a over the article at version, its texts
	// replaced; COMMON_CONFLICT when someone saved it meanwhile.
	UpdateArticle(ctx context.Context, a domain.Article, version int) error
}

// BroadcastStore keeps the operators' in-app messages.
type BroadcastStore interface {
	CreateBroadcast(ctx context.Context, b domain.Broadcast) error
	// Broadcast reads one with how many recipients read it; nil when
	// unknown.
	Broadcast(ctx context.Context, id string) (*domain.Broadcast, error)
	// Broadcasts pages through them, newest first, before beforeID ("" for
	// the newest), with their read counts.
	Broadcasts(ctx context.Context, beforeID string, limit int) ([]domain.Broadcast, error)
	// Sending returns up to ten broadcasts still sending whose next round
	// is due at now, oldest first.
	Sending(ctx context.Context, now time.Time) ([]domain.Broadcast, error)
	// Advance records a round: the cursor, the recipients reached, and
	// whether it is done; the failures in a row start over.
	Advance(ctx context.Context, id, cursor string, recipients int, done bool, at time.Time) error
	// FailRound records a failed round: the failures in a row, the error,
	// when the next may run, and FAILED when failed.
	FailRound(ctx context.Context, id string, failures int, lastError string, retryAt time.Time, failed bool) error
	// Resume sends a FAILED broadcast again from where it stopped; false
	// when it is not FAILED (or unknown).
	Resume(ctx context.Context, id string) (bool, error)
}

// HeadLength is how much of an article's body a list reads, for its
// summary (domain.Excerpt).
const HeadLength = 4000
