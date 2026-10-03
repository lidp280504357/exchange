// Package postgres stores the simulated market's bots, settings and state
// in the marketsim schema.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/marketsim/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/outbox"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

// Store implements ports.Store; audit records go out through the outbox
// (audit.events) in the same transaction as what they record.
type Store struct {
	db     *pg.DB
	events *event.Factory
}

// NewStore returns a store on db; events makes the audit envelopes (nil:
// no audit records, for tests).
func NewStore(db *pg.DB, events *event.Factory) *Store { return &Store{db: db, events: events} }

// audit queues an operator's action on audit.events.
func (s *Store) audit(ctx context.Context, q pg.Querier, a *ports.Audit) error {
	if a == nil || s.events == nil {
		return nil
	}
	env, err := s.events.New(ctx, &auditv1.AdminActionPerformed{
		Target: a.Target, Action: a.Action, Actor: a.Actor, Reason: a.Reason, Details: a.Details,
	}, "actor", a.Actor)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, q, event.TopicAudit, env)
}

// Bots lists the bot accounts by label.
func (s *Store) Bots(ctx context.Context) ([]ports.Bot, error) {
	rows, err := s.db.Query(ctx, `SELECT user_id::text, role, label, enabled FROM bots ORDER BY label`)
	if err != nil {
		return nil, fmt.Errorf("list bots: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ports.Bot, error) {
		var b ports.Bot
		var role string
		err := row.Scan(&b.UserID, &role, &b.Label, &b.Enabled)
		b.Role = domain.Role(role)
		return b, err
	})
	if err != nil {
		return nil, fmt.Errorf("list bots: %w", err)
	}
	return out, nil
}

// AddBot registers an account as a bot; the same account again changes
// nothing, another account under a label in use is a conflict.
func (s *Store) AddBot(ctx context.Context, b ports.Bot) error {
	_, err := s.db.Exec(ctx, `INSERT INTO bots (user_id, role, label, enabled) VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO NOTHING`, b.UserID, string(b.Role), b.Label, b.Enabled)
	if _, ok := pg.UniqueViolation(err); ok {
		return apperr.New(apperr.KindConflict, apperr.CodeConflict, "the label "+b.Label+" is another bot's")
	}
	if err != nil {
		return fmt.Errorf("add bot: %w", err)
	}
	return nil
}

// Settings returns the settings and their version.
func (s *Store) Settings(ctx context.Context) (domain.Params, int64, bool, error) {
	var raw []byte
	var version int64
	err := s.db.QueryRow(ctx, `SELECT params, version FROM settings WHERE id = 1`).Scan(&raw, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Params{}, 0, false, nil
	}
	if err != nil {
		return domain.Params{}, 0, false, fmt.Errorf("read settings: %w", err)
	}
	p := domain.DefaultParams() // settings saved before a field existed keep its default
	if err := json.Unmarshal(raw, &p); err != nil {
		return domain.Params{}, 0, false, fmt.Errorf("read settings: %w", err)
	}
	return p, version, true, nil
}

// SaveSettings stores new settings and the change as the guards count it,
// and returns their version.
func (s *Store) SaveSettings(ctx context.Context, p domain.Params, change ports.ParamChange, audit *ports.Audit) (int64, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	if change.At.IsZero() {
		change.At = time.Now()
	}
	var version int64
	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO settings (id, params, version, updated_by, updated_at) VALUES (1, $1, 1, $2, $3)
			ON CONFLICT (id) DO UPDATE SET params = EXCLUDED.params, version = settings.version + 1,
				updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at
			RETURNING version`, raw, change.Actor, change.At).Scan(&version)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO param_changes (version, at, actor, approved_by, move, volume) VALUES ($1, $2, $3, $4, $5, $6)`,
			version, change.At, change.Actor, change.ApprovedBy, change.Move, change.Volume); err != nil {
			return err
		}
		if change.State != nil {
			st, err := json.Marshal(change.State)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO state (id, state, saved_at) VALUES (1, $1, $2)
				ON CONFLICT (id) DO UPDATE SET state = EXCLUDED.state, saved_at = EXCLUDED.saved_at`, st, change.At); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, audit)
	})
	if err != nil {
		return 0, fmt.Errorf("save settings: %w", err)
	}
	return version, nil
}

// ParamChanges returns the changes of the settings made from from to to.
func (s *Store) ParamChanges(ctx context.Context, from, to time.Time) ([]ports.ParamChange, error) {
	rows, err := s.db.Query(ctx, `SELECT at, actor, approved_by, move, volume FROM param_changes WHERE at BETWEEN $1 AND $2 ORDER BY at`, from, to)
	if err != nil {
		return nil, fmt.Errorf("param changes: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ports.ParamChange, error) {
		var c ports.ParamChange
		err := row.Scan(&c.At, &c.Actor, &c.ApprovedBy, &c.Move, &c.Volume)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("param changes: %w", err)
	}
	return out, nil
}

const eventColumns = `id::text, type, size, price, mu, factor, duration_s, hold_s, starts_at, status, created_by, approved_by, reason,
	created_at, started_at, ended_at, from_log_e, from_p, ended_by, direction, then_mode, width_s, parent_id::text, crossed_at, result`

func scanEvent(row pgx.Row) (domain.Event, error) {
	var e domain.Event
	var typ string
	var duration, hold, width int
	var started, ended, crossed *time.Time
	var parent *string
	err := row.Scan(&e.ID, &typ, &e.Size, &e.Price, &e.Mu, &e.Factor, &duration, &hold, &e.StartsAt, &e.Status, &e.CreatedBy,
		&e.ApprovedBy, &e.Reason, &e.CreatedAt, &started, &ended, &e.FromLogE, &e.FromP, &e.EndedBy, &e.Direction, &e.Then, &width,
		&parent, &crossed, &e.Result)
	e.Type = domain.EventType(typ)
	e.Duration, e.Hold, e.Width = time.Duration(duration)*time.Second, time.Duration(hold)*time.Second, time.Duration(width)*time.Second
	if started != nil {
		e.StartedAt = *started
	}
	if ended != nil {
		e.EndedAt = *ended
	}
	if crossed != nil {
		e.CrossedAt = *crossed
	}
	if parent != nil {
		e.ParentID = *parent
	}
	return e, err
}

// Events returns the open events, oldest first, or the latest limit.
func (s *Store) Events(ctx context.Context, open bool, limit int) ([]domain.Event, error) {
	q := `SELECT ` + eventColumns + ` FROM events WHERE status IN ('SCHEDULED', 'RUNNING') ORDER BY starts_at, created_at`
	args := []any{}
	if !open {
		q, args = `SELECT `+eventColumns+` FROM events ORDER BY created_at DESC LIMIT $1`, []any{limit}
	}
	return s.list(ctx, q, args...)
}

// EventsStarting returns the events not canceled that start from from to
// to.
func (s *Store) EventsStarting(ctx context.Context, from, to time.Time) ([]domain.Event, error) {
	return s.list(ctx, `SELECT `+eventColumns+` FROM events WHERE starts_at BETWEEN $1 AND $2 AND status <> 'CANCELED' ORDER BY starts_at`,
		from, to)
}

func (s *Store) list(ctx context.Context, q string, args ...any) ([]domain.Event, error) {
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Event, error) { return scanEvent(row) })
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return out, nil
}

// SaveEvent stores a new event or its new course.
func (s *Store) SaveEvent(ctx context.Context, e domain.Event, audit *ports.Audit) error {
	return s.SaveEvents(ctx, []domain.Event{e}, audit)
}

// SaveEvents stores new events or their new courses in one transaction
// (a target and its spikes), with one audit record.
func (s *Store) SaveEvents(ctx context.Context, es []domain.Event, audit *ports.Audit) error {
	stamp := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		return &t
	}
	text := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		for _, e := range es {
			_, err := tx.Exec(ctx, `INSERT INTO events (`+strings.NewReplacer("id::text", "id", "parent_id::text", "parent_id").Replace(eventColumns)+`)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25)
				ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, started_at = EXCLUDED.started_at, ended_at = EXCLUDED.ended_at,
					from_log_e = EXCLUDED.from_log_e, from_p = EXCLUDED.from_p, ended_by = EXCLUDED.ended_by,
					direction = EXCLUDED.direction, then_mode = EXCLUDED.then_mode, crossed_at = EXCLUDED.crossed_at, result = EXCLUDED.result`,
				e.ID, string(e.Type), e.Size, e.Price, e.Mu, e.Factor, int(e.Duration.Seconds()), int(e.Hold.Seconds()), e.StartsAt, e.Status,
				e.CreatedBy, e.ApprovedBy, e.Reason, e.CreatedAt, stamp(e.StartedAt), stamp(e.EndedAt), e.FromLogE, e.FromP, e.EndedBy,
				e.Direction, e.Then, int(e.Width.Seconds()), text(e.ParentID), stamp(e.CrossedAt), e.Result)
			if err != nil {
				return fmt.Errorf("event %s: %w", e.ID, err)
			}
		}
		return s.audit(ctx, tx, audit)
	})
	if err != nil {
		return fmt.Errorf("save events: %w", err)
	}
	return nil
}

// SaveSample keeps a sample of the target and the last price.
func (s *Store) SaveSample(ctx context.Context, x ports.Sample) error {
	if _, err := s.db.Exec(ctx, `INSERT INTO samples (at, target, last) VALUES ($1, $2, $3) ON CONFLICT (at) DO NOTHING`,
		x.At, x.Target, x.Last); err != nil {
		return fmt.Errorf("save sample: %w", err)
	}
	return nil
}

// Samples returns the samples since t, oldest first.
func (s *Store) Samples(ctx context.Context, t time.Time) ([]ports.Sample, error) {
	rows, err := s.db.Query(ctx, `SELECT at, target, last FROM samples WHERE at >= $1 ORDER BY at`, t)
	if err != nil {
		return nil, fmt.Errorf("samples: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ports.Sample, error) {
		var x ports.Sample
		err := row.Scan(&x.At, &x.Target, &x.Last)
		return x, err
	})
	if err != nil {
		return nil, fmt.Errorf("samples: %w", err)
	}
	return out, nil
}

// PruneSamples drops the samples before t.
func (s *Store) PruneSamples(ctx context.Context, t time.Time) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM samples WHERE at < $1`, t); err != nil {
		return fmt.Errorf("prune samples: %w", err)
	}
	return nil
}

// State returns the model's saved state.
func (s *Store) State(ctx context.Context) (domain.State, bool, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT state FROM state WHERE id = 1`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.State{}, false, nil
	}
	if err != nil {
		return domain.State{}, false, fmt.Errorf("read state: %w", err)
	}
	var st domain.State
	if err := json.Unmarshal(raw, &st); err != nil {
		return domain.State{}, false, fmt.Errorf("read state: %w", err)
	}
	return st, true, nil
}

// SaveState stores the model's state.
func (s *Store) SaveState(ctx context.Context, st domain.State) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO state (id, state, saved_at) VALUES (1, $1, $2)
		ON CONFLICT (id) DO UPDATE SET state = EXCLUDED.state, saved_at = EXCLUDED.saved_at`, raw, time.Now()); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}
