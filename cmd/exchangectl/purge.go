package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	ledgerapp "github.com/skill/exchange/internal/ledger/application"
	ledgerdomain "github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/user/application"
	"github.com/skill/exchange/internal/user/domain"
)

// The test-account purge (L4, the user-kind design 2026-10-09 §1 #9; the
// coordinator's contract of 2026-10-10 04:25): a test account is not
// deleted - the ledger only appends and its entries name the account - but
// settled, closed and hidden. Its spot orders are canceled, what its
// margin accounts hold without a debt goes back to spot, its spot and
// futures balances go to the ADJUSTMENT account (MANUAL_ADJUSTMENT, the
// key purge:<user>:<account>:<asset>), it is closed whatever its status
// (auth ends its sessions) and marked purged.
//
// Contract positions and orders go first through derivatives-service's
// flatten, a margin debt through margin-service's settle (L4b, called only
// for an account that has some: each call is audited); one they leave
// open, a liquidation under way (409), a withdrawal in flight and an
// account exempt from the purge (the end-to-end scripts' standing
// accounts) are skipped and listed. A dry run calls neither: it counts
// such accounts apart.

// purgeReasonCode is the status changes' reason code.
const purgeReasonCode = "TEST_ACCOUNT_PURGE"

// The reasons an account is left as it is.
const (
	skipExempt      = "exempt from the purge"
	skipNotFlat     = "contract positions or orders left open"
	skipDebt        = "a margin debt left"
	skipLiquidating = "a liquidation under way"
	skipWithdrawal  = "a withdrawal in flight"
	skipOrders      = "spot orders not canceled in time"
	skipFrozen      = "a frozen balance left"
	skipBalance     = "a balance left"
	skipFailed      = "failed"
)

type purgeOptions struct {
	users     []string
	emailLike string
	olderThan time.Duration
	limit     int
	dryRun    bool
	reason    string
	// wait bounds how long a user's canceled orders may take to end; pace
	// is the pause between users, so that a thousand closures do not reach
	// auth and notification-service at once.
	wait, pace time.Duration
}

type purgeCandidate struct {
	id     string
	status string
	exempt bool
}

// purgeRow is one of a user's ledger rows that holds something; version
// counts the lines applied to it, so a key made with it names the balance
// as read (B184).
type purgeRow struct {
	account, scope, asset string
	available, frozen     decimal.Decimal
	version               int64
}

// sweepKey is the ledger adjustment's idempotency key of a row as read: a
// run again over the same balance replays it, a balance changed since
// (credited after a sweep) gets a key of its own (B184 ②). The journal
// stores it behind the ledger's adjust: prefix.
func sweepKey(user string, r purgeRow) string {
	return fmt.Sprintf("purge:%s:%s:%s:v%d", user, r.account, r.asset, r.version)
}

// marginKey is the same for a margin account's transfer out, within
// margin-service's 100 bytes: the account and pair as a short hash.
func marginKey(user string, r purgeRow) string {
	h := sha256.Sum256([]byte(r.account + "/" + r.scope))
	return fmt.Sprintf("purge:%s:%s:%x:v%d", user, r.asset, h[:4], r.version)
}

func (r purgeRow) margin() bool { return ledgerdomain.MarginType(r.account) }

// debt reports a margin account's debt or interest row.
func (r purgeRow) debt() bool {
	return (ledgerdomain.AccountKey{Type: r.account}).Debt()
}

// purgeState is what a user still has, read from the services' schemas.
type purgeState struct {
	positions, contractOrders, conditionals, spotOrders, withdrawals int
	rows                                                             []purgeRow
}

func (s purgeState) has(f func(purgeRow) bool) bool { return slices.ContainsFunc(s.rows, f) }

// contracts reports contract positions, orders or conditional orders,
// which flatten ends.
func (s purgeState) contracts() bool {
	return s.positions > 0 || s.contractOrders > 0 || s.conditionals > 0
}

// purgeEnd is what flatten or settle answered: done, or what is left.
type purgeEnd struct {
	complete bool
	// left describes what is still open; debts are a settle's remaining
	// debts.
	left  []string
	debts []purgeDebt
}

// purgeDebt is a margin debt settle could not repay from the account.
type purgeDebt struct {
	account, symbol, asset string
	amount                 decimal.Decimal
}

func (d purgeDebt) String() string {
	parts := []string{d.account}
	if d.symbol != "" {
		parts = append(parts, d.symbol)
	}
	return strings.Join(append(parts, d.asset, d.amount.String()), " ")
}

// errLiquidating is a 409 of flatten or settle: a liquidation is under
// way on the account (DERIV_POSITION_LIQUIDATING, MARGIN_FROZEN).
var errLiquidating = errors.New("a liquidation is under way")

// purgeData reads the test accounts and what they hold.
type purgeData interface {
	counts(ctx context.Context) (string, error)
	candidates(ctx context.Context, opts purgeOptions) ([]purgeCandidate, error)
	state(ctx context.Context, user string) (purgeState, error)
}

// purgeActions settle, close and mark a test account; the moves take
// their idempotency keys.
type purgeActions interface {
	flatten(ctx context.Context, user, actor, reason string) (purgeEnd, error)
	settleMargin(ctx context.Context, user, actor, reason string) (purgeEnd, error)
	marginIn(ctx context.Context, user string, d purgeDebt, key string) error
	cancelOrders(ctx context.Context, user string) error
	marginOut(ctx context.Context, user string, r purgeRow, key string) error
	sweep(ctx context.Context, user string, r purgeRow, key, actor, reason string) error
	setStatus(ctx context.Context, user, to, actor, note string) error
	markPurged(ctx context.Context, user, actor, reason string) error
}

// purgeOutcome is what became of one user.
type purgeOutcome struct {
	user   string
	purged bool
	skip   string
	detail string
	// ends are the services' own ends it needs first (a dry run's flatten,
	// settle).
	ends []string
	// swept is what went to ADJUSTMENT (would go, on a dry run), by
	// account and asset ("SPOT USDT"): a margin account's balance counts
	// in spot, where it goes first.
	swept map[string]decimal.Decimal
}

type purger struct {
	data  purgeData
	act   purgeActions
	actor string
	opts  purgeOptions
	sleep func(context.Context, time.Duration) error
}

func usersPurge(ctx context.Context, cfg settings, db *pg.DB, svc *application.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("users purge", flag.ContinueOnError)
	fs.SetOutput(out)
	kind := fs.String("kind", domain.KindTest, "the accounts' kind: TEST, the only one purged")
	ids := fs.String("user", "", "only these accounts (IDs, comma-separated)")
	like := fs.String("email-like", "", "only the accounts whose email address matches this LIKE pattern")
	older := fs.Duration("older-than", 0, "only the accounts signed up at least this long ago (e.g. 24h)")
	limit := fs.Int("limit", 0, "at most this many accounts (0: all)")
	dry := fs.Bool("dry-run", false, "only report what would be done")
	reason := fs.String("reason", "", "why (required, goes to the audit log)")
	wait := fs.Duration("wait", 20*time.Second, "how long a user's canceled orders may take to end")
	pace := fs.Duration("pace", 50*time.Millisecond, "pause between users")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *kind != domain.KindTest {
		return fmt.Errorf("only TEST accounts are purged, not %q", *kind)
	}
	if len(strings.TrimSpace(*reason)) < 3 {
		fs.Usage()
		return errors.New("--reason of at least 3 characters is required")
	}
	opts := purgeOptions{emailLike: *like, olderThan: *older, limit: *limit, dryRun: *dry, reason: *reason, wait: *wait, pace: *pace}
	for id := range strings.SplitSeq(*ids, ",") {
		if id = strings.TrimSpace(id); id != "" {
			opts.users = append(opts.users, id)
		}
	}
	dbs, closeAll, err := openLedgerDBs(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeAll()
	ledger, _, err := newLedgerService(ctx, dbs)
	if err != nil {
		return err
	}
	if !opts.dryRun && !ledger.Flags.Enabled(flags.KeyManualAdjustment, flags.Subject{}) {
		return fmt.Errorf("manual adjustments are off; turn on %s with exchangectl flags set", flags.KeyManualAdjustment)
	}
	p := &purger{
		data: sqlPurgeData{db: db},
		act: livePurgeActions{
			// flatten may take some 40 seconds (L4b): a minute and more.
			users: svc, ledger: ledger, client: &http.Client{Timeout: 90 * time.Second},
			trading: strings.TrimRight(cfg.TradingURL, "/"), margin: strings.TrimRight(cfg.MarginURL, "/"),
			derivatives: strings.TrimRight(cfg.DerivativesURL, "/"),
		},
		actor: actor(), opts: opts, sleep: sleepCtx,
	}
	return p.run(ctx, out)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// run purges the candidates one by one and reports the counts before and
// after, what was recovered and who was left as they are, and why. It
// fails when an account failed.
func (p *purger) run(ctx context.Context, out io.Writer) error {
	before, err := p.data.counts(ctx)
	if err != nil {
		return err
	}
	list, err := p.data.candidates(ctx, p.opts)
	if err != nil {
		return err
	}
	verb := "purging"
	if p.opts.dryRun {
		verb = "dry run over"
	}
	fmt.Fprintf(out, "%s %d test accounts (by %s); before: %s\n", verb, len(list), p.actor, before)
	var outcomes []purgeOutcome
	for i, c := range list {
		if i > 0 && !p.opts.dryRun && p.opts.pace > 0 {
			if err := p.sleep(ctx, p.opts.pace); err != nil {
				return err
			}
		}
		o := p.one(ctx, c)
		if o.skip != "" {
			fmt.Fprintf(out, "skip %s: %s%s\n", o.user, o.skip, prefixed(": ", o.detail))
		}
		outcomes = append(outcomes, o)
	}
	after, err := p.data.counts(ctx)
	if err != nil {
		return err
	}
	purged, skipped, recovered := summarize(outcomes)
	word, label := "purged", "recovered to ADJUSTMENT"
	if p.opts.dryRun {
		word, label = "would purge", "would recover to ADJUSTMENT"
	}
	fmt.Fprintf(out, "%s %d, skipped %d", word, purged, len(outcomes)-purged)
	for _, s := range skipped {
		fmt.Fprintf(out, "; %s %d", s.reason, s.n)
	}
	fmt.Fprintln(out)
	if flat, settle := countEnds(outcomes, "flatten"), countEnds(outcomes, "settle"); flat+settle > 0 {
		fmt.Fprintf(out, "first: derivatives flatten %d, margin settle %d (what they leave is not known on a dry run; their balances as they are now)\n", flat, settle)
	}
	parts := make([]string, 0, len(recovered))
	for _, r := range recovered {
		parts = append(parts, r.key+" "+r.amount.String())
	}
	if len(parts) == 0 {
		parts = append(parts, "nothing")
	}
	fmt.Fprintf(out, "%s: %s\n", label, strings.Join(parts, ", "))
	fmt.Fprintf(out, "after: %s\n", after)
	if n := skippedFor(skipped, skipFailed); n > 0 {
		return fmt.Errorf("%d accounts failed (see above)", n)
	}
	return nil
}

func countEnds(outcomes []purgeOutcome, end string) int {
	n := 0
	for _, o := range outcomes {
		if slices.Contains(o.ends, end) {
			n++
		}
	}
	return n
}

func prefixed(prefix, s string) string {
	if s == "" {
		return ""
	}
	return prefix + s
}

type skipCount struct {
	reason string
	n      int
}

func skippedFor(skipped []skipCount, reason string) int {
	for _, s := range skipped {
		if s.reason == reason {
			return s.n
		}
	}
	return 0
}

type recoveredAmount struct {
	key    string
	amount decimal.Decimal
}

// summarize counts the purged accounts and the skipped ones by reason, and
// adds up what was recovered by account and asset.
func summarize(outcomes []purgeOutcome) (int, []skipCount, []recoveredAmount) {
	purged, skips, sums := 0, map[string]int{}, map[string]decimal.Decimal{}
	for _, o := range outcomes {
		if o.purged {
			purged++
		}
		if o.skip != "" {
			skips[o.skip]++
		}
		for k, v := range o.swept {
			sums[k] = sums[k].Add(v)
		}
	}
	skipped := make([]skipCount, 0, len(skips))
	for r, n := range skips {
		skipped = append(skipped, skipCount{r, n})
	}
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].reason < skipped[j].reason })
	recovered := make([]recoveredAmount, 0, len(sums))
	for k, v := range sums {
		recovered = append(recovered, recoveredAmount{k, v})
	}
	sort.Slice(recovered, func(i, j int) bool { return recovered[i].key < recovered[j].key })
	return purged, skipped, recovered
}

// sweptKey names what a row's balance counts under in the report: its
// account (spot for a margin account's, which goes through spot) and asset.
func sweptKey(r purgeRow) string {
	if r.margin() {
		return ledgerdomain.AccountSpot + " " + r.asset
	}
	return r.account + " " + r.asset
}

func (p *purger) one(ctx context.Context, c purgeCandidate) purgeOutcome {
	o := purgeOutcome{user: c.id, swept: map[string]decimal.Decimal{}}
	if c.exempt {
		o.skip = skipExempt
		return o
	}
	s, err := p.data.state(ctx, c.id)
	if err != nil {
		o.skip, o.detail = skipFailed, err.Error()
		return o
	}
	if s.withdrawals > 0 {
		o.skip, o.detail = skipWithdrawal, fmt.Sprintf("%d", s.withdrawals)
		return o
	}
	if p.opts.dryRun {
		// flatten and settle are not called: what they would leave is not
		// known, so these accounts are counted apart.
		if s.contracts() {
			o.ends = append(o.ends, "flatten")
		}
		if s.has(purgeRow.debt) {
			o.ends = append(o.ends, "settle")
		}
		// What orders hold comes back once they are canceled.
		for _, r := range s.rows {
			if all := r.available.Add(r.frozen); all.IsPositive() {
				o.swept[sweptKey(r)] = o.swept[sweptKey(r)].Add(all)
			}
		}
		o.purged = true
		return o
	}
	if err := p.clear(ctx, c, &o, s); err != nil {
		o.skip, o.detail = skipFailed, err.Error()
	}
	return o
}

// clear ends the user's contract and margin trading, cancels, moves and
// sweeps, then closes and marks the user; o gets a skip reason when
// something is left.
func (p *purger) clear(ctx context.Context, c purgeCandidate, o *purgeOutcome, s purgeState) error {
	if s.contracts() {
		end, err := p.act.flatten(ctx, c.id, p.actor, p.opts.reason)
		if done, err := p.ended(o, end, err, skipNotFlat); !done || err != nil {
			return err
		}
	}
	if s.has(purgeRow.debt) {
		if done, err := p.settleDebts(ctx, c.id, o); !done || err != nil {
			return err
		}
	}
	if s.contracts() || s.has(purgeRow.debt) {
		var err error
		if s, err = p.data.state(ctx, c.id); err != nil {
			return err
		}
	}
	if s.spotOrders > 0 {
		if err := p.act.cancelOrders(ctx, c.id); err != nil {
			return fmt.Errorf("cancel the spot orders: %w", err)
		}
		ended, done, err := p.ordersEnded(ctx, c.id)
		if err != nil {
			return err
		}
		if !done {
			o.skip = skipOrders
			return nil
		}
		// The balances as they are now, the freezes released (B184 ①).
		s = ended
	}
	// What a margin account holds without a debt goes back to spot, as the
	// user would move it; it is swept from there with the rest.
	for _, r := range s.rows {
		if r.margin() && r.available.IsPositive() {
			if err := p.act.marginOut(ctx, c.id, r, marginKey(c.id, r)); err != nil {
				return fmt.Errorf("move %s %s out of %s: %w", r.available, r.asset, r.account, err)
			}
		}
	}
	s, err := p.data.state(ctx, c.id)
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(s.rows, func(r purgeRow) bool { return r.frozen.IsPositive() }); i >= 0 {
		o.skip, o.detail = skipFrozen, fmt.Sprintf("%s %s %s", s.rows[i].account, s.rows[i].asset, s.rows[i].frozen)
		return nil
	}
	for _, r := range s.rows {
		if r.margin() || !r.available.IsPositive() {
			continue
		}
		if err := p.act.sweep(ctx, c.id, r, sweepKey(c.id, r), p.actor, p.opts.reason); err != nil {
			return fmt.Errorf("sweep %s %s %s: %w", r.account, r.available, r.asset, err)
		}
		o.swept[sweptKey(r)] = o.swept[sweptKey(r)].Add(r.available)
	}
	if s, err = p.data.state(ctx, c.id); err != nil {
		return err
	}
	if len(s.rows) > 0 {
		r := s.rows[0]
		o.skip, o.detail = skipBalance, fmt.Sprintf("%s %s %s", r.account, r.asset, r.available.Add(r.frozen))
		return nil
	}
	if c.status != domain.StatusClosed {
		if err := p.act.setStatus(ctx, c.id, domain.StatusClosed, p.actor, p.opts.reason); err != nil {
			return fmt.Errorf("close it: %w", err)
		}
	}
	if err := p.act.markPurged(ctx, c.id, p.actor, p.opts.reason); err != nil {
		return fmt.Errorf("mark it purged: %w", err)
	}
	o.purged = true
	return nil
}

// ended reads what flatten or settle answered: on with the purge, or o
// skipped (a liquidation under way, something left) and not.
func (p *purger) ended(o *purgeOutcome, end purgeEnd, err error, left string) (bool, error) {
	switch {
	case errors.Is(err, errLiquidating):
		o.skip, o.detail = skipLiquidating, strings.TrimPrefix(err.Error(), errLiquidating.Error()+": ")
		return false, nil
	case err != nil:
		return false, err
	case !end.complete:
		o.skip, o.detail = left, strings.Join(end.left, "; ")
		return false, nil
	}
	return true, nil
}

// settleDebts has margin-service repay the user's debts from their margin
// accounts; a debt they cannot repay is met from spot (moved in as the
// user would) and settle called again, once (L4b's contract).
func (p *purger) settleDebts(ctx context.Context, user string, o *purgeOutcome) (bool, error) {
	end, err := p.act.settleMargin(ctx, user, p.actor, p.opts.reason)
	if err == nil && !end.complete && len(end.debts) > 0 {
		for _, d := range end.debts {
			// Spot as it is now: an earlier debt's move took from it
			// (B188 ①).
			s, readErr := p.data.state(ctx, user)
			if readErr != nil {
				return false, readErr
			}
			i := slices.IndexFunc(s.rows, func(r purgeRow) bool { return r.account == ledgerdomain.AccountSpot && r.asset == d.asset })
			if i < 0 || s.rows[i].available.LessThan(d.amount) {
				o.skip, o.detail = skipDebt, d.String()+", not in spot"
				return false, nil
			}
			if err := p.act.marginIn(ctx, user, d, marginInKey(user, d, s.rows[i].version)); err != nil {
				o.skip, o.detail = skipDebt, fmt.Sprintf("%s: %v", d, err)
				return false, nil
			}
		}
		end, err = p.act.settleMargin(ctx, user, p.actor, p.opts.reason)
	}
	return p.ended(o, end, err, skipDebt)
}

// marginInKey is a move into a margin account for one of its debts: the
// account and pair as a short hash, as marginKey, and the version of the
// spot row it comes from, so two debts in one asset get keys of their own
// (B188 ①).
func marginInKey(user string, d purgeDebt, spotVersion int64) string {
	h := sha256.Sum256([]byte(d.account + "/" + d.symbol))
	return fmt.Sprintf("purge:%s:%s:in:%x:v%d", user, d.asset, h[:4], spotVersion)
}

// ordersEnded waits up to the options' wait for a user's canceled orders
// to end and their freezes to be released, and returns what the user has
// then; false when they did not end.
func (p *purger) ordersEnded(ctx context.Context, user string) (purgeState, bool, error) {
	for waited := time.Duration(0); ; waited += 300 * time.Millisecond {
		s, err := p.data.state(ctx, user)
		if err != nil {
			return s, false, err
		}
		if s.spotOrders == 0 && !s.has(func(r purgeRow) bool { return r.frozen.IsPositive() }) {
			return s, true, nil
		}
		if waited >= p.opts.wait {
			return s, false, nil
		}
		if err := p.sleep(ctx, 300*time.Millisecond); err != nil {
			return s, false, err
		}
	}
}

// sqlPurgeData reads the users schema and the other services' tables by
// qualified names (exchangectl reaches every schema).
type sqlPurgeData struct{ db *pg.DB }

// counts are the test accounts not purged (exempt ones among them) and
// purged.
func (d sqlPurgeData) counts(ctx context.Context) (string, error) {
	var open, exempt, purged int
	err := d.db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE purged_at IS NULL), count(*) FILTER (WHERE purged_at IS NULL AND purge_exempt),
		count(*) FILTER (WHERE purged_at IS NOT NULL) FROM users WHERE kind = 'TEST'`).Scan(&open, &exempt, &purged)
	if err != nil {
		return "", fmt.Errorf("count the test accounts: %w", err)
	}
	return fmt.Sprintf("TEST %d not purged (%d exempt), %d purged", open, exempt, purged), nil
}

// candidates are the test accounts not purged yet that the options name,
// oldest first.
func (d sqlPurgeData) candidates(ctx context.Context, opts purgeOptions) ([]purgeCandidate, error) {
	ids := opts.users
	if ids == nil {
		ids = []string{}
	}
	q := `SELECT u.id::text, u.status, u.purge_exempt FROM users u
		WHERE u.kind = 'TEST' AND u.purged_at IS NULL
		AND (cardinality($1::uuid[]) = 0 OR u.id = ANY($1::uuid[]))
		AND ($2 = '' OR u.id IN (SELECT user_id FROM auth.identities WHERE kind = 'EMAIL' AND value LIKE $2))
		AND u.created_at <= $3`
	if opts.limit > 0 {
		// The limit counts the accounts to purge: the exempt ones are in
		// the counts before and after.
		q += fmt.Sprintf(" AND NOT u.purge_exempt ORDER BY u.created_at, u.id LIMIT %d", opts.limit)
	} else {
		q += " ORDER BY u.created_at, u.id"
	}
	rows, err := d.db.Query(ctx, q, ids, strings.ToLower(opts.emailLike), time.Now().Add(-opts.olderThan))
	if err != nil {
		return nil, fmt.Errorf("the test accounts: %w", err)
	}
	defer rows.Close()
	var out []purgeCandidate
	for rows.Next() {
		var c purgeCandidate
		if err := rows.Scan(&c.id, &c.status, &c.exempt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// state reads what a user still has: contract positions, orders and
// conditional orders, spot orders, withdrawals in flight and every ledger
// row holding something.
func (d sqlPurgeData) state(ctx context.Context, user string) (purgeState, error) {
	var s purgeState
	err := d.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM derivatives.positions WHERE user_id = $1 AND quantity <> 0),
		(SELECT count(*) FROM derivatives.orders WHERE user_id = $1 AND status IN ('NEW', 'OPEN', 'PARTIALLY_FILLED')),
		(SELECT count(*) FROM derivatives.conditional_orders WHERE user_id = $1 AND status = 'ACTIVE'),
		(SELECT count(*) FROM trading.orders WHERE user_id = $1 AND status IN ('NEW', 'OPEN', 'PARTIALLY_FILLED')),
		(SELECT count(*) FROM wallet.withdrawals WHERE user_id = $1
			AND status NOT IN ('CONFIRMED', 'INTERNAL_TRANSFER', 'REJECTED', 'CANCELED', 'FAILED'))`, user).
		Scan(&s.positions, &s.contractOrders, &s.conditionals, &s.spotOrders, &s.withdrawals)
	if err != nil {
		return s, fmt.Errorf("what %s has: %w", user, err)
	}
	rows, err := d.db.Query(ctx, `SELECT account_type, scope, asset, available, frozen, version FROM ledger.accounts
		WHERE owner_type = 'USER' AND owner_id = $1 AND (available <> 0 OR frozen <> 0) ORDER BY account_type, scope, asset`, user)
	if err != nil {
		return s, fmt.Errorf("%s's balances: %w", user, err)
	}
	defer rows.Close()
	for rows.Next() {
		var r purgeRow
		if err := rows.Scan(&r.account, &r.scope, &r.asset, &r.available, &r.frozen, &r.version); err != nil {
			return s, err
		}
		s.rows = append(s.rows, r)
	}
	return s, rows.Err()
}

// livePurgeActions act through the services: spot-trading-service and
// margin-service as the user would through the gateway, the ledger's and
// user-service's own use cases.
type livePurgeActions struct {
	users                        *application.Service
	ledger                       *ledgerapp.Service
	client                       *http.Client
	trading, margin, derivatives string
}

func (a livePurgeActions) cancelOrders(ctx context.Context, user string) error {
	return a.call(ctx, http.MethodDelete, a.trading+"/v1/orders", user, "", nil, nil)
}

// flatten has derivatives-service end the user's contract accounts (L4b):
// it may take some 40 seconds when the engine does not answer.
func (a livePurgeActions) flatten(ctx context.Context, user, actor, reason string) (purgeEnd, error) {
	var answer struct {
		Remaining []struct {
			Symbol   string `json:"symbol"`
			Side     string `json:"side"`
			Quantity string `json:"quantity"`
			Reason   string `json:"reason"`
		} `json:"remaining"`
		Complete bool `json:"complete"`
	}
	err := a.internal(ctx, a.derivatives+"/internal/derivatives/users/"+user+"/flatten", actor, reason, &answer)
	end := purgeEnd{complete: answer.Complete}
	for _, r := range answer.Remaining {
		end.left = append(end.left, strings.Join([]string{r.Symbol, r.Side, r.Quantity, r.Reason}, " "))
	}
	return end, err
}

// settleMargin has margin-service repay the user's margin debts (L4b).
func (a livePurgeActions) settleMargin(ctx context.Context, user, actor, reason string) (purgeEnd, error) {
	var answer struct {
		RemainingDebt []struct {
			Account string          `json:"account"`
			Symbol  *string         `json:"symbol"`
			Asset   string          `json:"asset"`
			Amount  decimal.Decimal `json:"amount"`
		} `json:"remaining_debt"`
		Complete bool `json:"complete"`
	}
	err := a.internal(ctx, a.margin+"/internal/margin/users/"+user+"/settle", actor, reason, &answer)
	end := purgeEnd{complete: answer.Complete}
	for _, d := range answer.RemainingDebt {
		debt := purgeDebt{account: d.Account, asset: d.Asset, amount: d.Amount}
		if d.Symbol != nil {
			debt.symbol = *d.Symbol
		}
		end.debts = append(end.debts, debt)
		end.left = append(end.left, debt.String())
	}
	return end, err
}

func (a livePurgeActions) marginIn(ctx context.Context, user string, d purgeDebt, key string) error {
	body := map[string]string{"direction": "IN", "account": d.account, "asset": d.asset, "amount": d.amount.String()}
	if d.symbol != "" {
		body["symbol"] = d.symbol
	}
	return a.call(ctx, http.MethodPost, a.margin+"/v1/margin/transfer", user, key, body, nil)
}

// internal posts {actor, reason} to a service's internal purge endpoint
// and decodes the answer; a 409 is errLiquidating with the service's own
// words. Not tried again: since C79 a slow engine's answer is
// complete:false, and a second call would run beside the first (B188 ②).
func (a livePurgeActions) internal(ctx context.Context, url, actor, reason string, answer any) error {
	body := map[string]string{"actor": actor, "reason": reason}
	err := a.call(ctx, http.MethodPost, url, "", "", body, answer)
	var status *httpStatusError
	if errors.As(err, &status) && status.code == http.StatusConflict {
		return fmt.Errorf("%w: %s", errLiquidating, status.body)
	}
	return err
}

// httpStatusError is a service's answer of 300 or more.
type httpStatusError struct {
	method, url string
	code        int
	body        string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d %s", e.method, e.url, e.code, e.body)
}

func (a livePurgeActions) marginOut(ctx context.Context, user string, r purgeRow, key string) error {
	body := map[string]string{"direction": "OUT", "account": r.account, "asset": r.asset, "amount": r.available.String()}
	if r.scope != "" {
		body["symbol"] = r.scope
	}
	return a.call(ctx, http.MethodPost, a.margin+"/v1/margin/transfer", user, key, body, nil)
}

func (a livePurgeActions) sweep(ctx context.Context, user string, r purgeRow, key, actor, reason string) error {
	_, err := a.ledger.AdjustAccount(ctx, key, user, r.account, r.asset, r.available.Neg(), actor, reason)
	return err
}

func (a livePurgeActions) setStatus(ctx context.Context, user, to, actor, note string) error {
	_, err := a.users.ChangeStatus(ctx, user, to, purgeReasonCode, actor, note)
	return err
}

func (a livePurgeActions) markPurged(ctx context.Context, user, actor, reason string) error {
	_, err := a.users.MarkPurged(ctx, user, actor, reason)
	return err
}

// call makes a request to a service as the user, as the gateway would
// forward it (X-User-Id), or to an internal endpoint (no user), with an
// idempotency key when given; answer, when given, takes the JSON answer.
func (a livePurgeActions) call(ctx context.Context, method, url, user, key string, body, answer any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	if user != "" {
		req.Header.Set("X-User-Id", user)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return &httpStatusError{method: method, url: url, code: resp.StatusCode, body: strings.TrimSpace(string(raw[:min(len(raw), 2048)]))}
	}
	if answer != nil {
		if err := json.Unmarshal(raw, answer); err != nil {
			return fmt.Errorf("%s %s: the answer: %w", method, url, err)
		}
	}
	return nil
}
