package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

func (r repos) Commands() ports.CommandRepo   { return commands(r) }
func (r repos) Sweeps() ports.SweepRepo       { return sweeps(r) }
func (r repos) ChainFees() ports.ChainFeeRepo { return chainFees(r) }
func (r repos) Fundings() ports.FundingRepo   { return fundings(r) }
func (r repos) Checks() ports.CheckRepo       { return checks(r) }

func (r repos) Suspensions() ports.SuspensionRepo { return suspensions(r) }

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

const chainFeeColumns = `tx_hash, network, asset, amount, purpose, reference, journal_id, booked_at, status, hold_reason, resolved_by,
	resolution, resolved_at, created_at`

func scanChainFee(row pgx.CollectableRow) (domain.ChainFee, error) {
	var f domain.ChainFee
	var journal *string
	var booked, resolved *time.Time
	err := row.Scan(&f.TxHash, &f.Network, &f.Asset, &f.Amount, &f.Purpose, &f.Reference, &journal, &booked, &f.Status, &f.HoldReason,
		&f.ResolvedBy, &f.Resolution, &resolved, &f.CreatedAt)
	f.JournalID, f.BookedAt, f.ResolvedAt = str(journal), at(booked), at(resolved)
	return f, err
}

func (r chainFees) list(ctx context.Context, where string, args ...any) ([]domain.ChainFee, error) {
	rows, err := r.q.Query(ctx, `SELECT `+chainFeeColumns+` FROM chain_fees WHERE `+where+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, fmt.Errorf("list chain fees: %w", err)
	}
	out, err := pgx.CollectRows(rows, scanChainFee)
	if err != nil {
		return nil, fmt.Errorf("list chain fees: %w", err)
	}
	return out, nil
}

func (r chainFees) Insert(ctx context.Context, f domain.ChainFee) error {
	status := f.Status
	if status == "" {
		status = domain.FeeBookable
	}
	_, err := r.q.Exec(ctx, `INSERT INTO chain_fees (tx_hash, network, asset, amount, purpose, reference, status, hold_reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (tx_hash) DO NOTHING`,
		f.TxHash, f.Network, f.Asset, f.Amount, f.Purpose, f.Reference, status, f.HoldReason)
	if err != nil {
		return fmt.Errorf("insert chain fee: %w", err)
	}
	return nil
}

func (r chainFees) Unbooked(ctx context.Context, network string) ([]domain.ChainFee, error) {
	return r.list(ctx, `network = $1 AND booked_at IS NULL AND status = 'BOOKABLE'`, network)
}

func (r chainFees) MarkBooked(ctx context.Context, txHash, journalID string) (string, error) {
	rows, err := r.q.Query(ctx, `WITH prev AS (SELECT status FROM chain_fees WHERE tx_hash = $1 AND booked_at IS NULL FOR UPDATE)
		UPDATE chain_fees c SET journal_id = $2, booked_at = now(), status = 'BOOKABLE' FROM prev WHERE c.tx_hash = $1
		RETURNING prev.status`, txHash, journalID)
	if err != nil {
		return "", fmt.Errorf("mark chain fee booked: %w", err)
	}
	was, err := pgx.CollectOneRow(rows, pgx.RowTo[string])
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("mark chain fee booked: %w", err)
	}
	return was, nil
}

func (r chainFees) Held(ctx context.Context) ([]domain.ChainFee, error) {
	return r.list(ctx, `status = 'HELD'`)
}

func (r chainFees) OfReference(ctx context.Context, reference string) ([]domain.ChainFee, error) {
	return r.list(ctx, `reference = $1`, reference)
}

func (r chainFees) Resolve(ctx context.Context, f domain.ChainFee) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE chain_fees SET status = $2, asset = $3, amount = $4, hold_reason = $5, resolved_by = $6, resolution = $7,
		resolved_at = $8 WHERE tx_hash = $1 AND booked_at IS NULL AND status IN ('HELD', 'BOOKABLE')`,
		f.TxHash, f.Status, f.Asset, f.Amount, f.HoldReason, f.ResolvedBy, f.Resolution, f.ResolvedAt)
	if err != nil {
		return false, fmt.Errorf("resolve chain fee: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const feeUnitColumns = `provider, asset, network, unit, confirmed_by, reason, confirmed_at`

func scanFeeUnit(row pgx.CollectableRow) (domain.FeeUnit, error) {
	var u domain.FeeUnit
	err := row.Scan(&u.Provider, &u.Asset, &u.Network, &u.Unit, &u.ConfirmedBy, &u.Reason, &u.ConfirmedAt)
	return u, err
}

func (r chainFees) Unit(ctx context.Context, provider, asset, network string) (*domain.FeeUnit, error) {
	rows, err := r.q.Query(ctx, `SELECT `+feeUnitColumns+` FROM custody_fee_units WHERE provider = $1 AND asset = $2 AND network = $3`,
		provider, asset, network)
	if err != nil {
		return nil, fmt.Errorf("get fee unit: %w", err)
	}
	u, err := pgx.CollectOneRow(rows, scanFeeUnit)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get fee unit: %w", err)
	}
	return &u, nil
}

func (r chainFees) PutUnit(ctx context.Context, u domain.FeeUnit) error {
	_, err := r.q.Exec(ctx, `INSERT INTO custody_fee_units (`+feeUnitColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (provider, asset, network) DO UPDATE SET unit = EXCLUDED.unit, confirmed_by = EXCLUDED.confirmed_by,
			reason = EXCLUDED.reason, confirmed_at = EXCLUDED.confirmed_at`,
		u.Provider, u.Asset, u.Network, u.Unit, u.ConfirmedBy, u.Reason, u.ConfirmedAt)
	if err != nil {
		return fmt.Errorf("put fee unit: %w", err)
	}
	return nil
}

func (r chainFees) Units(ctx context.Context) ([]domain.FeeUnit, error) {
	rows, err := r.q.Query(ctx, `SELECT `+feeUnitColumns+` FROM custody_fee_units ORDER BY provider, asset, network`)
	if err != nil {
		return nil, fmt.Errorf("list fee units: %w", err)
	}
	out, err := pgx.CollectRows(rows, scanFeeUnit)
	if err != nil {
		return nil, fmt.Errorf("list fee units: %w", err)
	}
	return out, nil
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
	_, err := r.q.Exec(ctx, `INSERT INTO chain_checks (network, asset, chain, ledger, unbooked, elsewhere, in_flight, shortfall,
		addresses, checked_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, c.Network, c.Asset, c.Chain, c.Ledger, c.Unbooked,
		c.Elsewhere, c.InFlight, c.Shortfall, c.Addresses, c.CheckedAt)
	if err != nil {
		return fmt.Errorf("insert chain check: %w", err)
	}
	return nil
}

func (r checks) Latest(ctx context.Context, network string) ([]domain.ChainCheck, error) {
	rows, err := r.q.Query(ctx, `SELECT DISTINCT ON (network, asset) network, asset, chain, ledger, unbooked, elsewhere, in_flight,
		shortfall, addresses, checked_at
		FROM chain_checks WHERE $1 = '' OR network = $1 ORDER BY network, asset, checked_at DESC`, network)
	if err != nil {
		return nil, fmt.Errorf("list chain checks: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ChainCheck, error) {
		var c domain.ChainCheck
		err := row.Scan(&c.Network, &c.Asset, &c.Chain, &c.Ledger, &c.Unbooked, &c.Elsewhere, &c.InFlight, &c.Shortfall, &c.Addresses,
			&c.CheckedAt)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("list chain checks: %w", err)
	}
	return out, nil
}

type suspensions repos

const suspensionColumns = `asset, shortfall, reason, suspended_by, suspended_at`

func scanSuspension(row pgx.CollectableRow) (domain.Suspension, error) {
	var x domain.Suspension
	err := row.Scan(&x.Asset, &x.Shortfall, &x.Reason, &x.SuspendedBy, &x.SuspendedAt)
	return x, err
}

func (r suspensions) Get(ctx context.Context, asset string) (*domain.Suspension, error) {
	rows, err := r.q.Query(ctx, `SELECT `+suspensionColumns+` FROM withdrawal_suspensions WHERE asset = $1`, asset)
	if err != nil {
		return nil, fmt.Errorf("get suspension: %w", err)
	}
	x, err := pgx.CollectOneRow(rows, scanSuspension)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get suspension: %w", err)
	}
	return &x, nil
}

func (r suspensions) List(ctx context.Context) ([]domain.Suspension, error) {
	rows, err := r.q.Query(ctx, `SELECT `+suspensionColumns+` FROM withdrawal_suspensions ORDER BY asset`)
	if err != nil {
		return nil, fmt.Errorf("list suspensions: %w", err)
	}
	out, err := pgx.CollectRows(rows, scanSuspension)
	if err != nil {
		return nil, fmt.Errorf("list suspensions: %w", err)
	}
	return out, nil
}

func (r suspensions) Put(ctx context.Context, x domain.Suspension) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO withdrawal_suspensions (`+suspensionColumns+`) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (asset) DO NOTHING`, x.Asset, x.Shortfall, x.Reason, x.SuspendedBy, x.SuspendedAt)
	if err != nil {
		return false, fmt.Errorf("put suspension: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r suspensions) Delete(ctx context.Context, asset string) (bool, error) {
	tag, err := r.q.Exec(ctx, `DELETE FROM withdrawal_suspensions WHERE asset = $1`, asset)
	if err != nil {
		return false, fmt.Errorf("delete suspension: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const watchColumns = `asset, suspect_since, accepted, accepted_until, accepted_by`

func scanWatch(row pgx.CollectableRow) (domain.ShortfallWatch, error) {
	var w domain.ShortfallWatch
	var since, until *time.Time
	if err := row.Scan(&w.Asset, &since, &w.Accepted, &until, &w.AcceptedBy); err != nil {
		return w, err
	}
	if since != nil {
		w.SuspectSince = *since
	}
	if until != nil {
		w.AcceptedUntil = *until
	}
	return w, nil
}

func (r suspensions) Watch(ctx context.Context, asset string) (domain.ShortfallWatch, error) {
	rows, err := r.q.Query(ctx, `SELECT `+watchColumns+` FROM shortfall_watch WHERE asset = $1`, asset)
	if err != nil {
		return domain.ShortfallWatch{}, fmt.Errorf("get shortfall watch: %w", err)
	}
	w, err := pgx.CollectOneRow(rows, scanWatch)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ShortfallWatch{Asset: asset, Accepted: decimal.Zero}, nil
	}
	if err != nil {
		return domain.ShortfallWatch{}, fmt.Errorf("get shortfall watch: %w", err)
	}
	return w, nil
}

func (r suspensions) Watches(ctx context.Context) ([]domain.ShortfallWatch, error) {
	rows, err := r.q.Query(ctx, `SELECT `+watchColumns+` FROM shortfall_watch
		WHERE suspect_since IS NOT NULL OR accepted > 0 ORDER BY asset`)
	if err != nil {
		return nil, fmt.Errorf("list shortfall watches: %w", err)
	}
	out, err := pgx.CollectRows(rows, scanWatch)
	if err != nil {
		return nil, fmt.Errorf("list shortfall watches: %w", err)
	}
	return out, nil
}

func (r suspensions) Suspect(ctx context.Context, asset string, now time.Time) (time.Time, error) {
	var since time.Time
	err := r.q.QueryRow(ctx, `INSERT INTO shortfall_watch (asset, suspect_since) VALUES ($1, $2)
		ON CONFLICT (asset) DO UPDATE SET suspect_since = COALESCE(shortfall_watch.suspect_since, EXCLUDED.suspect_since)
		RETURNING suspect_since`, asset, now).Scan(&since)
	if err != nil {
		return time.Time{}, fmt.Errorf("suspect shortfall: %w", err)
	}
	return since, nil
}

func (r suspensions) Clear(ctx context.Context, asset string, suspicion, accepted bool) error {
	_, err := r.q.Exec(ctx, `UPDATE shortfall_watch SET
		suspect_since = CASE WHEN $2 THEN NULL ELSE suspect_since END,
		accepted = CASE WHEN $3 THEN 0 ELSE accepted END,
		accepted_until = CASE WHEN $3 THEN NULL ELSE accepted_until END,
		accepted_by = CASE WHEN $3 THEN '' ELSE accepted_by END
		WHERE asset = $1`, asset, suspicion, accepted)
	if err != nil {
		return fmt.Errorf("clear shortfall watch: %w", err)
	}
	return nil
}

func (r suspensions) Accept(ctx context.Context, asset string, amount decimal.Decimal, until time.Time, by string) error {
	_, err := r.q.Exec(ctx, `INSERT INTO shortfall_watch (asset, accepted, accepted_until, accepted_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (asset) DO UPDATE SET accepted = EXCLUDED.accepted, accepted_until = EXCLUDED.accepted_until,
		accepted_by = EXCLUDED.accepted_by`, asset, amount, until, by)
	if err != nil {
		return fmt.Errorf("accept shortfall: %w", err)
	}
	return nil
}
