package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
)

type borrows repos

const borrowColumns = `borrow_id, user_id, account_type, symbol, asset, amount, interest_model, hourly_rate, first_interest,
	order_id, idem_key, request_hash, status, failure, created_at, done_at`

func scanBorrow(row pgx.Row) (ports.Borrow, error) {
	var b ports.Borrow
	var accountType, symbol string
	var order *string
	var done *time.Time
	if err := row.Scan(&b.ID, &b.UserID, &accountType, &symbol, &b.Asset, &b.Amount, &b.Model, &b.Rate, &b.FirstInterest, &order,
		&b.IdemKey, &b.RequestHash, &b.Status, &b.Failure, &b.CreatedAt, &done); err != nil {
		return ports.Borrow{}, err
	}
	b.Account, b.OrderID, b.DoneAt = account(accountType, symbol), stringOf(order), timeOf(done)
	return b, nil
}

func (r borrows) Insert(ctx context.Context, b ports.Borrow) error {
	_, err := r.q.Exec(ctx, `INSERT INTO borrows (`+borrowColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		b.ID, b.UserID, b.Account.Type, b.Account.Symbol, b.Asset, b.Amount, b.Model, b.Rate, b.FirstInterest, nullUUID(b.OrderID),
		b.IdemKey, b.RequestHash, b.Status, b.Failure, b.CreatedAt, nullTime(b.DoneAt))
	if err != nil {
		return fmt.Errorf("insert borrow: %w", err)
	}
	return nil
}

func (r borrows) ByKey(ctx context.Context, userID, idemKey string) (ports.Borrow, bool, error) {
	b, err := scanBorrow(r.q.QueryRow(ctx, `SELECT `+borrowColumns+` FROM borrows WHERE user_id = $1 AND idem_key = $2`, userID, idemKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Borrow{}, false, nil
	}
	if err != nil {
		return ports.Borrow{}, false, fmt.Errorf("load borrow: %w", err)
	}
	return b, true, nil
}

func (r borrows) GetForUpdate(ctx context.Context, id string) (ports.Borrow, error) {
	b, err := scanBorrow(r.q.QueryRow(ctx, `SELECT `+borrowColumns+` FROM borrows WHERE borrow_id = $1 FOR UPDATE`, id))
	if err != nil {
		return ports.Borrow{}, fmt.Errorf("load borrow %s: %w", id, err)
	}
	return b, nil
}

func (r borrows) Finish(ctx context.Context, b ports.Borrow) error {
	tag, err := r.q.Exec(ctx, `UPDATE borrows SET status = $2, failure = $3, done_at = $4 WHERE borrow_id = $1 AND status = 'PENDING'`,
		b.ID, b.Status, b.Failure, nullTime(b.DoneAt))
	if err != nil {
		return fmt.Errorf("finish borrow: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("finish borrow %s: not pending", b.ID)
	}
	return nil
}

func (r borrows) Pending(ctx context.Context, cutoff time.Time, limit int) ([]ports.Borrow, error) {
	rows, err := r.q.Query(ctx, `SELECT `+borrowColumns+` FROM borrows WHERE status = 'PENDING' AND created_at < $1
		ORDER BY created_at LIMIT $2`, cutoff, limit)
	return collect(rows, err, "pending borrows", scanBorrow)
}

func (r borrows) PendingOf(ctx context.Context, userID string) ([]ports.Borrow, error) {
	rows, err := r.q.Query(ctx, `SELECT `+borrowColumns+` FROM borrows WHERE status = 'PENDING' AND user_id = $1
		ORDER BY created_at`, userID)
	return collect(rows, err, "pending borrows", scanBorrow)
}

// collect scans every row of a query (what names the rows in errors).
func collect[T any](rows pgx.Rows, err error, what string, scan func(pgx.Row) (T, error)) ([]T, error) {
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", what, err)
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", what, err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type repays repos

const repayColumns = `repay_id, user_id, account_type, symbol, asset, interest_repaid, principal_repaid, reason, order_id,
	liquidation_id, idem_key, request_hash, status, failure, created_at, done_at`

func scanRepay(row pgx.Row) (ports.Repay, error) {
	var p ports.Repay
	var accountType, symbol string
	var order, liquidation *string
	var done *time.Time
	if err := row.Scan(&p.ID, &p.UserID, &accountType, &symbol, &p.Asset, &p.Interest, &p.Principal, &p.Reason, &order,
		&liquidation, &p.IdemKey, &p.RequestHash, &p.Status, &p.Failure, &p.CreatedAt, &done); err != nil {
		return ports.Repay{}, err
	}
	p.Account, p.OrderID, p.LiquidationID, p.DoneAt = account(accountType, symbol), stringOf(order), stringOf(liquidation), timeOf(done)
	return p, nil
}

func (r repays) Insert(ctx context.Context, p ports.Repay) error {
	_, err := r.q.Exec(ctx, `INSERT INTO repays (`+repayColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		p.ID, p.UserID, p.Account.Type, p.Account.Symbol, p.Asset, p.Interest, p.Principal, p.Reason, nullUUID(p.OrderID),
		nullUUID(p.LiquidationID), p.IdemKey, p.RequestHash, p.Status, p.Failure, p.CreatedAt, nullTime(p.DoneAt))
	if err != nil {
		return fmt.Errorf("insert repayment: %w", err)
	}
	return nil
}

func (r repays) ByKey(ctx context.Context, userID, idemKey string) (ports.Repay, bool, error) {
	p, err := scanRepay(r.q.QueryRow(ctx, `SELECT `+repayColumns+` FROM repays WHERE user_id = $1 AND idem_key = $2`, userID, idemKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Repay{}, false, nil
	}
	if err != nil {
		return ports.Repay{}, false, fmt.Errorf("load repayment: %w", err)
	}
	return p, true, nil
}

func (r repays) GetForUpdate(ctx context.Context, id string) (ports.Repay, error) {
	p, err := scanRepay(r.q.QueryRow(ctx, `SELECT `+repayColumns+` FROM repays WHERE repay_id = $1 FOR UPDATE`, id))
	if err != nil {
		return ports.Repay{}, fmt.Errorf("load repayment %s: %w", id, err)
	}
	return p, nil
}

func (r repays) Finish(ctx context.Context, p ports.Repay) error {
	tag, err := r.q.Exec(ctx, `UPDATE repays SET status = $2, failure = $3, done_at = $4 WHERE repay_id = $1 AND status = 'PENDING'`,
		p.ID, p.Status, p.Failure, nullTime(p.DoneAt))
	if err != nil {
		return fmt.Errorf("finish repayment: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("finish repayment %s: not pending", p.ID)
	}
	return nil
}

func (r repays) Pending(ctx context.Context, cutoff time.Time, limit int) ([]ports.Repay, error) {
	rows, err := r.q.Query(ctx, `SELECT `+repayColumns+` FROM repays WHERE status = 'PENDING' AND created_at < $1
		ORDER BY created_at LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending repayments: %w", err)
	}
	defer rows.Close()
	var out []ports.Repay
	for rows.Next() {
		p, err := scanRepay(rows)
		if err != nil {
			return nil, fmt.Errorf("scan repayment: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type interest repos

const chargeColumns = `interest_id, user_id, account_type, symbol, asset, hour, principal, interest_model, hourly_rate, interest,
	borrow_id, status, created_at, done_at`

func scanCharge(row pgx.Row) (ports.Charge, error) {
	var c ports.Charge
	var accountType, symbol string
	var borrow *string
	var done *time.Time
	if err := row.Scan(&c.ID, &c.UserID, &accountType, &symbol, &c.Asset, &c.Hour, &c.Principal, &c.Model, &c.Rate, &c.Interest,
		&borrow, &c.Status, &c.CreatedAt, &done); err != nil {
		return ports.Charge{}, err
	}
	c.Account, c.BorrowID, c.DoneAt, c.Hour = account(accountType, symbol), stringOf(borrow), timeOf(done), c.Hour.UTC()
	return c, nil
}

func (r interest) Insert(ctx context.Context, c ports.Charge) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO interest_charges (`+chargeColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) ON CONFLICT DO NOTHING`,
		c.ID, c.UserID, c.Account.Type, c.Account.Symbol, c.Asset, c.Hour, c.Principal, c.Model, c.Rate, c.Interest,
		nullUUID(c.BorrowID), c.Status, c.CreatedAt, nullTime(c.DoneAt))
	if err != nil {
		return false, fmt.Errorf("insert interest charge: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r interest) Hourly(ctx context.Context, userID string, a domain.Account, asset string, hour time.Time) (ports.Charge, bool, error) {
	c, err := scanCharge(r.q.QueryRow(ctx, `SELECT `+chargeColumns+` FROM interest_charges WHERE user_id = $1 AND account_type = $2
		AND symbol = $3 AND asset = $4 AND hour = $5 AND borrow_id IS NULL`, userID, a.Type, a.Symbol, asset, hour))
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Charge{}, false, nil
	}
	if err != nil {
		return ports.Charge{}, false, fmt.Errorf("load interest charge: %w", err)
	}
	return c, true, nil
}

func (r interest) GetForUpdate(ctx context.Context, id string) (ports.Charge, error) {
	c, err := scanCharge(r.q.QueryRow(ctx, `SELECT `+chargeColumns+` FROM interest_charges WHERE interest_id = $1 FOR UPDATE`, id))
	if err != nil {
		return ports.Charge{}, fmt.Errorf("load interest charge %s: %w", id, err)
	}
	return c, nil
}

func (r interest) Finish(ctx context.Context, c ports.Charge) error {
	tag, err := r.q.Exec(ctx, `UPDATE interest_charges SET status = $2, done_at = $3 WHERE interest_id = $1 AND status = 'PENDING'`,
		c.ID, c.Status, nullTime(c.DoneAt))
	if err != nil {
		return fmt.Errorf("finish interest charge: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("finish interest charge %s: not pending", c.ID)
	}
	return nil
}

func (r interest) query(ctx context.Context, sql string, args ...any) ([]ports.Charge, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query interest charges: %w", err)
	}
	defer rows.Close()
	var out []ports.Charge
	for rows.Next() {
		c, err := scanCharge(rows)
		if err != nil {
			return nil, fmt.Errorf("scan interest charge: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r interest) Pending(ctx context.Context, cutoff time.Time, limit int) ([]ports.Charge, error) {
	return r.query(ctx, `SELECT `+chargeColumns+` FROM interest_charges WHERE status = 'PENDING' AND created_at < $1
		ORDER BY created_at LIMIT $2`, cutoff, limit)
}

func (r interest) PendingOf(ctx context.Context, userID string) ([]ports.Charge, error) {
	return r.query(ctx, `SELECT `+chargeColumns+` FROM interest_charges WHERE status = 'PENDING' AND user_id = $1
		ORDER BY created_at`, userID)
}

func (r interest) LockHour(ctx context.Context, asset string, hour time.Time) error {
	key := fmt.Sprintf("margin-interest:%s:%d", asset, hour.Unix())
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
		return fmt.Errorf("lock interest hour: %w", err)
	}
	return nil
}

func (r interest) OfHour(ctx context.Context, asset string, hour time.Time) ([]ports.Charge, error) {
	return r.query(ctx, `SELECT `+chargeColumns+` FROM interest_charges WHERE asset = $1 AND hour = $2 AND borrow_id IS NULL
		ORDER BY user_id, account_type, symbol`, asset, hour)
}

func (r interest) Since(ctx context.Context, at time.Time) (map[ports.LoanKey]ports.LoanSince, error) {
	rows, err := r.q.Query(ctx, `
		SELECT user_id, account_type, symbol, asset,
			coalesce(sum(amount) FILTER (WHERE status = 'DONE' AND created_at >= $1), 0),
			0::numeric,
			bool_or(status = 'PENDING' AND created_at < $1)
		FROM borrows WHERE created_at >= $1 OR status = 'PENDING' GROUP BY 1, 2, 3, 4
		UNION ALL
		SELECT user_id, account_type, symbol, asset,
			0::numeric,
			coalesce(sum(principal_repaid) FILTER (WHERE status = 'DONE' AND created_at >= $1), 0),
			bool_or(status = 'PENDING' AND created_at < $1)
		FROM repays WHERE created_at >= $1 OR status = 'PENDING' GROUP BY 1, 2, 3, 4`, at)
	if err != nil {
		return nil, fmt.Errorf("query loan changes: %w", err)
	}
	defer rows.Close()
	out := map[ports.LoanKey]ports.LoanSince{}
	for rows.Next() {
		var k ports.LoanKey
		var accountType, symbol string
		var s ports.LoanSince
		if err := rows.Scan(&k.UserID, &accountType, &symbol, &k.Asset, &s.Borrowed, &s.Repaid, &s.Unsettled); err != nil {
			return nil, fmt.Errorf("scan loan changes: %w", err)
		}
		k.Account = account(accountType, symbol)
		cur := out[k]
		out[k] = ports.LoanSince{
			Borrowed: cur.Borrowed.Add(s.Borrowed), Repaid: cur.Repaid.Add(s.Repaid), Unsettled: cur.Unsettled || s.Unsettled,
		}
	}
	return out, rows.Err()
}

func (r interest) OfUser(ctx context.Context, userID string, a *domain.Account, asset, before string, limit int) ([]ports.Charge, error) {
	var accountType, symbol *string
	if a != nil {
		t := string(a.Type)
		accountType, symbol = &t, &a.Symbol
	}
	var cursor any // NULL: the first page
	if before != "" {
		if _, err := uuid.Parse(before); err != nil {
			return nil, nil
		}
		cursor = before
	}
	return r.query(ctx, `SELECT `+chargeColumns+` FROM interest_charges WHERE user_id = $1 AND status = 'DONE'
		AND ($2::text IS NULL OR (account_type = $2 AND symbol = $3)) AND ($4 = '' OR asset = $4)
		AND ($5::uuid IS NULL OR interest_id < $5::uuid) ORDER BY interest_id DESC LIMIT $6`,
		userID, accountType, symbol, asset, cursor, limit)
}

func (r interest) Run(ctx context.Context, hour time.Time) (string, bool, error) {
	var status string
	err := r.q.QueryRow(ctx, `SELECT status FROM interest_runs WHERE hour = $1`, hour).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load interest run: %w", err)
	}
	return status, true, nil
}

func (r interest) StartRun(ctx context.Context, hour time.Time) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO interest_runs (hour, status) VALUES ($1, 'RUNNING') ON CONFLICT DO NOTHING`, hour); err != nil {
		return fmt.Errorf("start interest run: %w", err)
	}
	return nil
}

func (r interest) FinishRun(ctx context.Context, hour time.Time, loans int) error {
	if _, err := r.q.Exec(ctx, `UPDATE interest_runs SET status = 'DONE', loans = $2, done_at = now() WHERE hour = $1`, hour, loans); err != nil {
		return fmt.Errorf("finish interest run: %w", err)
	}
	return nil
}

func (r interest) LastDone(ctx context.Context) (time.Time, bool, error) {
	return r.hourOf(ctx, `SELECT max(hour) FROM interest_runs WHERE status = 'DONE'`)
}

func (r interest) FirstRunning(ctx context.Context) (time.Time, bool, error) {
	return r.hourOf(ctx, `SELECT min(hour) FROM interest_runs WHERE status = 'RUNNING'`)
}

func (r interest) hourOf(ctx context.Context, sql string) (time.Time, bool, error) {
	var h *time.Time
	if err := r.q.QueryRow(ctx, sql).Scan(&h); err != nil {
		return time.Time{}, false, fmt.Errorf("load interest runs: %w", err)
	}
	if h == nil {
		return time.Time{}, false, nil
	}
	return h.UTC(), true, nil
}

type transfers repos

const transferColumns = `transfer_id, user_id, direction, account_type, symbol, asset, amount, idem_key, request_hash, status,
	failure, created_at, done_at`

func scanTransfer(row pgx.Row) (ports.Transfer, error) {
	var t ports.Transfer
	var accountType, symbol string
	var done *time.Time
	if err := row.Scan(&t.ID, &t.UserID, &t.Direction, &accountType, &symbol, &t.Asset, &t.Amount, &t.IdemKey, &t.RequestHash,
		&t.Status, &t.Failure, &t.CreatedAt, &done); err != nil {
		return ports.Transfer{}, err
	}
	t.Account, t.DoneAt = account(accountType, symbol), timeOf(done)
	return t, nil
}

func (r transfers) Insert(ctx context.Context, t ports.Transfer) error {
	_, err := r.q.Exec(ctx, `INSERT INTO transfers (`+transferColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		t.ID, t.UserID, t.Direction, t.Account.Type, t.Account.Symbol, t.Asset, t.Amount, t.IdemKey, t.RequestHash, t.Status,
		t.Failure, t.CreatedAt, nullTime(t.DoneAt))
	if err != nil {
		return fmt.Errorf("insert transfer: %w", err)
	}
	return nil
}

func (r transfers) ByKey(ctx context.Context, userID, idemKey string) (ports.Transfer, bool, error) {
	t, err := scanTransfer(r.q.QueryRow(ctx, `SELECT `+transferColumns+` FROM transfers WHERE user_id = $1 AND idem_key = $2`,
		userID, idemKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Transfer{}, false, nil
	}
	if err != nil {
		return ports.Transfer{}, false, fmt.Errorf("load transfer: %w", err)
	}
	return t, true, nil
}

func (r transfers) GetForUpdate(ctx context.Context, id string) (ports.Transfer, error) {
	t, err := scanTransfer(r.q.QueryRow(ctx, `SELECT `+transferColumns+` FROM transfers WHERE transfer_id = $1 FOR UPDATE`, id))
	if err != nil {
		return ports.Transfer{}, fmt.Errorf("load transfer %s: %w", id, err)
	}
	return t, nil
}

func (r transfers) Finish(ctx context.Context, t ports.Transfer) error {
	tag, err := r.q.Exec(ctx, `UPDATE transfers SET status = $2, failure = $3, done_at = $4 WHERE transfer_id = $1 AND status = 'PENDING'`,
		t.ID, t.Status, t.Failure, nullTime(t.DoneAt))
	if err != nil {
		return fmt.Errorf("finish transfer: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("finish transfer %s: not pending", t.ID)
	}
	return nil
}

func (r transfers) Pending(ctx context.Context, cutoff time.Time, limit int) ([]ports.Transfer, error) {
	rows, err := r.q.Query(ctx, `SELECT `+transferColumns+` FROM transfers WHERE status = 'PENDING' AND created_at < $1
		ORDER BY created_at LIMIT $2`, cutoff, limit)
	return collect(rows, err, "pending transfers", scanTransfer)
}

func (r transfers) PendingOf(ctx context.Context, userID string) ([]ports.Transfer, error) {
	rows, err := r.q.Query(ctx, `SELECT `+transferColumns+` FROM transfers WHERE status = 'PENDING' AND user_id = $1
		ORDER BY created_at`, userID)
	return collect(rows, err, "pending transfers", scanTransfer)
}
