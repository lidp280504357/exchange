// Package pushes publishes margin accounts as they stand on
// margin.accounts, for the private channel margin's ACCOUNT pushes.
package pushes

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/kafka"
)

// Publisher implements ports.Pushes over a direct producer: the topic
// keeps derived state an hour and needs no outbox (a lost one is
// replaced by the next).
type Publisher struct {
	Pub    kafka.Publisher
	Events *event.Factory
}

// Push publishes the accounts, keyed by user.
func (p *Publisher) Push(ctx context.Context, accounts []*marginv1.MarginAccountUpdated) error {
	recs := make([]kafka.Record, 0, len(accounts))
	for _, a := range accounts {
		env, err := p.Events.New(ctx, a, "user", a.GetUserId())
		if err != nil {
			return err
		}
		raw, err := proto.Marshal(env)
		if err != nil {
			return fmt.Errorf("margin account of %s: %w", a.GetUserId(), err)
		}
		recs = append(recs, kafka.Record{Topic: event.TopicMarginAccounts, Key: a.GetUserId(), EventType: env.GetEventType(), Envelope: raw})
	}
	return p.Pub.Publish(ctx, recs...)
}
