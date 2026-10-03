package application

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/lidp280504357/exchange/internal/notification/domain"
	"github.com/lidp280504357/exchange/internal/notification/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// The mails sent later and the retention of old rows (the C4b review,
// C5.5 ⑫).

// Queued mails are tried again after these waits; after the last one a
// mail is FAILED (its DeliveryFailed event is the dead letter).
var queueRetries = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

// queueLease holds a taken mail from another round while it is sent;
// queueGiveUp is how long a mail is tried at most.
const (
	queueLease  = 5 * time.Minute
	queueGiveUp = 24 * time.Hour
)

// MailQueue sends the mails a broadcast queued with its notices, a few at
// a time (PerRound, 2 when zero, a round each second) so that a message to
// everyone keeps to the providers' pace, and tries one that failed again
// later (queueRetries). The address is read when the mail is sent: it is
// never stored in the clear.
type MailQueue struct {
	Queue      ports.MailQueue
	Notices    ports.NoticeStore
	Recipients ports.Recipients
	Dispatcher *Dispatcher
	Log        *slog.Logger
	Now        func() time.Time
	PerRound   int
}

// Round sends the mails due; it returns how many were sent.
func (q *MailQueue) Round(ctx context.Context) (int, error) {
	per := q.PerRound
	if per <= 0 {
		per = 2
	}
	due, err := q.Queue.TakeDue(ctx, q.Now(), queueLease, per)
	if err != nil {
		return 0, err
	}
	sent := 0
	var errs []error
	for _, d := range due {
		ok, err := q.send(ctx, d)
		if ok {
			sent++
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return sent, errors.Join(errs...)
}

// send tries d once on its channel's providers. A failure that may pass
// waits for the next retry; one that will not (no address any more, a
// refused one), the last retry and a day's tries fail d for good.
func (q *MailQueue) send(ctx context.Context, d domain.Delivery) (bool, error) {
	m, err := q.message(ctx, d)
	if err == nil {
		if err = q.Dispatcher.SendOnce(ctx, d, m); err == nil {
			return true, q.Queue.Settle(ctx, d.ID)
		}
	}
	// Each round makes at least one attempt: its count bounds the rounds.
	round := min(d.Attempts, len(queueRetries))
	if !retryable(err) || round >= len(queueRetries) || q.Now().Sub(d.CreatedAt) > queueGiveUp {
		q.Dispatcher.fail(d, domain.ClassOf(err))
		return false, q.Queue.Settle(ctx, d.ID)
	}
	q.Log.WarnContext(ctx, "queued mail failed; tried again later", "delivery_id", d.ID, "class", string(domain.ClassOf(err)), "error", err)
	return false, q.Queue.Retry(ctx, d.ID, q.Now().Add(queueRetries[round]))
}

// gone is a queued mail that has nowhere to go any more.
func gone(why string) error {
	return &domain.SendError{Class: domain.FailureInvalidTarget, Err: errors.New(why)}
}

// message builds d's mail from its notice and the user's address on d's
// channel now.
func (q *MailQueue) message(ctx context.Context, d domain.Delivery) (domain.Message, error) {
	notice, err := q.Notices.Notice(ctx, d.ID)
	if err != nil {
		return domain.Message{}, err
	}
	if notice == nil {
		return domain.Message{}, gone("the notice is gone")
	}
	r, err := q.Recipients.Recipient(ctx, d.UserID)
	if apperr.Is(err, apperr.CodeNotFound) {
		return domain.Message{}, gone("the user is gone")
	}
	if err != nil {
		return domain.Message{}, err
	}
	contacts, err := q.Recipients.Contacts(ctx, d.UserID)
	if err != nil {
		return domain.Message{}, err
	}
	for _, c := range contacts {
		if c.Channel == d.Channel {
			m := domain.NoticeMail(c.Channel, c.Value, notice.Title, notice.Body, r.AntiPhishingCode, r.Language)
			m.IdempotencyKey = notice.ID
			return m, nil
		}
	}
	return domain.Message{}, gone("the user has no address on the channel any more")
}

// Retention deletes what is past its keep, a batch at a time: in-app
// notices and the broadcasts that made them after NoticeKeep (180 days
// when zero), delivery records after DeliveryKeep (90 days when zero).
// The audit trail keeps who sent what.
type Retention struct {
	Store        ports.RetentionStore
	NoticeKeep   time.Duration
	DeliveryKeep time.Duration
	Now          func() time.Time
}

// retentionBatch is how many rows one statement deletes.
const retentionBatch = 5000

// Purge deletes the rows past their keep; it returns how many of each.
func (r *Retention) Purge(ctx context.Context) (notices, broadcasts, deliveries int64, err error) {
	notice, delivery := r.NoticeKeep, r.DeliveryKeep
	if notice <= 0 {
		notice = 180 * 24 * time.Hour
	}
	if delivery <= 0 {
		delivery = 90 * 24 * time.Hour
	}
	now := r.Now()
	for {
		n, err := r.Store.PurgeNotices(ctx, now.Add(-notice), retentionBatch)
		notices += n
		if err != nil || n < retentionBatch {
			if err != nil {
				return notices, 0, 0, err
			}
			break
		}
	}
	if broadcasts, err = r.Store.PurgeBroadcasts(ctx, now.Add(-notice)); err != nil {
		return notices, broadcasts, 0, err
	}
	for {
		n, err := r.Store.PurgeDeliveries(ctx, now.Add(-delivery), retentionBatch)
		deliveries += n
		if err != nil || n < retentionBatch {
			return notices, broadcasts, deliveries, err
		}
	}
}
