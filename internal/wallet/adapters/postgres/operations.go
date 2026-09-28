package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

func (r repos) Commands() ports.CommandRepo   { return commands(r) }
func (r repos) Sweeps() ports.SweepRepo       { return sweeps(r) }
func (r repos) ChainFees() ports.ChainFeeRepo { return chainFees(r) }
func (r repos) Fundings() ports.FundingRepo   { return fundings(r) }
func (r repos) Checks() ports.CheckRepo       { return checks(r) }

func (r addresses) List(ctx context.Context, network string) ([]domain.Address, error) {
	rows, err := r.q.Query(ctx, `SELECT user_id, network, derivation_index, address, created_at FROM deposit_addresses
		WHERE network = $1 ORDER BY derivation_index`, network)
	if err != nil {
		return nil, fmt.Errorf("list deposit addresses: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Address, error) {
		var a domain.Address
		var idx int32
		err := row.Scan(&a.UserID, &a.Network, &idx, &a.Address, &a.CreatedAt)
		a.Index = uint32(idx) //nolint:gosec // non-negative column
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("list deposit addresses: %w", err)
	}
	return out, nil
}

type commands repos

const commandColumns = `id, network, kind, args, status, result, requested_by, created_at, done_at`

func scanCommand(row pgx.Row) (domain.Command, error) {
	var c domain.Command
	var args []byte
	var done *time.Time
	if err := row.Scan(&c.ID, &c.Network, &c.Kind, &args, &c.Status, &c.Result, &c.RequestedBy, &c.CreatedAt, &done); err != nil {
		return domain.Command{}, err
	}
	if err := json.Unmarshal(args, &c.Args); err != nil {
		return domain.Command{}, fmt.Errorf("command %s args: %w", c.ID, err)
	}
	c.DoneAt = at(done)
	return c, nil
}

func (r commands) Insert(ctx context.Context, c domain.Command) error {
	args, err := json.Marshal(c.Args)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx, `INSERT INTO commands (`+commandColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		c.ID, c.Network, c.Kind, args, c.Status, c.Result, c.RequestedBy, c.CreatedAt, stamp(c.DoneAt))
	if err != nil {
		return fmt.Errorf("insert command: %w", err)
	}
	return nil
}

func (r commands) list(ctx context.Context, sql string, args ...any) ([]domain.Command, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list commands: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Command, error) { return scanCommand(row) })
	if err != nil {
		return nil, fmt.Errorf("list commands: %w", err)
	}
	return out, nil
}

func (r commands) Pending(ctx context.Context, network string) ([]domain.Command, error) {
	return r.list(ctx, `SELECT `+commandColumns+` FROM commands WHERE network = $1 AND status = 'PENDING' ORDER BY created_at`, network)
}

func (r commands) Recent(ctx context.Context, limit int) ([]domain.Command, error) {
	return r.list(ctx, `SELECT `+commandColumns+` FROM commands ORDER BY created_at DESC LIMIT $1`, limit)
}

func (r commands) Update(ctx context.Context, c domain.Command) error {
	_, err := r.q.Exec(ctx, `UPDATE commands SET status = $2, result = $3, done_at = $4 WHERE id = $1`,
		c.ID, c.Status, c.Result, stamp(c.DoneAt))
	if err != nil {
		return fmt.Errorf("update command: %w", err)
	}
	return nil
}

type sweeps repos

const sweepColumns = `id, network, address, derivation_index, asset, amount, nonce, tx_hash, raw_tx, status, command_id, created_at, updated_at`

func (r sweeps) Insert(ctx context.Context, s domain.Sweep) error {
	_, err := r.q.Exec(ctx, `INSERT INTO sweeps (`+sweepColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		s.ID, s.Network, s.Address, int64(s.Index), s.Asset, s.Amount, int64(s.Nonce), s.TxHash, s.Raw, s.Status, //nolint:gosec // nonces fit
		text(s.CommandID), s.CreatedAt, s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert sweep: %w", err)
	}
	return nil
}

func (r sweeps) Update(ctx context.Context, s domain.Sweep) error {
	if _, err := r.q.Exec(ctx, `UPDATE sweeps SET status = $2, updated_at = $3 WHERE id = $1`, s.ID, s.Status, s.UpdatedAt); err != nil {
		return fmt.Errorf("update sweep: %w", err)
	}
	return nil
}

func (r sweeps) Open(ctx context.Context, network string) ([]domain.Sweep, error) {
	rows, err := r.q.Query(ctx, `SELECT `+sweepColumns+` FROM sweeps WHERE network = $1 AND status = 'BROADCAST' ORDER BY created_at`, network)
	if err != nil {
		return nil, fmt.Errorf("list sweeps: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Sweep, error) {
		var s domain.Sweep
		var idx int32
		var nonce int64
		var cmd *string
		err := row.Scan(&s.ID, &s.Network, &s.Address, &idx, &s.Asset, &s.Amount, &nonce, &s.TxHash, &s.Raw, &s.Status, &cmd,
			&s.CreatedAt, &s.UpdatedAt)
		s.Index, s.Nonce, s.CommandID = uint32(idx), uint64(nonce), str(cmd) //nolint:gosec // non-negative columns
		return s, err
	})
	if err != nil {
		return nil, fmt.Errorf("list sweeps: %w", err)
	}
	return out, nil
}

type chainFees repos

func (r chainFees) Insert(ctx context.Context, f domain.ChainFee) error {
	_, err := r.q.Exec(ctx, `INSERT INTO chain_fees (tx_hash, network, asset, amount, purpose, reference) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tx_hash) DO NOTHING`, f.TxHash, f.Network, f.Asset, f.Amount, f.Purpose, f.Reference)
	if err != nil {
		return fmt.Errorf("insert chain fee: %w", err)
	}
	return nil
}

func (r chainFees) Unbooked(ctx context.Context, network string) ([]domain.ChainFee, error) {
	rows, err := r.q.Query(ctx, `SELECT tx_hash, network, asset, amount, purpose, reference FROM chain_fees
		WHERE network = $1 AND booked_at IS NULL ORDER BY created_at`, network)
	if err != nil {
		return nil, fmt.Errorf("list chain fees: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ChainFee, error) {
		var f domain.ChainFee
		err := row.Scan(&f.TxHash, &f.Network, &f.Asset, &f.Amount, &f.Purpose, &f.Reference)
		return f, err
	})
	if err != nil {
		return nil, fmt.Errorf("list chain fees: %w", err)
	}
	return out, nil
}

func (r chainFees) MarkBooked(ctx context.Context, txHash, journalID string) error {
	_, err := r.q.Exec(ctx, `UPDATE chain_fees SET journal_id = $2, booked_at = now() WHERE tx_hash = $1 AND booked_at IS NULL`,
		txHash, journalID)
	if err != nil {
		return fmt.Errorf("mark chain fee booked: %w", err)
	}
	return nil
}

type fundings repos

func (r fundings) Get(ctx context.Context, txHash string) (*domain.Funding, error) {
	var f domain.Funding
	var cmd *string
	err := r.q.QueryRow(ctx, `SELECT tx_hash, network, asset, account_type, amount, journal_id, command_id, created_at FROM fundings
		WHERE tx_hash = $1`, txHash).Scan(&f.TxHash, &f.Network, &f.Asset, &f.AccountType, &f.Amount, &f.JournalID, &cmd, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get funding: %w", err)
	}
	f.CommandID = str(cmd)
	return &f, nil
}

func (r fundings) Insert(ctx context.Context, f domain.Funding) error {
	_, err := r.q.Exec(ctx, `INSERT INTO fundings (tx_hash, network, asset, account_type, amount, journal_id, command_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, f.TxHash, f.Network, f.Asset, f.AccountType, f.Amount, f.JournalID, text(f.CommandID), f.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert funding: %w", err)
	}
	return nil
}

type checks repos

func (r checks) Insert(ctx context.Context, c domain.ChainCheck) error {
	_, err := r.q.Exec(ctx, `INSERT INTO chain_checks (network, asset, chain, ledger, unbooked, shortfall, addresses, checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, c.Network, c.Asset, c.Chain, c.Ledger, c.Unbooked, c.Shortfall, c.Addresses, c.CheckedAt)
	if err != nil {
		return fmt.Errorf("insert chain check: %w", err)
	}
	return nil
}

func (r checks) Latest(ctx context.Context, network string) ([]domain.ChainCheck, error) {
	rows, err := r.q.Query(ctx, `SELECT DISTINCT ON (asset) network, asset, chain, ledger, unbooked, shortfall, addresses, checked_at
		FROM chain_checks WHERE network = $1 ORDER BY asset, checked_at DESC`, network)
	if err != nil {
		return nil, fmt.Errorf("list chain checks: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ChainCheck, error) {
		var c domain.ChainCheck
		err := row.Scan(&c.Network, &c.Asset, &c.Chain, &c.Ledger, &c.Unbooked, &c.Shortfall, &c.Addresses, &c.CheckedAt)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("list chain checks: %w", err)
	}
	return out, nil
}
