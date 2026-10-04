package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/outbox"
	"github.com/skill/exchange/internal/wallet/domain"
	"github.com/skill/exchange/internal/wallet/ports"
)

func (r repos) WithdrawAddresses() ports.WithdrawAddressRepo { return withdrawAddresses(r) }
func (r repos) Withdrawals() ports.WithdrawalRepo            { return withdrawals(r) }
func (r repos) Attempts() ports.AttemptRepo                  { return attempts(r) }
func (r repos) Nonces() ports.NonceRepo                      { return nonces(r) }
func (r repos) Prices() ports.PriceRepo                      { return prices(r) }

func (r repos) EmitWithdrawal(ctx context.Context, msg proto.Message, userID string) error {
	if userID == domain.NoOwner {
		return errNoOwnerEvent
	}
	env, err := r.events.New(ctx, msg, "user", userID)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, r.q, event.TopicWalletWithdrawal, env)
}

func (r repos) Audit(ctx context.Context, msg proto.Message, actor string) error {
	env, err := r.events.New(ctx, msg, "actor", actor)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, r.q, event.TopicAudit, env)
}

type withdrawAddresses repos

const addressColumns = `id, user_id, network, address, label, created_at, usable_at`

func scanWithdrawAddress(row pgx.Row) (domain.WithdrawAddress, error) {
	var a domain.WithdrawAddress
	err := row.Scan(&a.ID, &a.UserID, &a.Network, &a.Address, &a.Label, &a.CreatedAt, &a.UsableAt)
	return a, err
}

func (r withdrawAddresses) Insert(ctx context.Context, a domain.WithdrawAddress) error {
	_, err := r.q.Exec(ctx, `INSERT INTO withdraw_addresses (`+addressColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		a.ID, a.UserID, a.Network, a.Address, a.Label, a.CreatedAt, a.UsableAt)
	if err != nil {
		return fmt.Errorf("insert withdrawal address: %w", err)
	}
	return nil
}

func (r withdrawAddresses) List(ctx context.Context, userID string) ([]domain.WithdrawAddress, error) {
	rows, err := r.q.Query(ctx, `SELECT `+addressColumns+` FROM withdraw_addresses WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list withdrawal addresses: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.WithdrawAddress, error) { return scanWithdrawAddress(row) })
	if err != nil {
		return nil, fmt.Errorf("list withdrawal addresses: %w", err)
	}
	return out, nil
}

func (r withdrawAddresses) Find(ctx context.Context, userID, network, address string) (*domain.WithdrawAddress, error) {
	a, err := scanWithdrawAddress(r.q.QueryRow(ctx, `SELECT `+addressColumns+` FROM withdraw_addresses
		WHERE user_id = $1 AND network = $2 AND lower(address) = lower($3) AND deleted_at IS NULL`, userID, network, address))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find withdrawal address: %w", err)
	}
	return &a, nil
}

func (r withdrawAddresses) Delete(ctx context.Context, userID, id string, now time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE withdraw_addresses SET deleted_at = $3 WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
		id, userID, now)
	if err != nil {
		return false, fmt.Errorf("delete withdrawal address: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

type withdrawals repos

const withdrawalColumns = `id, user_id, asset, network, address, amount, fee, internal_user_id, status, risk_score, risk_reasons,
	approvals_required, approvals, reject_reason, value_usdt, nonce, tx_hash, block_number, confirmations, required_confirmations,
	freeze_journal_id, settle_journal_id, unfreeze_journal_id, created_at, updated_at, approved_at, broadcast_at, confirmed_at,
	provider, provider_status, submitted_at, held_at, held_by, hold_note`

func scanWithdrawal(row pgx.Row) (domain.Withdrawal, error) {
	var w domain.Withdrawal
	var internal, txHash, freeze, settle, unfreeze *string
	var nonce, block *int64
	var score, required, conf, reqConf int32
	var approved, broadcast, confirmed, submitted, held *time.Time
	err := row.Scan(&w.ID, &w.UserID, &w.Asset, &w.Network, &w.Address, &w.Amount, &w.Fee, &internal, &w.Status, &score,
		&w.RiskReasons, &required, &w.Approvals, &w.RejectReason, &w.ValueUSDT, &nonce, &txHash, &block, &conf, &reqConf,
		&freeze, &settle, &unfreeze, &w.CreatedAt, &w.UpdatedAt, &approved, &broadcast, &confirmed, &w.Provider, &w.ProviderStatus,
		&submitted, &held, &w.HeldBy, &w.HoldNote)
	if err != nil {
		return domain.Withdrawal{}, err
	}
	w.HeldAt = at(held)
	w.InternalUserID, w.TxHash = str(internal), str(txHash)
	w.FreezeJournal, w.SettleJournal, w.UnfreezeJournal = str(freeze), str(settle), str(unfreeze)
	w.RiskScore, w.ApprovalsRequired = int(score), int(required)
	w.Confirmations, w.Required = uint32(conf), uint32(reqConf) //nolint:gosec // non-negative columns
	w.Nonce = -1
	if nonce != nil {
		w.Nonce = *nonce
	}
	if block != nil {
		w.BlockNumber = uint64(*block) //nolint:gosec // non-negative column
	}
	w.ApprovedAt, w.BroadcastAt, w.ConfirmedAt, w.SubmittedAt = at(approved), at(broadcast), at(confirmed), at(submitted)
	return w, nil
}

func nonceArg(n int64) *int64 {
	if n < 0 {
		return nil
	}
	return &n
}

func blockArg(n uint64) *int64 {
	if n == 0 {
		return nil
	}
	v := int64(n) //nolint:gosec // block heights fit
	return &v
}

func (r withdrawals) Insert(ctx context.Context, w domain.Withdrawal) error {
	_, err := r.q.Exec(ctx, `INSERT INTO withdrawals (`+withdrawalColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26,
			$27, $28, $29, $30, $31, $32, $33, $34)`,
		w.ID, w.UserID, w.Asset, w.Network, w.Address, w.Amount, w.Fee, text(w.InternalUserID), w.Status, w.RiskScore,
		nonNil(w.RiskReasons), w.ApprovalsRequired, nonNil(w.Approvals), w.RejectReason, w.ValueUSDT, nonceArg(w.Nonce), text(w.TxHash),
		blockArg(w.BlockNumber), int64(w.Confirmations), int64(w.Required), text(w.FreezeJournal), text(w.SettleJournal),
		text(w.UnfreezeJournal), w.CreatedAt, w.UpdatedAt, stamp(w.ApprovedAt), stamp(w.BroadcastAt), stamp(w.ConfirmedAt), w.Provider,
		w.ProviderStatus, stamp(w.SubmittedAt), stamp(w.HeldAt), w.HeldBy, w.HoldNote)
	if err != nil {
		return fmt.Errorf("insert withdrawal: %w", err)
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (r withdrawals) Update(ctx context.Context, w domain.Withdrawal) error {
	_, err := r.q.Exec(ctx, `UPDATE withdrawals SET status = $2, risk_score = $3, risk_reasons = $4, approvals_required = $5,
		approvals = $6, reject_reason = $7, nonce = $8, tx_hash = $9, block_number = $10, confirmations = $11,
		freeze_journal_id = $12, settle_journal_id = $13, unfreeze_journal_id = $14, updated_at = $15, approved_at = $16,
		broadcast_at = $17, confirmed_at = $18, provider_status = $19, submitted_at = $20, held_at = $21, held_by = $22,
		hold_note = $23 WHERE id = $1`,
		w.ID, w.Status, w.RiskScore, nonNil(w.RiskReasons), w.ApprovalsRequired, nonNil(w.Approvals), w.RejectReason,
		nonceArg(w.Nonce), text(w.TxHash), blockArg(w.BlockNumber), int64(w.Confirmations), text(w.FreezeJournal),
		text(w.SettleJournal), text(w.UnfreezeJournal), w.UpdatedAt, stamp(w.ApprovedAt), stamp(w.BroadcastAt), stamp(w.ConfirmedAt),
		w.ProviderStatus, stamp(w.SubmittedAt), stamp(w.HeldAt), w.HeldBy, w.HoldNote)
	if err != nil {
		return fmt.Errorf("update withdrawal: %w", err)
	}
	return nil
}

func (r withdrawals) one(ctx context.Context, sql string, args ...any) (*domain.Withdrawal, error) {
	w, err := scanWithdrawal(r.q.QueryRow(ctx, sql, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get withdrawal: %w", err)
	}
	return &w, nil
}

func (r withdrawals) Get(ctx context.Context, id string) (*domain.Withdrawal, error) {
	return r.one(ctx, `SELECT `+withdrawalColumns+` FROM withdrawals WHERE id = $1`, id)
}

func (r withdrawals) GetForUpdate(ctx context.Context, id string) (*domain.Withdrawal, error) {
	return r.one(ctx, `SELECT `+withdrawalColumns+` FROM withdrawals WHERE id = $1 FOR UPDATE`, id)
}

func (r withdrawals) list(ctx context.Context, sql string, args ...any) ([]domain.Withdrawal, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list withdrawals: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Withdrawal, error) { return scanWithdrawal(row) })
	if err != nil {
		return nil, fmt.Errorf("list withdrawals: %w", err)
	}
	return out, nil
}

func (r withdrawals) ByUser(ctx context.Context, userID, before string, limit int) ([]domain.Withdrawal, error) {
	if before == "" {
		return r.list(ctx, `SELECT `+withdrawalColumns+` FROM withdrawals WHERE user_id = $1 ORDER BY id DESC LIMIT $2`, userID, limit)
	}
	return r.list(ctx, `SELECT `+withdrawalColumns+` FROM withdrawals WHERE user_id = $1 AND id < $2 ORDER BY id DESC LIMIT $3`,
		userID, before, limit)
}

func (r withdrawals) ByStatus(ctx context.Context, network string, statuses ...string) ([]domain.Withdrawal, error) {
	return r.list(ctx, `SELECT `+withdrawalColumns+` FROM withdrawals WHERE network = $1 AND status = ANY($2) ORDER BY id`,
		network, statuses)
}

func (r withdrawals) Submitted(ctx context.Context, provider string) ([]domain.Withdrawal, error) {
	return r.list(ctx, `SELECT `+withdrawalColumns+` FROM withdrawals WHERE status = 'SUBMITTED' AND provider = $1 ORDER BY id`, provider)
}

func (r withdrawals) Page(ctx context.Context, network string, f ports.WithdrawalFilter) ([]domain.Withdrawal, error) {
	var user, after *uuid.UUID
	for _, v := range []struct {
		dst **uuid.UUID
		src string
	}{{&user, f.UserID}, {&after, f.After}} {
		if v.src == "" {
			continue
		}
		id, err := uuid.Parse(v.src)
		if err != nil {
			return nil, apperr.Invalid("not an ID: " + v.src)
		}
		*v.dst = &id
	}
	order, cmp := "DESC", "<"
	if f.Oldest {
		order, cmp = "ASC", ">"
	}
	var minValue, maxValue *decimal.Decimal
	if f.MinValue.IsPositive() {
		minValue = &f.MinValue
	}
	if f.MaxValue.IsPositive() {
		maxValue = &f.MaxValue
	}
	return r.list(ctx, `SELECT `+withdrawalColumns+` FROM withdrawals WHERE ($1 = '' OR network = $1) AND ($2 = '' OR status = $2)
		AND ($3::uuid IS NULL OR user_id = $3) AND ($4 = '' OR asset = $4) AND ($5::uuid IS NULL OR id `+cmp+` $5)
		AND ($7 = '' OR (held_at IS NOT NULL AND status = 'PENDING_REVIEW') = ($7 = 'true')) AND ($8::numeric IS NULL OR value_usdt >= $8)
		AND ($9::numeric IS NULL OR value_usdt <= $9) AND risk_score >= $10
		ORDER BY id `+order+` LIMIT $6`, network, f.Status, user, f.Asset, after, f.Limit, f.Held, minValue, maxValue, f.MinRisk)
}

func (r withdrawals) Unreleased(ctx context.Context, network string) ([]domain.Withdrawal, error) {
	return r.list(ctx, `SELECT `+withdrawalColumns+` FROM withdrawals WHERE network = $1
		AND (status IN ('REJECTED', 'CANCELED') OR (status = 'FAILED' AND tx_hash IS NULL))
		AND freeze_journal_id IS NOT NULL AND unfreeze_journal_id IS NULL ORDER BY id`, network)
}

func (r withdrawals) Outstanding(ctx context.Context, provider string) ([]domain.Withdrawal, error) {
	return r.list(ctx, `SELECT `+withdrawalColumns+` FROM withdrawals WHERE provider = $1
		AND (status = 'SUBMITTED' OR (status = 'CONFIRMED' AND settle_journal_id IS NULL)) ORDER BY id`, provider)
}

func (r withdrawals) Unsettled(ctx context.Context, network string) ([]domain.Withdrawal, error) {
	return r.list(ctx, `SELECT `+withdrawalColumns+` FROM withdrawals WHERE network = $1
		AND status IN ('BROADCAST', 'CONFIRMING', 'CONFIRMED') AND internal_user_id IS NULL AND settle_journal_id IS NULL ORDER BY id`, network)
}

func (r withdrawals) ValueSince(ctx context.Context, userID string, t time.Time) (decimal.Decimal, error) {
	var sum decimal.Decimal
	err := r.q.QueryRow(ctx, `SELECT coalesce(sum(value_usdt), 0) FROM withdrawals WHERE user_id = $1 AND created_at >= $2
		AND status NOT IN ('REJECTED', 'CANCELED')`, userID, t).Scan(&sum)
	if err != nil {
		return decimal.Zero, fmt.Errorf("sum withdrawals: %w", err)
	}
	return sum, nil
}

type attempts repos

func (r attempts) Insert(ctx context.Context, a domain.Attempt) error {
	_, err := r.q.Exec(ctx, `INSERT INTO withdrawal_attempts (tx_hash, withdrawal_id, nonce, max_fee, max_tip, raw_tx, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, strings.ToLower(a.TxHash), a.WithdrawalID, int64(a.Nonce), //nolint:gosec // nonces fit
		decimal.NewFromBigInt(a.MaxFee, 0), decimal.NewFromBigInt(a.MaxTip, 0), a.Raw, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert withdrawal attempt: %w", err)
	}
	return nil
}

func (r attempts) Of(ctx context.Context, withdrawalID string) ([]domain.Attempt, error) {
	rows, err := r.q.Query(ctx, `SELECT tx_hash, withdrawal_id, nonce, max_fee, max_tip, raw_tx, created_at FROM withdrawal_attempts
		WHERE withdrawal_id = $1 ORDER BY created_at DESC`, withdrawalID)
	if err != nil {
		return nil, fmt.Errorf("list withdrawal attempts: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Attempt, error) {
		var a domain.Attempt
		var nonce int64
		var maxFee, maxTip decimal.Decimal
		err := row.Scan(&a.TxHash, &a.WithdrawalID, &nonce, &maxFee, &maxTip, &a.Raw, &a.CreatedAt)
		a.Nonce, a.MaxFee, a.MaxTip = uint64(nonce), maxFee.BigInt(), maxTip.BigInt() //nolint:gosec // non-negative column
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("list withdrawal attempts: %w", err)
	}
	return out, nil
}

type nonces repos

func (r nonces) Peek(ctx context.Context, address string) (uint64, error) {
	var n int64
	err := r.q.QueryRow(ctx, `SELECT next_nonce FROM nonces WHERE address = lower($1)`, address).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read nonce: %w", err)
	}
	return uint64(n), nil //nolint:gosec // non-negative column
}

func (r nonces) Advance(ctx context.Context, address string, next uint64) error {
	_, err := r.q.Exec(ctx, `INSERT INTO nonces (address, next_nonce, updated_at) VALUES (lower($1), $2, now())
		ON CONFLICT (address) DO UPDATE SET next_nonce = greatest(nonces.next_nonce, excluded.next_nonce), updated_at = now()`,
		address, int64(next)) //nolint:gosec // nonces fit
	if err != nil {
		return fmt.Errorf("advance nonce: %w", err)
	}
	return nil
}

type prices repos

func (r prices) Get(ctx context.Context, day time.Time, asset string) (*decimal.Decimal, error) {
	var p decimal.Decimal
	err := r.q.QueryRow(ctx, `SELECT usdt FROM price_snapshots WHERE day = $1 AND asset = $2`, day, asset).Scan(&p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get price: %w", err)
	}
	return &p, nil
}

func (r prices) Put(ctx context.Context, day time.Time, asset string, price decimal.Decimal, source string) (decimal.Decimal, error) {
	var p decimal.Decimal
	err := r.q.QueryRow(ctx, `INSERT INTO price_snapshots (day, asset, usdt, source) VALUES ($1, $2, $3, $4)
		ON CONFLICT (day, asset) DO UPDATE SET day = excluded.day RETURNING usdt`, day, asset, price, source).Scan(&p)
	if err != nil {
		return decimal.Zero, fmt.Errorf("put price: %w", err)
	}
	return p, nil
}
