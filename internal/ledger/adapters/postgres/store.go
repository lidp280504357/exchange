// Package postgres stores the ledger in the ledger schema.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/ledger/ports"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/outbox"
	"github.com/skill/exchange/internal/platform/pg"
)

// Store implements ports.Store.
type Store struct {
	db     *pg.DB
	events *event.Factory
}

// NewStore returns a store whose events are built by events.
func NewStore(db *pg.DB, events *event.Factory) *Store { return &Store{db: db, events: events} }

// Tx runs fn in a transaction.
func (s *Store) Tx(ctx context.Context, fn func(ports.Repos) error) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error { return fn(repos{q: tx, events: s.events}) })
}

// Read returns repositories on the pool.
func (s *Store) Read() ports.Repos { return repos{q: s.db, events: s.events} }

type repos struct {
	q      pg.Querier
	events *event.Factory
}

func (r repos) Accounts() ports.AccountRepo   { return accounts(r) }
func (r repos) Journals() ports.JournalRepo   { return journals(r) }
func (r repos) Transfers() ports.TransferRepo { return transfers(r) }
func (r repos) Trades() ports.TradeRepo       { return trades(r) }

func (r repos) Emit(ctx context.Context, topic string, msg proto.Message, aggregateType, aggregateID string) error {
	env, err := r.events.New(ctx, msg, aggregateType, aggregateID)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, r.q, topic, env)
}

type accounts repos

const accountColumns = `id, owner_type, owner_id, account_type, scope, asset, available, frozen, version, updated_at`

func scanAccount(row pgx.Row) (domain.Account, error) {
	var a domain.Account
	var id uuid.UUID
	err := row.Scan(&id, &a.Key.OwnerType, &a.Key.OwnerID, &a.Key.Type, &a.Key.Scope, &a.Key.Asset, &a.Available, &a.Frozen, &a.Version,
		&a.UpdatedAt)
	a.ID = id.String()
	return a, err
}

func (r accounts) Lock(ctx context.Context, keys []domain.AccountKey) ([]domain.Account, error) {
	out := make([]domain.Account, 0, len(keys))
	for _, k := range keys {
		if _, err := r.q.Exec(ctx, `INSERT INTO accounts (id, owner_type, owner_id, account_type, scope, asset) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (owner_type, owner_id, account_type, scope, asset) DO NOTHING`,
			uuid.Must(uuid.NewV7()), k.OwnerType, k.OwnerID, k.Type, k.Scope, k.Asset); err != nil {
			return nil, fmt.Errorf("create account %s: %w", k, err)
		}
		a, err := scanAccount(r.q.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts
			WHERE owner_type = $1 AND owner_id = $2 AND account_type = $3 AND scope = $4 AND asset = $5 FOR UPDATE`,
			k.OwnerType, k.OwnerID, k.Type, k.Scope, k.Asset))
		if err != nil {
			return nil, fmt.Errorf("lock account %s: %w", k, err)
		}
		out = append(out, a)
	}
	return out, nil
}

func (r accounts) Save(ctx context.Context, a domain.Account) error {
	if _, err := r.q.Exec(ctx, `UPDATE accounts SET available = $2, frozen = $3, version = $4, updated_at = now() WHERE id = $1`,
		a.ID, a.Available, a.Frozen, a.Version); err != nil {
		return fmt.Errorf("save account %s: %w", a.Key, err)
	}
	return nil
}

func (r accounts) ByOwner(ctx context.Context, ownerID, accountType string) ([]domain.Account, error) {
	return r.query(ctx, `SELECT `+accountColumns+` FROM accounts WHERE owner_id = $1 AND ($2 = '' OR account_type = $2)
		ORDER BY account_type, scope, asset`, ownerID, accountType)
}

func (r accounts) Margin(ctx context.Context, userID string) ([]domain.Account, error) {
	return r.query(ctx, `SELECT `+accountColumns+` FROM accounts WHERE owner_type = 'USER' AND owner_id = $1
		AND account_type LIKE 'MARGIN\_%' ORDER BY account_type, scope, asset`, userID)
}

func (r accounts) MarginDebts(ctx context.Context) ([]domain.Account, error) {
	return r.query(ctx, `SELECT `+accountColumns+` FROM accounts WHERE account_type IN ('MARGIN_CROSS_DEBT', 'MARGIN_CROSS_INTEREST',
		'MARGIN_ISOLATED_DEBT', 'MARGIN_ISOLATED_INTEREST') AND available <> 0 ORDER BY owner_id, account_type, scope, asset`)
}

// holdings is what each user holds of the asset $1, the users holding
// nothing left out.
const holdings = `SELECT owner_id, sum(available + frozen) AS amount FROM accounts
	WHERE owner_type = 'USER' AND asset = $1 GROUP BY owner_id HAVING sum(available + frozen) <> 0`

func (r accounts) Holders(ctx context.Context, asset string, limit int, apart []string) (domain.Holders, error) {
	out := domain.Holders{Top: []domain.Holding{}}
	rows, err := r.q.Query(ctx, holdings+` ORDER BY amount DESC, owner_id LIMIT $2`, asset, limit)
	if err != nil {
		return out, fmt.Errorf("list holders: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h domain.Holding
		if err := rows.Scan(&h.UserID, &h.Amount); err != nil {
			return out, fmt.Errorf("list holders: %w", err)
		}
		out.Top = append(out.Top, h)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("list holders: %w", err)
	}
	if rows, err = r.q.Query(ctx, `SELECT owner_id = ANY($2) AS apart, coalesce(sum(amount), 0), count(*) FILTER (WHERE amount > 0)
		FROM (`+holdings+`) h GROUP BY 1`, asset, apart); err != nil {
		return out, fmt.Errorf("sum holders: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var isApart bool
		var sum domain.HoldingSum
		if err := rows.Scan(&isApart, &sum.Amount, &sum.Holders); err != nil {
			return out, fmt.Errorf("sum holders: %w", err)
		}
		if isApart {
			out.Apart = sum
		} else {
			out.Others = sum
		}
	}
	return out, rows.Err()
}

func (r accounts) query(ctx context.Context, sql string, args ...any) ([]domain.Account, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()
	var out []domain.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("list accounts: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type journals repos

func (r journals) KeyedTotal(ctx context.Context, prefix string, account domain.AccountKey) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := r.q.QueryRow(ctx, `SELECT coalesce(sum(l.amount), 0) FROM journal_lines l
		JOIN accounts a ON a.id = l.account_id JOIN journals j ON j.id = l.journal_id
		WHERE a.owner_type = $2 AND a.owner_id = $3 AND a.account_type = $4 AND a.scope = $5 AND a.asset = $6
			AND starts_with(j.idem_key, $1)`,
		prefix, account.OwnerType, account.OwnerID, account.Type, account.Scope, account.Asset).Scan(&total)
	if err != nil {
		return decimal.Zero, fmt.Errorf("sum keyed journals: %w", err)
	}
	return total, nil
}

func (r journals) Payee(ctx context.Context, journalID string) (string, error) {
	var user string
	err := r.q.QueryRow(ctx, `SELECT a.owner_id FROM journal_lines l JOIN accounts a ON a.id = l.account_id
		WHERE l.journal_id = $1::uuid AND a.owner_type = 'USER' AND l.amount > 0 ORDER BY l.id LIMIT 1`, journalID).Scan(&user)
	if pg.IsNoRows(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load a journal's payee: %w", err)
	}
	return user, nil
}

// ByIdemKey finds the journal posted with key, or the key of one the
// retention run deleted (ADR-0022): its lines are gone, its key, hash and
// ID are what a replay needs.
func (r journals) ByIdemKey(ctx context.Context, key string) (*domain.Journal, error) {
	var j domain.Journal
	var id uuid.UUID
	var source *uuid.UUID
	err := r.q.QueryRow(ctx, `SELECT id, seq, idem_key, request_hash, entry_type, source_event_id, trace_id, memo, posted_at
		FROM journals WHERE idem_key = $1`, key).Scan(&id, &j.Seq, &j.IdemKey, &j.RequestHash, &j.EntryType, &source, &j.TraceID, &j.Memo, &j.PostedAt)
	if pg.IsNoRows(err) {
		return r.deletedKey(ctx, key)
	}
	if err != nil {
		return nil, fmt.Errorf("load journal: %w", err)
	}
	j.ID = id.String()
	if source != nil {
		j.SourceEventID = source.String()
	}
	return &j, nil
}

// deletedKey is the journal of a key the retention run kept, nil when
// there is none.
func (r journals) deletedKey(ctx context.Context, key string) (*domain.Journal, error) {
	var j domain.Journal
	var id uuid.UUID
	err := r.q.QueryRow(ctx, `SELECT journal_id, seq, idem_key, request_hash, entry_type, posted_at FROM journal_keys WHERE idem_key = $1`,
		key).Scan(&id, &j.Seq, &j.IdemKey, &j.RequestHash, &j.EntryType, &j.PostedAt)
	if pg.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load a deleted journal's key: %w", err)
	}
	j.ID = id.String()
	return &j, nil
}

func (r journals) Insert(ctx context.Context, j domain.Journal, lines []domain.PostedLine) (int64, error) {
	var source any
	if j.SourceEventID != "" {
		source = j.SourceEventID
	}
	var seq int64
	if err := r.q.QueryRow(ctx, `INSERT INTO journals (id, idem_key, request_hash, entry_type, source_event_id, trace_id, memo, posted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING seq`,
		j.ID, j.IdemKey, j.RequestHash, j.EntryType, source, j.TraceID, j.Memo, j.PostedAt).Scan(&seq); err != nil {
		return 0, fmt.Errorf("insert journal: %w", err)
	}
	batch := &pgx.Batch{}
	for _, l := range lines {
		batch.Queue(`INSERT INTO journal_lines (journal_id, account_id, asset, amount, balance_kind, available_after, frozen_after, account_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			j.ID, l.Account.ID, l.Account.Key.Asset, l.Amount, l.Kind, l.Account.Available, l.Account.Frozen, l.AccountVersion)
	}
	if err := r.q.SendBatch(ctx, batch).Close(); err != nil {
		return 0, fmt.Errorf("insert journal lines: %w", err)
	}
	return seq, nil
}

// A user's ledger lines, newest first (Entries): $1 the owner, $2 below
// this line id, $3 the asset (empty for all), $4 how many. A trade against HOUSE
// is a trade to its user (ADR-0015): it shows as TRADE_SETTLE.
const (
	entriesColumns = `SELECT l.id, l.journal_id,
		CASE WHEN j.entry_type = 'HOUSE_TRADE_SETTLE' THEN 'TRADE_SETTLE' ELSE j.entry_type END,
		a.account_type, l.asset, l.amount, l.balance_kind, l.available_after, l.frozen_after, j.posted_at`
	entriesOwner = `
		WHERE a.owner_id = $1 AND a.owner_type = 'USER' AND ($3 = '' OR a.asset = $3)
		ORDER BY l.id DESC LIMIT $4`
	// Every type: each account's lines backwards on (account_id,
	// account_version).
	entriesAll = entriesColumns + `
		FROM accounts a CROSS JOIN LATERAL (
			SELECT l.* FROM journal_lines l WHERE l.account_id = a.id AND l.id < $2 ORDER BY l.account_version DESC LIMIT $4
		) l JOIN journals j ON j.id = l.journal_id` + entriesOwner
	// Types $5 and $6 (empty for none): each account's lines of each type
	// backwards on journal_line_types' key (account_id, entry_type, line_id).
	entriesOfTypes = entriesColumns + `
		FROM accounts a CROSS JOIN LATERAL (
			SELECT t.line_id FROM (
				(SELECT t.line_id FROM journal_line_types t
					WHERE t.account_id = a.id AND t.entry_type = $5 AND t.line_id < $2 ORDER BY t.line_id DESC LIMIT $4)
				UNION ALL
				(SELECT t.line_id FROM journal_line_types t
					WHERE t.account_id = a.id AND t.entry_type = $6 AND t.line_id < $2 ORDER BY t.line_id DESC LIMIT $4)
			) t ORDER BY t.line_id DESC LIMIT $4
		) t JOIN journal_lines l ON l.id = t.line_id JOIN journals j ON j.id = l.journal_id` + entriesOwner
)

func (r journals) Entries(ctx context.Context, ownerID, asset, entryType string, beforeID int64, limit int) ([]domain.Entry, error) {
	if beforeID <= 0 {
		beforeID = 1<<63 - 1
	}
	// The newest lines of each of the user's accounts, then the newest of
	// those (entriesAll, entriesOfTypes). Joined the other way the planner
	// walked the whole of journal_lines backwards by id for a user with
	// fewer lines than the page: 58 s over 3.5 million lines on the test
	// server (2026-10-07; 17 ms this way). Within an account a line's
	// version grows with its id (both are taken under the account's lock);
	// filtered by type, one the account seldom has costs no walk through the
	// rest (B141: 4.5 s for a busy bot's transfers before, 30 ms).
	var rows pgx.Rows
	var err error
	if entryType == "" {
		rows, err = r.q.Query(ctx, entriesAll, ownerID, beforeID, asset, limit)
	} else {
		// TRADE_SETTLE covers the trades against HOUSE.
		also := ""
		if entryType == domain.EntryTradeSettle {
			also = domain.EntryHouseTradeSettle
		}
		rows, err = r.q.Query(ctx, entriesOfTypes, ownerID, beforeID, asset, limit, entryType, also)
	}
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()
	var out []domain.Entry
	for rows.Next() {
		var e domain.Entry
		var journal uuid.UUID
		if err := rows.Scan(&e.ID, &journal, &e.EntryType, &e.AccountType, &e.Asset, &e.Amount, &e.Kind, &e.Available, &e.Frozen, &e.PostedAt); err != nil {
			return nil, fmt.Errorf("list entries: %w", err)
		}
		e.JournalID = journal.String()
		out = append(out, e)
	}
	return out, rows.Err()
}

type transfers repos

const transferColumns = `id, user_id, idem_key, request_hash, asset, amount, from_account_type, to_account_type, status,
	failure_reason, journal_id, created_at`

func scanTransfer(row pgx.Row) (domain.Transfer, error) {
	var t domain.Transfer
	var id, user uuid.UUID
	var journal *uuid.UUID
	err := row.Scan(&id, &user, &t.IdemKey, &t.RequestHash, &t.Asset, &t.Amount, &t.From, &t.To, &t.Status, &t.FailureReason, &journal, &t.CreatedAt)
	t.ID, t.UserID = id.String(), user.String()
	if journal != nil {
		t.JournalID = journal.String()
	}
	return t, err
}

func (r transfers) ByKey(ctx context.Context, userID, idemKey string) (*domain.Transfer, error) {
	t, err := scanTransfer(r.q.QueryRow(ctx, `SELECT `+transferColumns+` FROM transfers WHERE user_id = $1 AND idem_key = $2`, userID, idemKey))
	if pg.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load transfer: %w", err)
	}
	return &t, nil
}

func (r transfers) Insert(ctx context.Context, t domain.Transfer) error {
	var journal any
	if t.JournalID != "" {
		journal = t.JournalID
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO transfers (id, user_id, idem_key, request_hash, asset, amount, from_account_type,
		to_account_type, status, failure_reason, journal_id, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		t.ID, t.UserID, t.IdemKey, t.RequestHash, t.Asset, t.Amount, t.From, t.To, t.Status, t.FailureReason, journal, t.CreatedAt); err != nil {
		return fmt.Errorf("insert transfer: %w", err)
	}
	return nil
}

func (r transfers) List(ctx context.Context, userID, beforeID string, limit int) ([]domain.Transfer, error) {
	var before any
	if beforeID != "" {
		before = beforeID
	}
	rows, err := r.q.Query(ctx, `SELECT `+transferColumns+` FROM transfers WHERE user_id = $1 AND ($2::uuid IS NULL OR id < $2::uuid)
		ORDER BY id DESC LIMIT $3`, userID, before, limit)
	if err != nil {
		return nil, fmt.Errorf("list transfers: %w", err)
	}
	defer rows.Close()
	var out []domain.Transfer
	for rows.Next() {
		t, err := scanTransfer(rows)
		if err != nil {
			return nil, fmt.Errorf("list transfers: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type margins repos

func (r repos) Margins() ports.MarginRepo { return margins(r) }

func (r margins) ByKey(ctx context.Context, key string) (*domain.MarginPosting, error) {
	var p domain.MarginPosting
	var journals []byte
	err := r.q.QueryRow(ctx, `SELECT idem_key, user_id, request_hash, reference, journals, created_at FROM margin_postings
		WHERE idem_key = $1`, key).Scan(&p.IdemKey, &p.UserID, &p.RequestHash, &p.Reference, &journals, &p.CreatedAt)
	if pg.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load margin posting: %w", err)
	}
	if err := json.Unmarshal(journals, &p.Journals); err != nil {
		return nil, fmt.Errorf("margin posting %s: %w", key, err)
	}
	return &p, nil
}

func (r margins) Insert(ctx context.Context, p domain.MarginPosting) error {
	journals, err := json.Marshal(p.Journals)
	if err != nil {
		return err
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO margin_postings (idem_key, user_id, request_hash, reference, journals, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, p.IdemKey, p.UserID, p.RequestHash, p.Reference, journals, p.CreatedAt); err != nil {
		return fmt.Errorf("insert margin posting: %w", err)
	}
	return nil
}

type futures repos

func (r repos) Futures() ports.FuturesRepo { return futures(r) }

func (r futures) ByKey(ctx context.Context, key string) (*domain.FuturesSettlement, error) {
	var s domain.FuturesSettlement
	var user uuid.UUID
	var outcomes []byte
	err := r.q.QueryRow(ctx, `SELECT idem_key, user_id, request_hash, reference, outcomes, created_at FROM futures_settlements
		WHERE idem_key = $1`, key).Scan(&s.IdemKey, &user, &s.RequestHash, &s.Reference, &outcomes, &s.CreatedAt)
	if pg.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load futures settlement: %w", err)
	}
	if err := json.Unmarshal(outcomes, &s.Outcomes); err != nil {
		return nil, fmt.Errorf("futures settlement %s: %w", key, err)
	}
	s.UserID = user.String()
	return &s, nil
}

func (r futures) Insert(ctx context.Context, s domain.FuturesSettlement) error {
	outcomes, err := json.Marshal(s.Outcomes)
	if err != nil {
		return err
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO futures_settlements (idem_key, user_id, request_hash, reference, outcomes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, s.IdemKey, s.UserID, s.RequestHash, s.Reference, outcomes, s.CreatedAt); err != nil {
		return fmt.Errorf("insert futures settlement: %w", err)
	}
	return nil
}
