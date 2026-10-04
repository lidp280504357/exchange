// Package mock is the provider for channels without a contracted vendor
// (SMS in development, decision #8) and for test mail domains. It stores
// messages in mock_messages, readable through the dev inbox outside
// production.
package mock

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/skill/exchange/internal/notification/domain"
	"github.com/skill/exchange/internal/platform/pg"
)

// Provider records messages instead of sending them.
type Provider struct {
	name string
	db   pg.Querier
}

// New returns a provider named name.
func New(name string, db pg.Querier) *Provider { return &Provider{name: name, db: db} }

// Name identifies the provider.
func (p *Provider) Name() string { return p.name }

// Send stores m.
func (p *Provider) Send(ctx context.Context, m domain.Message) (string, error) {
	var id int64
	err := p.db.QueryRow(ctx, `INSERT INTO mock_messages (channel, target, subject, body) VALUES ($1, $2, $3, $4) RETURNING id`,
		string(m.Channel), m.To, m.Subject, m.Text).Scan(&id)
	if err != nil {
		return "", &domain.SendError{Class: domain.FailureUnknown, Retryable: true, Err: err}
	}
	return "mock-" + strconv.FormatInt(id, 10), nil
}

// Message is one stored message.
type Message struct {
	Channel   string    `json:"channel"`
	Target    string    `json:"target"`
	Subject   string    `json:"subject,omitempty"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// Messages returns the latest messages to target, newest first.
func (p *Provider) Messages(ctx context.Context, target string, limit int) ([]Message, error) {
	rows, err := p.db.Query(ctx, `SELECT channel, target, subject, body, created_at FROM mock_messages
		WHERE target = $1 ORDER BY id DESC LIMIT $2`, target, limit)
	if err != nil {
		return nil, fmt.Errorf("mock inbox: %w", err)
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.Channel, &m.Target, &m.Subject, &m.Body, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("mock inbox: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Purge deletes messages older than cutoff.
func (p *Provider) Purge(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := p.db.Exec(ctx, `DELETE FROM mock_messages WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("mock inbox: purge: %w", err)
	}
	return tag.RowsAffected(), nil
}
