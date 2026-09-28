package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/evm"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

// Ledger system accounts the chain check reads.
const (
	accountDepositPending    = "DEPOSIT_PENDING"
	accountWithdrawalPending = "WITHDRAWAL_PENDING"
	accountGasSupply         = "GAS_SUPPLY"
)

// transferGas is the gas of a plain coin transfer.
const transferGas = 21000

var txHashPattern = regexp.MustCompile(`^0x[0-9a-f]{64}$`)

// Processor carries out a network's operations on the instance holding
// the scanner lease: operator commands (sweeps, fundings, chain checks),
// the receipts of broadcast sweeps, the booking of chain fees, the hourly
// chain check of invariant 4 and the hot wallet's balance.
type Processor struct {
	Store    ports.Store
	Chain    ports.Transactor
	Signer   ports.Signer
	Ledger   ports.Ledger
	Networks ports.Networks
	Log      *slog.Logger
	Now      func() time.Time
	Network  string
	ChainID  uint64
	// CheckEvery paces the chain check, BalanceEvery the hot wallet's
	// balance.
	CheckEvery   time.Duration
	BalanceEvery time.Duration

	hot         string
	lastCheck   time.Time
	lastBalance time.Time

	chainGauge     *prometheus.GaugeVec
	ledgerGauge    *prometheus.GaugeVec
	shortfallGauge *prometheus.GaugeVec
	hotGauge       *prometheus.GaugeVec
	unbookedGauge  prometheus.Gauge
	openSweeps     prometheus.Gauge
}

// NewProcessor registers the processor's metrics with reg.
func NewProcessor(p Processor, reg prometheus.Registerer) *Processor {
	labels := prometheus.Labels{"network": p.Network}
	gauge := func(name, help string) *prometheus.GaugeVec {
		return prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help, ConstLabels: labels}, []string{"asset"})
	}
	p.chainGauge = gauge("wallet_chain_balance", "What the platform's wallets hold on chain, at the last chain check.")
	p.ledgerGauge = gauge("wallet_chain_expected", "What the ledger expects the wallets to hold: −(DEPOSIT_PENDING + WITHDRAWAL_PENDING).")
	p.shortfallGauge = gauge("wallet_chain_shortfall", "Expected minus held minus unbooked gas; above 0 the wallets miss funds (invariant 4).")
	p.hotGauge = gauge("wallet_hot_wallet_balance", "The hot wallet's balance.")
	p.unbookedGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "wallet_chain_fees_unbooked", Help: "Gas paid on chain that the ledger has not booked yet (GAS_SUPPLY short?).", ConstLabels: labels,
	})
	p.openSweeps = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "wallet_sweeps_open", Help: "Broadcast sweeps waiting for their receipt.", ConstLabels: labels,
	})
	reg.MustRegister(p.chainGauge, p.ledgerGauge, p.shortfallGauge, p.hotGauge, p.unbookedGauge, p.openSweeps)
	if p.CheckEvery <= 0 {
		p.CheckEvery = time.Hour
	}
	if p.BalanceEvery <= 0 {
		p.BalanceEvery = 5 * time.Minute
	}
	return &p
}

// Round runs every step once; a failing step does not stop the others.
func (p *Processor) Round(ctx context.Context) error {
	if p.hot == "" {
		hot, err := p.Signer.HotWallet(ctx)
		if err != nil {
			return fmt.Errorf("hot wallet: %w", err)
		}
		p.hot = hot
	}
	native, err := p.native(ctx)
	if err != nil {
		return err
	}
	errs := []error{p.commands(ctx, native), p.trackSweeps(ctx, native), p.bookFees(ctx)}
	now := p.Now()
	if now.Sub(p.lastCheck) >= p.CheckEvery {
		if _, err := p.check(ctx, native); err != nil {
			errs = append(errs, fmt.Errorf("chain check: %w", err))
		} else {
			p.lastCheck = now
		}
	}
	if now.Sub(p.lastBalance) >= p.BalanceEvery {
		if b, err := p.Chain.Balance(ctx, p.hot); err != nil {
			errs = append(errs, fmt.Errorf("hot wallet balance: %w", err))
		} else {
			p.hotGauge.WithLabelValues(native.Asset).Set(evm.FromWei(b, evm.NativeDecimals).InexactFloat64())
			p.lastBalance = now
		}
	}
	return errors.Join(errs...)
}

// native returns the network's coin (the asset without a contract).
func (p *Processor) native(ctx context.Context) (domain.Network, error) {
	nets, err := p.Networks.OnNetwork(ctx, p.Network)
	if err != nil {
		return domain.Network{}, err
	}
	for _, n := range nets {
		if n.Contract == "" {
			return n, nil
		}
	}
	return domain.Network{}, fmt.Errorf("no coin is configured on %s", p.Network)
}

// Queue records an operator command for the processor.
func Queue(ctx context.Context, store ports.Store, network, kind string, args map[string]string, by string, now time.Time) (domain.Command, error) {
	switch kind {
	case domain.CommandSweep, domain.CommandFund, domain.CommandReconcile:
	default:
		return domain.Command{}, fmt.Errorf("unknown command %q", kind)
	}
	if strings.TrimSpace(by) == "" {
		return domain.Command{}, errors.New("the operator is required")
	}
	c := domain.Command{
		ID: uuid.Must(uuid.NewV7()).String(), Network: network, Kind: kind, Args: args, Status: domain.CommandPending,
		RequestedBy: by, CreatedAt: now,
	}
	return c, store.Tx(ctx, func(r ports.Repos) error { return r.Commands().Insert(ctx, c) })
}

func (p *Processor) commands(ctx context.Context, native domain.Network) error {
	list, err := p.Store.Read().Commands().Pending(ctx, p.Network)
	if err != nil {
		return err
	}
	for _, c := range list {
		result, done, err := p.run(ctx, c, native)
		switch {
		case err != nil:
			c.Status, c.Result, c.DoneAt = domain.CommandFailed, err.Error(), p.Now()
		case done:
			c.Status, c.Result, c.DoneAt = domain.CommandDone, result, p.Now()
		default:
			c.Result = result
		}
		p.Log.InfoContext(ctx, "operator command", "command_id", c.ID, "kind", c.Kind, "status", c.Status, "result", c.Result,
			"requested_by", c.RequestedBy)
		if err := p.Store.Tx(ctx, func(r ports.Repos) error { return r.Commands().Update(ctx, c) }); err != nil {
			return err
		}
	}
	return nil
}

// run carries out a command; done false leaves it pending for the next
// round (a funding waiting for confirmations).
func (p *Processor) run(ctx context.Context, c domain.Command, native domain.Network) (string, bool, error) {
	switch c.Kind {
	case domain.CommandSweep:
		return p.sweep(ctx, c, native)
	case domain.CommandFund:
		return p.fund(ctx, c, native)
	case domain.CommandReconcile:
		check, err := p.check(ctx, native)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("held %s, expected %s, unbooked gas %s, shortfall %s %s", check.Chain, check.Ledger, check.Unbooked,
			check.Shortfall, check.Asset), true, nil
	}
	return "", false, fmt.Errorf("unknown command %q", c.Kind)
}

// fees returns a fee cap with room for the base fee to double, and the
// tip.
func (p *Processor) fees(ctx context.Context) (maxFee, tip *big.Int, err error) {
	base, tip, err := p.Chain.Fees(ctx)
	if err != nil {
		return nil, nil, err
	}
	return new(big.Int).Add(new(big.Int).Mul(base, big.NewInt(2)), tip), tip, nil
}

// sweep moves the coin of every deposit address holding at least the
// command's minimum (default the network's minimum deposit) to the hot
// wallet, less the gas.
func (p *Processor) sweep(ctx context.Context, c domain.Command, native domain.Network) (string, bool, error) {
	least := native.MinDeposit
	if s := c.Args["min"]; s != "" {
		v, err := decimal.NewFromString(s)
		if err != nil || v.IsNegative() {
			return "", false, fmt.Errorf("bad minimum %q", s)
		}
		least = v
	}
	floor, err := evm.ToWei(least.Truncate(evm.NativeDecimals), evm.NativeDecimals)
	if err != nil {
		return "", false, err
	}
	r := p.Store.Read()
	addrs, err := r.Addresses().List(ctx, p.Network)
	if err != nil {
		return "", false, err
	}
	open, err := r.Sweeps().Open(ctx, p.Network)
	if err != nil {
		return "", false, err
	}
	busy := map[string]bool{}
	for _, s := range open {
		busy[strings.ToLower(s.Address)] = true
	}
	maxFee, tip, err := p.fees(ctx)
	if err != nil {
		return "", false, err
	}
	cost := new(big.Int).Mul(maxFee, big.NewInt(transferGas))
	swept, total := 0, decimal.Zero
	for _, a := range addrs {
		if busy[strings.ToLower(a.Address)] {
			continue
		}
		balance, err := p.Chain.Balance(ctx, a.Address)
		if err != nil {
			return "", false, err
		}
		if balance.Cmp(floor) < 0 || balance.Cmp(cost) <= 0 {
			continue
		}
		value := new(big.Int).Sub(balance, cost)
		nonce, err := p.Chain.PendingNonce(ctx, a.Address)
		if err != nil {
			return "", false, err
		}
		id := uuid.Must(uuid.NewV7()).String()
		signed, err := p.Signer.Sign(ctx, ports.SignRequest{
			ID: id, Purpose: domain.FeeSweep, Reference: id, ChainID: p.ChainID, Index: a.Index, Nonce: nonce, To: p.hot,
			Value: value, GasLimit: transferGas, MaxFee: maxFee, MaxTip: tip,
		})
		if err != nil {
			return "", false, fmt.Errorf("sign the sweep of %s: %w", a.Address, err)
		}
		now := p.Now()
		s := domain.Sweep{
			ID: id, Network: p.Network, Address: a.Address, Index: a.Index, Asset: native.Asset,
			Amount: evm.FromWei(value, evm.NativeDecimals), Nonce: nonce, TxHash: strings.ToLower(signed.TxHash), Raw: signed.Raw,
			Status: domain.SweepBroadcast, CommandID: c.ID, CreatedAt: now, UpdatedAt: now,
		}
		// Recorded before the broadcast: a lost broadcast is sent again.
		if err := p.Store.Tx(ctx, func(r ports.Repos) error { return r.Sweeps().Insert(ctx, s) }); err != nil {
			return "", false, err
		}
		if err := p.Chain.SendRaw(ctx, s.Raw); err != nil {
			p.Log.WarnContext(ctx, "sweep broadcast failed, will retry", "sweep_id", s.ID, "error", err)
		}
		p.Log.InfoContext(ctx, "sweep broadcast", "sweep_id", s.ID, "address", s.Address, "amount", s.Amount.String(), "tx_hash", s.TxHash)
		swept++
		total = total.Add(s.Amount)
	}
	return fmt.Sprintf("%d addresses swept: %s %s to the hot wallet %s", swept, total, native.Asset, p.hot), true, nil
}

// trackSweeps settles broadcast sweeps from their receipts, recording the
// gas, and broadcasts again those the node lost.
func (p *Processor) trackSweeps(ctx context.Context, native domain.Network) error {
	open, err := p.Store.Read().Sweeps().Open(ctx, p.Network)
	if err != nil {
		return err
	}
	p.openSweeps.Set(float64(len(open)))
	for _, s := range open {
		rc, err := p.Chain.Receipt(ctx, s.TxHash)
		if err != nil {
			return err
		}
		if rc == nil {
			if err := p.rebroadcast(ctx, s.TxHash, s.Raw, s.UpdatedAt); err != nil {
				p.Log.WarnContext(ctx, "sweep rebroadcast failed", "sweep_id", s.ID, "error", err)
			}
			continue
		}
		s.Status, s.UpdatedAt = domain.SweepConfirmed, p.Now()
		if !rc.Succeeded {
			s.Status = domain.SweepFailed
		}
		fee := evm.FromWei(new(big.Int).Mul(new(big.Int).SetUint64(rc.GasUsed), rc.EffectiveGasPrice), evm.NativeDecimals)
		err = p.Store.Tx(ctx, func(r ports.Repos) error {
			if err := r.Sweeps().Update(ctx, s); err != nil {
				return err
			}
			if !fee.IsPositive() {
				return nil
			}
			return r.ChainFees().Insert(ctx, domain.ChainFee{
				TxHash: s.TxHash, Network: p.Network, Asset: native.Asset, Amount: fee, Purpose: domain.FeeSweep, Reference: s.ID,
			})
		})
		if err != nil {
			return err
		}
		p.Log.InfoContext(ctx, "sweep mined", "sweep_id", s.ID, "status", s.Status, "fee", fee.String())
	}
	return nil
}

// rebroadcast sends a transaction again when the node has not known it
// for a minute.
func (p *Processor) rebroadcast(ctx context.Context, hash, raw string, since time.Time) error {
	if p.Now().Sub(since) < time.Minute {
		return nil
	}
	tx, err := p.Chain.Transaction(ctx, hash)
	if err != nil || tx != nil {
		return err
	}
	return p.Chain.SendRaw(ctx, raw)
}

// bookFees asks the ledger to book the gas of mined transactions; with
// GAS_SUPPLY short the fees wait (wallet_chain_fees_unbooked).
func (p *Processor) bookFees(ctx context.Context) error {
	fees, err := p.Store.Read().ChainFees().Unbooked(ctx, p.Network)
	if err != nil {
		return err
	}
	left := decimal.Zero
	var failed error
	for _, f := range fees {
		journal, err := p.Ledger.BookChainFee(ctx, f.TxHash, f.Asset, f.Amount, f.TxHash)
		if err != nil {
			left = left.Add(f.Amount)
			failed = fmt.Errorf("book the gas of %s: %w", f.TxHash, err)
			continue
		}
		if err := p.Store.Tx(ctx, func(r ports.Repos) error { return r.ChainFees().MarkBooked(ctx, f.TxHash, journal) }); err != nil {
			return err
		}
	}
	p.unbookedGauge.Set(left.InexactFloat64())
	return failed
}

// fund books the platform's transfer into the hot wallet (args tx, account
// default GAS_SUPPLY) once it has the network's confirmations.
func (p *Processor) fund(ctx context.Context, c domain.Command, native domain.Network) (string, bool, error) {
	hash := strings.ToLower(c.Args["tx"])
	account := c.Args["account"]
	if account == "" {
		account = accountGasSupply
	}
	if !txHashPattern.MatchString(hash) {
		return "", false, fmt.Errorf("bad transaction hash %q", c.Args["tx"])
	}
	r := p.Store.Read()
	if f, err := r.Fundings().Get(ctx, hash); err != nil || f != nil {
		if f != nil {
			return fmt.Sprintf("already booked to %s (journal %s)", f.AccountType, f.JournalID), true, nil
		}
		return "", false, err
	}
	tx, err := p.Chain.Transaction(ctx, hash)
	if err != nil {
		return "", false, err
	}
	if tx == nil {
		return "", false, errors.New("the node does not know the transaction")
	}
	if tx.BlockNumber == 0 {
		return "waiting for the transaction to be mined", false, nil
	}
	if !strings.EqualFold(tx.To, p.hot) {
		return "", false, fmt.Errorf("the transaction pays %s, not the hot wallet %s", tx.To, p.hot)
	}
	owners, err := r.Addresses().Owners(ctx, p.Network)
	if err != nil {
		return "", false, err
	}
	if _, sweep := owners[strings.ToLower(tx.From)]; sweep {
		return "", false, errors.New("the transaction comes from a deposit address: a sweep, not a funding")
	}
	rc, err := p.Chain.Receipt(ctx, hash)
	if err != nil {
		return "", false, err
	}
	if rc == nil || !rc.Succeeded {
		return "", false, errors.New("the transaction failed")
	}
	head, err := p.Chain.Head(ctx)
	if err != nil {
		return "", false, err
	}
	need := uint64(max(native.Confirmations, 1))
	if conf := head - min(head, tx.BlockNumber) + 1; conf < need {
		return fmt.Sprintf("waiting for confirmations (%d/%d)", conf, need), false, nil
	}
	amount := evm.FromWei(tx.Value, evm.NativeDecimals)
	if !amount.IsPositive() {
		return "", false, errors.New("the transaction transfers nothing")
	}
	journal, err := p.Ledger.Fund(ctx, hash, account, native.Asset, amount, hash)
	if err != nil {
		return "", false, err
	}
	f := domain.Funding{
		TxHash: hash, Network: p.Network, Asset: native.Asset, AccountType: account, Amount: amount, JournalID: journal,
		CommandID: c.ID, CreatedAt: p.Now(),
	}
	if err := p.Store.Tx(ctx, func(r ports.Repos) error { return r.Fundings().Insert(ctx, f) }); err != nil {
		return "", false, err
	}
	return fmt.Sprintf("booked %s %s to %s (journal %s)", amount, native.Asset, account, journal), true, nil
}

// check compares the wallets' holdings with the ledger (invariant 4).
func (p *Processor) check(ctx context.Context, native domain.Network) (domain.ChainCheck, error) {
	r := p.Store.Read()
	addrs, err := r.Addresses().List(ctx, p.Network)
	if err != nil {
		return domain.ChainCheck{}, err
	}
	total := new(big.Int)
	for _, a := range append([]string{p.hot}, addresses(addrs)...) {
		b, err := p.Chain.Balance(ctx, a)
		if err != nil {
			return domain.ChainCheck{}, err
		}
		total.Add(total, b)
	}
	sys, err := p.Ledger.SystemBalances(ctx, native.Asset)
	if err != nil {
		return domain.ChainCheck{}, err
	}
	fees, err := r.ChainFees().Unbooked(ctx, p.Network)
	if err != nil {
		return domain.ChainCheck{}, err
	}
	unbooked := decimal.Zero
	for _, f := range fees {
		unbooked = unbooked.Add(f.Amount)
	}
	c := domain.NewChainCheck(p.Network, native.Asset, evm.FromWei(total, evm.NativeDecimals),
		sys[accountDepositPending].Add(sys[accountWithdrawalPending]).Neg(), unbooked, len(addrs)+1, p.Now())
	if err := p.Store.Tx(ctx, func(r ports.Repos) error { return r.Checks().Insert(ctx, c) }); err != nil {
		return domain.ChainCheck{}, err
	}
	p.chainGauge.WithLabelValues(c.Asset).Set(c.Chain.InexactFloat64())
	p.ledgerGauge.WithLabelValues(c.Asset).Set(c.Ledger.InexactFloat64())
	p.shortfallGauge.WithLabelValues(c.Asset).Set(c.Shortfall.InexactFloat64())
	if c.Shortfall.IsPositive() {
		p.Log.ErrorContext(ctx, "the platform wallets hold less than the ledger expects", "network", p.Network, "asset", c.Asset,
			"held", c.Chain.String(), "expected", c.Ledger.String(), "unbooked", c.Unbooked.String(), "shortfall", c.Shortfall.String())
	}
	return c, nil
}

func addresses(list []domain.Address) []string {
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = a.Address
	}
	return out
}
