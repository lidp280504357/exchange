// Package postgres stores the wallet in the wallet schema.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/outbox"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
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

func (r repos) Addresses() ports.AddressRepo { return addresses(r) }
func (r repos) Deposits() ports.DepositRepo  { return deposits(r) }
func (r repos) Blocks() ports.BlockRepo      { return blocks(r) }

func (r repos) Emit(ctx context.Context, msg proto.Message, userID string) error {
	env, err := r.events.New(ctx, msg, "user", userID)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, r.q, event.TopicWalletDeposit, env)
}

type addresses repos

func (r addresses) Get(ctx context.Context, userID, network string) (*domain.Address, error) {
	var a domain.Address
	var idx int32
	err := r.q.QueryRow(ctx, `SELECT user_id, network, derivation_index, address, created_at FROM deposit_addresses
		WHERE user_id = $1 AND network = $2`, userID, network).Scan(&a.UserID, &a.Network, &idx, &a.Address, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get deposit address: %w", err)
	}
	a.Index = uint32(idx) //nolint:gosec // the column is non-negative
	return &a, nil
}

func (r addresses) NextIndex(ctx context.Context, network string) (uint32, error) {
	var next int32
	err := r.q.QueryRow(ctx, `INSERT INTO address_indexes (network, next_index) VALUES ($1, 1)
		ON CONFLICT (network) DO UPDATE SET next_index = address_indexes.next_index + 1
		RETURNING next_index - 1`, network).Scan(&next)
	if err != nil {
		return 0, fmt.Errorf("reserve address index: %w", err)
	}
	return uint32(next), nil //nolint:gosec // the column is non-negative
}

func (r addresses) Insert(ctx context.Context, a domain.Address) error {
	_, err := r.q.Exec(ctx, `INSERT INTO deposit_addresses (user_id, network, derivation_index, address, created_at)
		VALUES ($1, $2, $3, $4, $5)`, a.UserID, a.Network, int64(a.Index), a.Address, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert deposit address: %w", err)
	}
	return nil
}

func (r addresses) Owners(ctx context.Context, network string) (map[string]string, error) {
	rows, err := r.q.Query(ctx, `SELECT lower(address), user_id FROM deposit_addresses WHERE network = $1`, network)
	if err != nil {
		return nil, fmt.Errorf("list deposit addresses: %w", err)
	}
	out := map[string]string{}
	var addr, user string
	_, err = pgx.ForEachRow(rows, []any{&addr, &user}, func() error {
		out[addr] = user
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list deposit addresses: %w", err)
	}
	return out, nil
}

type deposits repos

const depositColumns = `id, user_id, asset, network, address, contract, tx_hash, log_index, block_number, block_hash, amount,
	raw_amount, confirmations, required_confirmations, unclaimed, reason, status, journal_id, credit_requested_at, detected_at,
	confirmed_at, credited_at`

func scanDeposit(row pgx.Row) (domain.Deposit, error) {
	var d domain.Deposit
	var asset, contract, reason, journal *string
	var block int64
	var conf, required int32
	var requested, confirmed, credited *time.Time
	err := row.Scan(&d.ID, &d.UserID, &asset, &d.Network, &d.Address, &contract, &d.TxHash, &d.LogIndex, &block, &d.BlockHash,
		&d.Amount, &d.RawAmount, &conf, &required, &d.Unclaimed, &reason, &d.Status, &journal, &requested, &d.DetectedAt,
		&confirmed, &credited)
	if err != nil {
		return domain.Deposit{}, err
	}
	d.Asset, d.Contract, d.Reason, d.JournalID = str(asset), str(contract), str(reason), str(journal)
	d.BlockNumber, d.Confirmations, d.Required = uint64(block), uint32(conf), uint32(required) //nolint:gosec // non-negative columns
	d.CreditRequested, d.ConfirmedAt, d.CreditedAt = at(requested), at(confirmed), at(credited)
	return d, nil
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func text(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func at(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

func stamp(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (r deposits) Insert(ctx context.Context, d domain.Deposit) error {
	_, err := r.q.Exec(ctx, `INSERT INTO deposits (`+depositColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)`,
		d.ID, d.UserID, text(d.Asset), d.Network, d.Address, text(d.Contract), d.TxHash, d.LogIndex, int64(d.BlockNumber), //nolint:gosec // block heights fit
		d.BlockHash, d.Amount, d.RawAmount, int64(d.Confirmations), int64(d.Required), d.Unclaimed, text(d.Reason), d.Status,
		text(d.JournalID), stamp(d.CreditRequested), d.DetectedAt, stamp(d.ConfirmedAt), stamp(d.CreditedAt))
	if err != nil {
		return fmt.Errorf("insert deposit: %w", err)
	}
	return nil
}

func (r deposits) Update(ctx context.Context, d domain.Deposit) error {
	_, err := r.q.Exec(ctx, `UPDATE deposits SET block_number = $2, block_hash = $3, confirmations = $4, unclaimed = $5,
		reason = $6, status = $7, journal_id = $8, credit_requested_at = $9, confirmed_at = $10, credited_at = $11, updated_at = now()
		WHERE id = $1`,
		d.ID, int64(d.BlockNumber), d.BlockHash, int64(d.Confirmations), d.Unclaimed, text(d.Reason), d.Status, //nolint:gosec // block heights fit
		text(d.JournalID), stamp(d.CreditRequested), stamp(d.ConfirmedAt), stamp(d.CreditedAt))
	if err != nil {
		return fmt.Errorf("update deposit: %w", err)
	}
	return nil
}

func (r deposits) one(ctx context.Context, sql string, args ...any) (*domain.Deposit, error) {
	d, err := scanDeposit(r.q.QueryRow(ctx, sql, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get deposit: %w", err)
	}
	return &d, nil
}

func (r deposits) Find(ctx context.Context, network, txHash string, logIndex int64) (*domain.Deposit, error) {
	return r.one(ctx, `SELECT `+depositColumns+` FROM deposits WHERE network = $1 AND tx_hash = $2 AND log_index = $3`,
		network, strings.ToLower(txHash), logIndex)
}

func (r deposits) GetForUpdate(ctx context.Context, id string) (*domain.Deposit, error) {
	return r.one(ctx, `SELECT `+depositColumns+` FROM deposits WHERE id = $1 FOR UPDATE`, id)
}

func (r deposits) list(ctx context.Context, sql string, args ...any) ([]domain.Deposit, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list deposits: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Deposit, error) { return scanDeposit(row) })
	if err != nil {
		return nil, fmt.Errorf("list deposits: %w", err)
	}
	return out, nil
}

func (r deposits) Pending(ctx context.Context, network string) ([]domain.Deposit, error) {
	return r.list(ctx, `SELECT `+depositColumns+` FROM deposits WHERE network = $1 AND status IN ('DETECTED', 'CONFIRMING')
		ORDER BY id`, network)
}

func (r deposits) Unrequested(ctx context.Context, network string) ([]domain.Deposit, error) {
	return r.list(ctx, `SELECT `+depositColumns+` FROM deposits WHERE network = $1 AND status = 'CONFIRMED'
		AND credit_requested_at IS NULL ORDER BY id`, network)
}

func (r deposits) FromBlock(ctx context.Context, network string, n uint64) ([]domain.Deposit, error) {
	return r.list(ctx, `SELECT `+depositColumns+` FROM deposits WHERE network = $1 AND status IN ('DETECTED', 'CONFIRMING')
		AND block_number >= $2 ORDER BY id FOR UPDATE`, network, int64(n)) //nolint:gosec // block heights fit
}

func (r deposits) ByUser(ctx context.Context, userID, before string, limit int) ([]domain.Deposit, error) {
	if before == "" {
		return r.list(ctx, `SELECT `+depositColumns+` FROM deposits WHERE user_id = $1 ORDER BY id DESC LIMIT $2`, userID, limit)
	}
	return r.list(ctx, `SELECT `+depositColumns+` FROM deposits WHERE user_id = $1 AND id < $2 ORDER BY id DESC LIMIT $3`,
		userID, before, limit)
}

type blocks repos

func (r blocks) Cursor(ctx context.Context, network string) (uint64, error) {
	var n int64
	err := r.q.QueryRow(ctx, `SELECT block FROM scan_cursors WHERE network = $1`, network).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read scan cursor: %w", err)
	}
	return uint64(n), nil //nolint:gosec // non-negative column
}

func (r blocks) Hash(ctx context.Context, network string, n uint64) (string, error) {
	var h string
	err := r.q.QueryRow(ctx, `SELECT hash FROM scanned_blocks WHERE network = $1 AND number = $2`, network, int64(n)).Scan(&h) //nolint:gosec // block heights fit
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read block hash: %w", err)
	}
	return h, nil
}

func (r blocks) Save(ctx context.Context, network string, n uint64, hash string) error {
	_, err := r.q.Exec(ctx, `WITH b AS (
			INSERT INTO scanned_blocks (network, number, hash) VALUES ($1, $2, $3)
			ON CONFLICT (network, number) DO UPDATE SET hash = excluded.hash
		)
		INSERT INTO scan_cursors (network, block) VALUES ($1, $2)
		ON CONFLICT (network) DO UPDATE SET block = excluded.block, updated_at = now()`, network, int64(n), hash) //nolint:gosec // block heights fit
	if err != nil {
		return fmt.Errorf("save scanned block: %w", err)
	}
	return nil
}

func (r blocks) Rewind(ctx context.Context, network string, n uint64) error {
	_, err := r.q.Exec(ctx, `WITH d AS (DELETE FROM scanned_blocks WHERE network = $1 AND number >= $2)
		UPDATE scan_cursors SET block = $2 - 1, updated_at = now() WHERE network = $1`, network, int64(n)) //nolint:gosec // block heights fit
	if err != nil {
		return fmt.Errorf("rewind scan: %w", err)
	}
	return nil
}

func (r blocks) Prune(ctx context.Context, network string, below uint64) error {
	_, err := r.q.Exec(ctx, `DELETE FROM scanned_blocks WHERE network = $1 AND number < $2`, network, int64(below)) //nolint:gosec // block heights fit
	if err != nil {
		return fmt.Errorf("prune scanned blocks: %w", err)
	}
	return nil
}
