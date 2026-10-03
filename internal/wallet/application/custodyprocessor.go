package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	walletv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

// CustodyProcessor carries out the custodian's side of the networks it
// serves (ADR-0011), on the one instance holding its lease: requests left
// REQUESTED by an outage, releases of refused funds, approved withdrawals
// handed over (or transferred inside the ledger), settlements of the ones
// it sent, its fees, the credit requests of its deposits, its balances,
// and the hourly check of what it holds against the ledger (invariant 4
// over every holder of the asset).
type CustodyProcessor struct {
	Store       ports.Store
	Ledger      ports.Ledger
	Networks    ports.Networks
	Eligibility ports.Eligibility
	Custody     ports.Custody
	Log         *slog.Logger
	Now         func() time.Time
	// Elsewhere reports what the platform's own wallets hold of an asset
	// now (nil: nothing).
	Elsewhere ports.Holdings
	// Contradictions counts what the custodian did against a finished
	// withdrawal (the service's counter, shared).
	Contradictions prometheus.Counter
	// CheckEvery paces the check (an hour), BalanceEvery the balances (5
	// minutes); a withdrawal the custodian did not acknowledge is handed
	// over again after ResubmitAfter (a minute), until UncertainAfter (30
	// minutes) after its first hand-over.
	CheckEvery     time.Duration
	BalanceEvery   time.Duration
	ResubmitAfter  time.Duration
	UncertainAfter time.Duration
	// An asset's withdrawals are suspended when more of it is missing than
	// its ShortfallStop (WALLET_SHORTFALL_STOP; an asset not named there
	// gets the smallest withdrawal fee of its networks with the custodian,
	// the custodian's fee granularity at least) beyond what explains it:
	// withdrawals with an unknown outcome, fees held for a person and a
	// difference a person accepted. It takes two checks at least
	// RecheckAfter (5 minutes) apart: a first sighting has the next check
	// come then (design §9, review B4 and of ebb8aaa).
	ShortfallStop map[string]decimal.Decimal
	RecheckAfter  time.Duration

	lastCheck   time.Time
	lastBalance time.Time
	recheckAt   time.Time

	up        prometheus.Gauge
	balance   *prometheus.GaugeVec
	held      *prometheus.GaugeVec
	expected  *prometheus.GaugeVec
	shortfall *prometheus.GaugeVec
	compared  *prometheus.GaugeVec
	submitted prometheus.Gauge
	oldest    prometheus.Gauge
	attention prometheus.Gauge
	uncertain prometheus.Gauge
	waiting   prometheus.Gauge
	unbooked  prometheus.Gauge
	feesHeld  prometheus.Gauge
	suspended *prometheus.GaugeVec
	stopped   *prometheus.GaugeVec
}

// checkRetry is when a check that failed is tried again.
const checkRetry = time.Minute

// NewCustodyProcessor registers the processor's metrics with reg.
func NewCustodyProcessor(p CustodyProcessor, reg prometheus.Registerer) *CustodyProcessor {
	labels := prometheus.Labels{"provider": p.Custody.Provider()}
	gauge := func(name, help string) prometheus.Gauge {
		return prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help, ConstLabels: labels})
	}
	vec := func(name, help, label string) *prometheus.GaugeVec {
		return prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help, ConstLabels: labels}, []string{label})
	}
	p.up = gauge("wallet_custody_up", "1 when the custodian answered the last balance request.")
	p.balance = vec("wallet_custody_balance", "What the custodian holds of a coin, as it reports it.", "coin")
	p.held = vec("wallet_custody_held", "What the custodian holds of an asset, at the last check.", "asset")
	p.expected = vec("wallet_custody_expected", "What the ledger expects every holder of the asset to hold: −(DEPOSIT_PENDING + WITHDRAWAL_PENDING).", "asset")
	p.shortfall = vec("wallet_custody_shortfall", "Expected minus what every holder holds, what is in flight and the unbooked fees; above 0 funds are missing (invariant 4).", "asset")
	p.compared = vec("wallet_custody_not_compared", "1 when the last check left the asset out (no balance reported, or one beyond belief): its shortfall is not known.", "asset")
	p.submitted = gauge("wallet_custody_submitted", "Withdrawals with the custodian, not reported sent or failed yet.")
	p.oldest = gauge("wallet_custody_submitted_oldest_seconds", "How long the oldest withdrawal has been with the custodian.")
	p.attention = gauge("wallet_custody_callbacks_attention", "Verified callbacks that failed or found nothing to apply to.")
	p.uncertain = gauge("wallet_custody_withdrawals_uncertain", "Withdrawals refused on a retry that the custodian may still send: they wait for its callback or a person.")
	p.waiting = gauge("wallet_custody_deposits_held", "Deposits the custodian confirmed that wait because their asset takes no deposits.")
	p.unbooked = gauge("wallet_custody_fees_unbooked", "The custodian's fees the ledger has not booked yet (GAS_SUPPLY short?).")
	p.feesHeld = gauge("wallet_custody_fees_held", "The custodian's fees held for a person to book or write off (exchangectl wallet custody-fees).")
	p.suspended = vec("wallet_withdrawals_suspended", "1 while an asset's withdrawals are suspended (funds missing, or an operator); a person lifts it.", "asset")
	p.stopped = vec("wallet_withdrawals_suspended_waiting", "Approved withdrawals that wait because their asset's withdrawals are suspended.", "asset")
	reg.MustRegister(p.up, p.balance, p.held, p.expected, p.shortfall, p.compared, p.submitted, p.oldest, p.attention, p.uncertain, p.waiting,
		p.unbooked, p.feesHeld, p.suspended, p.stopped)
	if p.CheckEvery <= 0 {
		p.CheckEvery = time.Hour
	}
	if p.BalanceEvery <= 0 {
		p.BalanceEvery = 5 * time.Minute
	}
	if p.ResubmitAfter <= 0 {
		p.ResubmitAfter = time.Minute
	}
	if p.UncertainAfter <= 0 {
		p.UncertainAfter = 30 * time.Minute
	}
	if p.RecheckAfter <= 0 {
		p.RecheckAfter = 5 * time.Minute
	}
	return &p
}

// networks lists the networks the custodian serves.
func (p *CustodyProcessor) networks(ctx context.Context) ([]domain.Network, error) {
	all, err := p.Networks.ForAsset(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []domain.Network
	for _, n := range all {
		if n.Provider == p.Custody.Provider() {
			out = append(out, n)
		}
	}
	return out, nil
}

func names(nets []domain.Network) []string {
	var out []string
	for _, n := range nets {
		if !slices.Contains(out, n.Network) {
			out = append(out, n.Network)
		}
	}
	return out
}

// Round runs every step once; a failing step does not stop the others.
func (p *CustodyProcessor) Round(ctx context.Context) error {
	nets, err := p.networks(ctx)
	if err != nil {
		return err
	}
	var errs []error
	held, unbooked := 0, decimal.Zero
	stopped := map[string]int{} // approved withdrawals of suspended assets, by asset
	for _, n := range names(nets) {
		o := netOps{Store: p.Store, Ledger: p.Ledger, Log: p.Log, Now: p.Now, Network: n}
		errs = append(errs, o.recoverRequested(ctx), o.release(ctx), p.dispatch(ctx, o, nets, stopped), o.settle(ctx))
		waiting, err := requestCredits(ctx, p.Store, p.Eligibility, p.Ledger, n, nets, p.Now)
		held += waiting
		left, ferr := o.bookFees(ctx)
		unbooked = unbooked.Add(left)
		errs = append(errs, err, ferr)
	}
	p.waiting.Set(float64(held))
	p.unbooked.Set(unbooked.InexactFloat64())
	p.stopped.Reset()
	for asset, n := range stopped {
		p.stopped.WithLabelValues(asset).Set(float64(n))
	}
	errs = append(errs, p.resubmit(ctx, nets), p.observe(ctx), p.commands(ctx))
	now := p.Now()
	if now.Sub(p.lastBalance) >= p.BalanceEvery {
		if _, err := p.coins(ctx); err != nil {
			errs = append(errs, fmt.Errorf("custodian balances: %w", err))
		} else {
			p.lastBalance = now
		}
	}
	recheck := !p.recheckAt.IsZero() && !now.Before(p.recheckAt)
	if now.Sub(p.lastCheck) >= p.CheckEvery || recheck {
		p.recheckAt = time.Time{}
		_, err := p.Check(ctx)
		switch {
		case err == nil || errors.Is(err, errNotCompared):
			p.lastCheck = now
		default: // tried again in checkRetry, not every round while the custodian is away
			p.lastCheck = now.Add(checkRetry - p.CheckEvery)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("custody check: %w", err))
			if recheck && p.recheckAt.IsZero() {
				p.recheckAt = now.Add(p.RecheckAfter) // still due: not an hour later
			}
		}
	}
	return errors.Join(errs...)
}

// commands runs the operators' checks queued for the custodian
// (exchangectl wallet reconcile --network UDUN).
func (p *CustodyProcessor) commands(ctx context.Context) error {
	list, err := p.Store.Read().Commands().Pending(ctx, p.Custody.Provider())
	if err != nil {
		return err
	}
	for _, c := range list {
		c.Status, c.DoneAt = domain.CommandDone, p.Now()
		if c.Kind != domain.CommandReconcile {
			c.Status, c.Result = domain.CommandFailed, "only RECONCILE runs for the custodian"
		} else if checks, err := p.Check(ctx); err != nil && !errors.Is(err, errNotCompared) {
			c.Status, c.Result = domain.CommandFailed, err.Error()
		} else {
			var parts []string
			for _, x := range checks {
				parts = append(parts, fmt.Sprintf("%s held %s, elsewhere %s, in flight %s, expected %s, shortfall %s", x.Asset, x.Chain,
					x.Elsewhere, x.InFlight, x.Ledger, x.Shortfall))
			}
			if err != nil {
				parts = append(parts, err.Error())
			}
			c.Result = strings.Join(parts, "; ")
			p.lastCheck = p.Now()
		}
		p.Log.InfoContext(ctx, "operator command", "command_id", c.ID, "kind", c.Kind, "status", c.Status, "result", c.Result,
			"requested_by", c.RequestedBy)
		if err := p.Store.Tx(ctx, func(r ports.Repos) error { return r.Commands().Update(ctx, c) }); err != nil {
			return err
		}
	}
	return nil
}

// dispatch hands the network's approved withdrawals to the custodian,
// oldest first; internal ones complete in the ledger.
func (p *CustodyProcessor) dispatch(ctx context.Context, o netOps, nets []domain.Network, waiting map[string]int) error {
	list, err := p.Store.Read().Withdrawals().ByStatus(ctx, o.Network, domain.WithdrawalApproved)
	if err != nil {
		return err
	}
	suspended, err := suspendedAssets(ctx, p.Store.Read())
	if err != nil {
		return err
	}
	for _, w := range list {
		if w.InternalUserID != "" {
			if err := o.transferInternal(ctx, w); err != nil {
				return err
			}
			continue
		}
		if suspended[w.Asset] {
			waiting[w.Asset]++
			continue // waits APPROVED until a person lifts the suspension
		}
		if err := p.submit(ctx, w, nets); err != nil {
			return err
		}
	}
	return nil
}

func networkOf(nets []domain.Network, asset, network string) (domain.Network, bool) {
	for _, n := range nets {
		if n.Asset == asset && n.Network == network {
			return n, true
		}
	}
	return domain.Network{}, false
}

// submit marks an approved withdrawal SUBMITTED, which closes the door to
// a cancellation, then hands it over.
func (p *CustodyProcessor) submit(ctx context.Context, w domain.Withdrawal, nets []domain.Network) error {
	net, ok := networkOf(nets, w.Asset, w.Network)
	if !ok {
		return fmt.Errorf("withdrawal %s: %s is not on the custodian's network %s", w.ID, w.Asset, w.Network)
	}
	moved := false
	err := p.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Withdrawals().GetForUpdate(ctx, w.ID)
		if err != nil || cur == nil || !cur.Submit(p.Now()) {
			return err
		}
		moved, w = true, *cur
		if err := r.Withdrawals().Update(ctx, *cur); err != nil {
			return err
		}
		return r.EmitWithdrawal(ctx, &walletv1.WithdrawalSubmitted{Withdrawal: WithdrawalProto(*cur)}, cur.UserID)
	})
	if err != nil || !moved {
		return err
	}
	return p.handOver(ctx, w, net, false)
}

// handOver gives a SUBMITTED withdrawal to the custodian. An unknown
// outcome leaves it for resubmit (the withdrawal ID makes a repeat
// harmless). A refusal of the first hand-over fails it and releases its
// funds; a refusal of a retry does neither: the custodian may hold the
// first and send it, so the withdrawal is marked uncertain and waits for
// the custodian's callback or a person (CustodyUncertain).
func (p *CustodyProcessor) handOver(ctx context.Context, w domain.Withdrawal, net domain.Network, retry bool) error {
	err := p.Custody.Submit(ctx, w, net)
	refused := apperr.Is(err, domain.ErrCustodyRefused.Code)
	if err != nil && !refused {
		p.Log.WarnContext(ctx, "handing a withdrawal to the custodian failed, it is tried again", "withdrawal_id", w.ID, "error", err)
		return nil
	}
	var failed *domain.Withdrawal
	err = p.Store.Tx(ctx, func(r ports.Repos) error {
		cur, rerr := r.Withdrawals().GetForUpdate(ctx, w.ID)
		if rerr != nil || cur == nil {
			return rerr
		}
		if refused && retry {
			if !cur.Uncertain(detail(err), p.Now()) {
				return nil
			}
			p.Log.ErrorContext(ctx, "the custodian refused a withdrawal handed over again, it may hold the first: left for its callback or a person",
				"withdrawal_id", cur.ID, "reason", cur.RejectReason)
			return r.Withdrawals().Update(ctx, *cur)
		}
		if !refused {
			if !cur.Custodian(domain.CustodyAccepted, "", p.Now()) {
				if cur.Status == domain.WithdrawalFailed {
					// Resolved as failed, its funds released, while this
					// handover was on its way: the custodian may send it.
					if p.Contradictions != nil {
						p.Contradictions.Inc()
					}
					p.Log.ErrorContext(ctx, "the custodian accepted a withdrawal already failed and released: it may send it; check its console",
						"withdrawal_id", cur.ID, "reason", cur.RejectReason)
				}
				return nil
			}
			p.Log.InfoContext(ctx, "withdrawal handed to the custodian", "withdrawal_id", cur.ID, "network", cur.Network)
			return r.Withdrawals().Update(ctx, *cur)
		}
		if !cur.Custodian(domain.CustodyRejected, "", p.Now()) {
			return nil
		}
		cur.RejectReason = "CUSTODY_REFUSED: " + detail(err)
		failed = cur
		if err := r.Withdrawals().Update(ctx, *cur); err != nil {
			return err
		}
		p.Log.ErrorContext(ctx, "the custodian refused a withdrawal", "withdrawal_id", cur.ID, "reason", cur.RejectReason)
		return r.EmitWithdrawal(ctx, &walletv1.WithdrawalFailed{Withdrawal: WithdrawalProto(*cur)}, cur.UserID)
	})
	if err != nil || failed == nil {
		return err
	}
	_, err = ReleaseWithdrawal(ctx, p.Store, p.Ledger, *failed)
	return err
}

// resubmit hands over again the withdrawals the custodian has not
// acknowledged after ResubmitAfter.
func (p *CustodyProcessor) resubmit(ctx context.Context, nets []domain.Network) error {
	list, err := p.Store.Read().Withdrawals().Submitted(ctx, p.Custody.Provider())
	if err != nil {
		return err
	}
	var errs []error
	for _, w := range list {
		if w.ProviderStatus != domain.CustodySubmitted || p.Now().Sub(w.SubmittedAt) < p.ResubmitAfter {
			continue
		}
		// No answer to any hand-over for UncertainAfter (the custodian
		// times out): one may have reached it, so the funds stay frozen and
		// the hand-overs stop; a person checks its console and resolves it
		// (custody-resolve), as after a refused retry.
		if p.Now().Sub(w.SubmittedAt) >= p.UncertainAfter {
			errs = append(errs, p.giveUp(ctx, w))
			continue
		}
		net, ok := networkOf(nets, w.Asset, w.Network)
		if !ok {
			errs = append(errs, fmt.Errorf("withdrawal %s: no network %s for %s", w.ID, w.Network, w.Asset))
			continue
		}
		errs = append(errs, p.handOver(ctx, w, net, true))
	}
	return errors.Join(errs...)
}

// giveUp marks a withdrawal the custodian never answered UNCERTAIN.
func (p *CustodyProcessor) giveUp(ctx context.Context, w domain.Withdrawal) error {
	return p.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Withdrawals().GetForUpdate(ctx, w.ID)
		if err != nil || cur == nil || cur.ProviderStatus != domain.CustodySubmitted {
			return err
		}
		if !cur.Uncertain(fmt.Sprintf("no answer from the custodian in %s of hand-overs", p.UncertainAfter), p.Now()) {
			return nil
		}
		p.Log.ErrorContext(ctx, "the custodian never answered a withdrawal's hand-overs: left for its callback or a person",
			"withdrawal_id", cur.ID, "since", cur.SubmittedAt)
		return r.Withdrawals().Update(ctx, *cur)
	})
}

// observe updates the gauges of the withdrawals with the custodian and of
// the callbacks that need a person.
func (p *CustodyProcessor) observe(ctx context.Context) error {
	r := p.Store.Read()
	list, err := r.Withdrawals().Submitted(ctx, p.Custody.Provider())
	if err != nil {
		return err
	}
	p.submitted.Set(float64(len(list)))
	uncertain := 0
	for _, w := range list {
		if w.ProviderStatus == domain.CustodyUncertain {
			uncertain++
		}
	}
	p.uncertain.Set(float64(uncertain))
	oldest := 0.0
	if len(list) > 0 {
		oldest = p.Now().Sub(list[0].SubmittedAt).Seconds()
	}
	p.oldest.Set(oldest)
	n, _, err := r.Callbacks().Attention(ctx)
	if err != nil {
		return err
	}
	p.attention.Set(float64(n))
	fees, err := r.ChainFees().Held(ctx)
	if err != nil {
		return err
	}
	held := 0
	for _, f := range fees {
		if strings.HasPrefix(f.TxHash, p.Custody.Provider()+":") {
			held++
		}
	}
	p.feesHeld.Set(float64(held))
	stopped, err := r.Suspensions().List(ctx)
	if err != nil {
		return err
	}
	p.suspended.Reset()
	for _, x := range stopped {
		p.suspended.WithLabelValues(x.Asset).Set(1)
	}
	return nil
}

// coins reads the custodian's coins and balances.
func (p *CustodyProcessor) coins(ctx context.Context) ([]ports.CustodyCoin, error) {
	coins, err := p.Custody.Coins(ctx)
	if err != nil {
		p.up.Set(0)
		return nil, err
	}
	p.up.Set(1)
	for _, c := range coins {
		if c.Balance != nil {
			p.balance.WithLabelValues(c.Code).Set(c.Balance.InexactFloat64())
		}
	}
	return coins, nil
}

// heldOf sums the balances of the asset's coins with the custodian. Given
// expected, what the ledger expects every holder of the asset to hold, a
// coin's balance of half of that in the coin's smallest unit or more is
// not believed: it is in that unit rather than in coins, and compared as
// it is it would hide any shortfall. A coin the custodian does not list
// whose networks are all closed both ways is left out (USDT on BEP20 and
// ERC20 while the merchant's wallet has it on TRON only): nothing of it
// can be there, and leaving it out can only show a shortfall, never hide
// one.
func heldOf(coins []ports.CustodyCoin, nets []domain.Network, asset string, expected decimal.Decimal) (decimal.Decimal, error) {
	var codes []string
	open := map[string]bool{}
	for _, n := range nets {
		if n.Asset != asset {
			continue
		}
		if !slices.Contains(codes, n.ProviderCoin) {
			codes = append(codes, n.ProviderCoin)
		}
		open[n.ProviderCoin] = open[n.ProviderCoin] || n.Enabled || n.WithdrawEnabled
	}
	held := decimal.Zero
	for _, code := range codes {
		i := slices.IndexFunc(coins, func(c ports.CustodyCoin) bool { return c.Code == code })
		if i < 0 && !open[code] {
			continue
		}
		if i < 0 || coins[i].Balance == nil {
			// Taken as zero it would look like a shortfall of all of it.
			return decimal.Zero, fmt.Errorf("the custodian reported no balance of %s (%s)", asset, code)
		}
		b, d := *coins[i].Balance, coins[i].Decimals
		if expected.IsPositive() && d > 0 && b.GreaterThanOrEqual(expected.Shift(d).Div(decimal.NewFromInt(2))) {
			return decimal.Zero, fmt.Errorf("the custodian reports %s of %s (%s) against %s the ledger expects: in its smallest unit (%d decimals)?",
				b, asset, code, expected, d)
		}
		held = held.Add(b)
	}
	return held, nil
}

// errNotCompared marks an asset Check left out; the others' checks still
// count.
var errNotCompared = errors.New("not compared")

// Holdings reports what the custodian holds of asset now, for the chain
// check of the platform's own wallets: a balance beyond belief (in the
// coin's smallest unit) is an error there too, not a holding that would
// hide the wallets' shortfall.
func (p *CustodyProcessor) Holdings(ctx context.Context, asset string) (decimal.Decimal, error) {
	nets, err := p.networks(ctx)
	if err != nil {
		return decimal.Zero, err
	}
	coins, err := p.Custody.Coins(ctx)
	if err != nil {
		return decimal.Zero, err
	}
	sys, err := p.Ledger.SystemBalances(ctx, asset)
	if err != nil {
		return decimal.Zero, err
	}
	return heldOf(coins, nets, asset, sys[accountDepositPending].Add(sys[accountWithdrawalPending]).Neg())
}

// Check compares, for every asset the custodian serves, what it holds with
// what the ledger expects every holder to hold, counting the platform's
// own wallets (Elsewhere), the withdrawals with the custodian (in flight)
// and its fees not booked yet (invariant 4). An asset whose balance the
// custodian did not report, or reported beyond belief, is not compared:
// its error is returned with the others' checks.
func (p *CustodyProcessor) Check(ctx context.Context) ([]domain.ChainCheck, error) {
	nets, err := p.networks(ctx)
	if err != nil {
		return nil, err
	}
	coins, err := p.coins(ctx)
	if err != nil {
		return nil, err
	}
	r := p.Store.Read()
	outstanding, err := r.Withdrawals().Outstanding(ctx, p.Custody.Provider())
	if err != nil {
		return nil, err
	}
	flying, unknown := inFlight(outstanding), unknownOutcome(outstanding)
	held, err := r.ChainFees().Held(ctx)
	if err != nil {
		return nil, err
	}
	feesHeld := map[string]decimal.Decimal{} // what the custodian's fees held for a person explain, by asset
	for _, f := range held {
		if !strings.HasPrefix(f.TxHash, p.Custody.Provider()+":") {
			continue
		}
		// A held fee is one beyond belief, in a unit nobody confirmed or
		// without a bound: it explains at most feeBound times its network's
		// withdrawal fee, not a loss as large as itself (review AB).
		if n, ok := networkOf(nets, f.Asset, f.Network); ok {
			feesHeld[f.Asset] = feesHeld[f.Asset].Add(decimal.Min(f.Amount, n.WithdrawFee.Mul(decimal.NewFromInt(feeBound))))
		}
	}
	// The simulated deposits a custody reset took out of the expectation:
	// shown on their own line, apart from the shortfall.
	baselines, err := r.Checks().Baselines(ctx, p.Custody.Provider())
	if err != nil {
		return nil, err
	}
	unbooked := map[string]decimal.Decimal{}
	networksOf := map[string][]string{}
	var assets []string
	for _, n := range nets {
		if !slices.Contains(assets, n.Asset) {
			assets = append(assets, n.Asset)
		}
		if !slices.Contains(networksOf[n.Asset], n.Network) {
			networksOf[n.Asset] = append(networksOf[n.Asset], n.Network)
		}
	}
	for _, network := range names(nets) {
		fees, err := r.ChainFees().Unbooked(ctx, network)
		if err != nil {
			return nil, err
		}
		for _, f := range fees {
			unbooked[f.Asset] = unbooked[f.Asset].Add(f.Amount)
		}
	}
	var out []domain.ChainCheck
	var skipped []error
	for _, asset := range assets {
		sys, err := p.Ledger.SystemBalances(ctx, asset)
		if err != nil {
			return out, err
		}
		expected := sys[accountDepositPending].Add(sys[accountWithdrawalPending]).Neg()
		held, err := heldOf(coins, nets, asset, expected)
		if err != nil {
			p.compared.WithLabelValues(asset).Set(1)
			skipped = append(skipped, fmt.Errorf("%s %w: %w", asset, errNotCompared, err))
			continue
		}
		elsewhere := decimal.Zero
		if p.Elsewhere != nil {
			if elsewhere, err = p.Elsewhere(ctx, asset); err != nil {
				return out, fmt.Errorf("what the platform's wallets hold of %s: %w", asset, err)
			}
		}
		addresses, err := r.Addresses().Count(ctx, networksOf[asset])
		if err != nil {
			return out, err
		}
		c := domain.NewChainCheck(p.Custody.Provider(), asset, held, expected, unbooked[asset], addresses, p.Now()).
			Beside(elsewhere, flying[asset])
		c.Baseline = baselines[asset]
		if err := p.Store.Tx(ctx, func(r ports.Repos) error { return r.Checks().Insert(ctx, c) }); err != nil {
			return out, err
		}
		p.compared.WithLabelValues(asset).Set(0)
		p.held.WithLabelValues(asset).Set(c.Chain.InexactFloat64())
		p.expected.WithLabelValues(asset).Set(c.Ledger.InexactFloat64())
		p.shortfall.WithLabelValues(asset).Set(c.Shortfall.InexactFloat64())
		if c.Shortfall.IsPositive() {
			p.Log.ErrorContext(ctx, "the custodian and the platform's wallets hold less than the ledger expects", "asset", asset,
				"held", c.Chain.String(), "elsewhere", c.Elsewhere.String(), "in_flight", c.InFlight.String(), "expected", c.Ledger.String(),
				"unbooked", c.Unbooked.String(), "shortfall", c.Shortfall.String())
		}
		if err := p.watchShortfall(ctx, c, unknown[asset].Add(feesHeld[asset]), p.stopOf(asset, nets)); err != nil {
			return out, err
		}
		out = append(out, c)
	}
	return out, errors.Join(skipped...)
}

// stopOf is how much of asset may be missing before its withdrawals stop:
// ShortfallStop's, else the smallest withdrawal fee of its networks with
// the custodian, or for networks charging none their smallest minimum
// withdrawal or deposit (review AB: not 0, which a rounding passes).
func (p *CustodyProcessor) stopOf(asset string, nets []domain.Network) decimal.Decimal {
	if stop, ok := p.ShortfallStop[asset]; ok {
		return stop
	}
	for _, of := range []func(domain.Network) decimal.Decimal{
		func(n domain.Network) decimal.Decimal { return n.WithdrawFee },
		func(n domain.Network) decimal.Decimal { return n.MinWithdraw },
		func(n domain.Network) decimal.Decimal { return n.MinDeposit },
	} {
		stop := decimal.Zero
		for _, n := range nets {
			if v := of(n); n.Asset == asset && v.IsPositive() && (stop.IsZero() || v.LessThan(stop)) {
				stop = v
			}
		}
		if stop.IsPositive() {
			return stop
		}
	}
	return decimal.Zero
}

// watchShortfall suspends an asset's withdrawals when more of it is
// missing than stop beyond what explains it (explained: withdrawals with
// an unknown outcome, handed over unanswered or UNCERTAIN, and the fees
// held for a person, each alerted on its own; and a difference a person
// accepted while it lasts) on two checks at least RecheckAfter apart. The
// first sighting is kept in the store, so a restart keeps it and a person
// lifting a suspension clears it, and has the check come again then; a
// check that finds nothing missing forgets it (design §9, review B4 and of
// ebb8aaa). A person lifts the suspension (ResumeWithdrawals).
func (p *CustodyProcessor) watchShortfall(ctx context.Context, c domain.ChainCheck, explained, stop decimal.Decimal) error {
	now := p.Now()
	w, err := p.Store.Read().Suspensions().Watch(ctx, c.Asset)
	if err != nil {
		return err
	}
	missing := c.Shortfall.Sub(explained)
	accepted := w.AcceptedAt(now)
	if lapsed := w.Accepted.IsPositive() && accepted.IsZero(); lapsed {
		if err := p.Store.Tx(ctx, func(r ports.Repos) error { return r.Suspensions().Clear(ctx, c.Asset, false, true) }); err != nil {
			return err
		}
	}
	if !missing.Sub(accepted).GreaterThan(stop) {
		if w.SuspectSince.IsZero() {
			return nil
		}
		return p.Store.Tx(ctx, func(r ports.Repos) error { return r.Suspensions().Clear(ctx, c.Asset, true, false) })
	}
	var since time.Time
	if err := p.Store.Tx(ctx, func(r ports.Repos) error {
		since, err = r.Suspensions().Suspect(ctx, c.Asset, now)
		return err
	}); err != nil {
		return err
	}
	if due := since.Add(p.RecheckAfter); now.Before(due) {
		if p.recheckAt.IsZero() || due.Before(p.recheckAt) {
			p.recheckAt = due
		}
		if w.SuspectSince.IsZero() {
			p.Log.ErrorContext(ctx, "funds missing that nothing explains: checked again, its withdrawals stop if still missing",
				"asset", c.Asset, "missing", missing.String(), "accepted", accepted.String(), "stop", stop.String(),
				"check_again_in", p.RecheckAfter.String())
		}
		return nil // the check at due decides
	}
	reason := fmt.Sprintf("the custody checks of %s and %s found %s %s missing that nothing in flight explains", since.UTC().Format(time.RFC3339),
		now.UTC().Format(time.RFC3339), missing, c.Asset)
	if accepted.IsPositive() {
		reason += fmt.Sprintf(" (%s of it accepted by %s)", accepted, w.AcceptedBy)
	}
	x := domain.Suspension{Asset: c.Asset, Shortfall: missing, SuspendedBy: domain.SuspendedBySystem, SuspendedAt: now, Reason: reason}
	done, err := suspend(ctx, p.Store, x)
	if err != nil {
		return err
	}
	if done {
		p.Log.ErrorContext(ctx, "withdrawals suspended: funds missing on two checks; a person finds the cause and lifts it (exchangectl wallet withdrawals-resume)",
			"asset", c.Asset, "missing", missing.String(), "accepted", accepted.String())
	}
	return nil
}
