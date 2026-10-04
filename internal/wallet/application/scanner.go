package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	walletv1 "github.com/skill/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/skill/exchange/internal/platform/evm"
	"github.com/skill/exchange/internal/wallet/domain"
	"github.com/skill/exchange/internal/wallet/ports"
)

// reasonUserClosed is user-service's eligibility refusal for a closed
// account.
const reasonUserClosed = "USER_CLOSED"

// Scanner follows one network's blocks (§11.5). Each block's parent hash
// must match the block scanned before it; otherwise the chain reorganized
// and the scan goes back confirmations + 6 blocks, orphaning the pending
// deposits there (a rescan that finds them again detects them anew).
// Deposits are unique per (transaction, log). Confirmations count up to
// the last verified block, and confirmed deposits go to the ledger while
// their asset takes deposits.
type Scanner struct {
	Store       ports.Store
	Chain       ports.Chain
	Networks    ports.Networks
	Eligibility ports.Eligibility
	Log         *slog.Logger
	Now         func() time.Time
	Network     string
	// Start is the first block when nothing was scanned yet; 0 starts at
	// the head (history before the service is not scanned).
	Start uint64
	// Batch caps the blocks of one round; Keep is how many block hashes
	// stay for reorganization checks.
	Batch int
	Keep  uint64

	lag      prometheus.Gauge
	head     prometheus.Gauge
	held     prometheus.Gauge
	success  prometheus.Gauge
	detected *prometheus.CounterVec
	orphaned prometheus.Counter
}

// NewScanner registers the scanner's metrics with reg.
func NewScanner(s Scanner, reg prometheus.Registerer) *Scanner {
	labels := prometheus.Labels{"network": s.Network}
	s.lag = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "wallet_scan_lag_blocks", Help: "Blocks between the chain head and the last scanned block.", ConstLabels: labels,
	})
	s.head = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "wallet_scan_block", Help: "The last scanned block.", ConstLabels: labels,
	})
	s.held = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "wallet_deposits_held", Help: "Confirmed deposits waiting for their asset to take deposits again.", ConstLabels: labels,
	})
	s.success = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "wallet_scan_last_success_timestamp_seconds", Help: "When a scan round last completed.", ConstLabels: labels,
	})
	s.detected = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "wallet_deposits_detected_total", Help: "Deposits found on chain, by status at detection.", ConstLabels: labels,
	}, []string{"status"})
	s.orphaned = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "wallet_deposits_orphaned_total", Help: "Deposits a reorganization dropped.", ConstLabels: labels,
	})
	reg.MustRegister(s.lag, s.head, s.held, s.success, s.detected, s.orphaned)
	if s.Batch <= 0 {
		s.Batch = 20
	}
	if s.Keep == 0 {
		s.Keep = 128
	}
	return &s
}

// Round scans the blocks since the cursor (at most Batch), counts the
// confirmations of pending deposits and sends confirmed ones to the
// ledger.
func (s *Scanner) Round(ctx context.Context) error {
	nets, err := s.Networks.OnNetwork(ctx, s.Network)
	if err != nil {
		return err
	}
	head, err := s.Chain.Head(ctx)
	if err != nil {
		return err
	}
	cursor, err := s.Store.Read().Blocks().Cursor(ctx, s.Network)
	if err != nil {
		return err
	}
	if cursor == 0 {
		cursor = head - 1
		if s.Start > 0 {
			cursor = s.Start - 1
		}
	}
	if cursor, err = s.scan(ctx, nets, cursor, head); err != nil {
		return err
	}
	s.head.Set(float64(cursor))
	s.lag.Set(float64(head - min(cursor, head)))
	if err := s.confirm(ctx, cursor); err != nil {
		return err
	}
	if err := s.request(ctx, nets); err != nil {
		return err
	}
	if cursor > s.Keep {
		if err := s.Store.Read().Blocks().Prune(ctx, s.Network, cursor-s.Keep); err != nil {
			return err
		}
	}
	s.success.SetToCurrentTime()
	return nil
}

// scan reads the blocks after cursor up to head (at most Batch) and
// records their deposits and hashes in one transaction; it returns the
// new cursor.
func (s *Scanner) scan(ctx context.Context, nets []domain.Network, cursor, head uint64) (uint64, error) {
	to := min(head, cursor+uint64(s.Batch)) //nolint:gosec // Batch is positive
	if to <= cursor {
		return cursor, nil
	}
	r := s.Store.Read()
	owners, err := r.Addresses().Owners(ctx, s.Network)
	if err != nil {
		return cursor, err
	}
	prev, err := r.Blocks().Hash(ctx, s.Network, cursor)
	if err != nil {
		return cursor, err
	}
	blocks := make([]ports.Block, 0, to-cursor)
	for n := cursor + 1; n <= to; n++ {
		b, err := s.Chain.Block(ctx, n, owners)
		if err != nil {
			return cursor, err
		}
		if prev != "" && !strings.EqualFold(prev, b.ParentHash) {
			if n == cursor+1 {
				return cursor, s.reorganize(ctx, n, nets)
			}
			// The chain moved while this round read it: read again.
			return cursor, fmt.Errorf("block %d does not follow the block %d just read", n, n-1)
		}
		prev = b.Hash
		blocks = append(blocks, b)
	}
	if len(owners) > 0 {
		tokens, err := s.Chain.TokenTransfers(ctx, cursor+1, to, slices.Sorted(maps.Keys(owners)))
		if err != nil {
			return cursor, err
		}
		for _, t := range tokens {
			if t.BlockNumber <= cursor || t.BlockNumber > to || !strings.EqualFold(blocks[t.BlockNumber-cursor-1].Hash, t.BlockHash) {
				return cursor, fmt.Errorf("token transfer %s is not in block %d as read", t.TxHash, t.BlockNumber)
			}
			blocks[t.BlockNumber-cursor-1].Transfers = append(blocks[t.BlockNumber-cursor-1].Transfers, t)
		}
	}
	found, err := s.classify(ctx, nets, blocks, owners)
	if err != nil {
		return cursor, err
	}
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		for _, d := range found {
			if err := s.detect(ctx, r, d); err != nil {
				return err
			}
		}
		for _, b := range blocks {
			if err := r.Blocks().Save(ctx, s.Network, b.Number, b.Hash); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return cursor, err
	}
	return to, nil
}

// classify turns transfers into new deposits: a configured asset's go
// through its minimum; other tokens are rejected (UNSUPPORTED_TOKEN), and
// so are amounts too small for the asset's decimals.
func (s *Scanner) classify(ctx context.Context, nets []domain.Network, blocks []ports.Block, owners map[string]string) ([]domain.Deposit, error) {
	byContract := make(map[string]domain.Network, len(nets))
	for _, n := range nets {
		byContract[strings.ToLower(n.Contract)] = n
	}
	decimals := map[string]int32{}
	now := s.Now()
	var out []domain.Deposit
	for _, b := range blocks {
		for _, t := range b.Transfers {
			user, ok := owners[strings.ToLower(t.To)]
			if !ok || t.Amount.Sign() <= 0 {
				continue
			}
			d := domain.Deposit{
				ID: uuid.Must(uuid.NewV7()).String(), UserID: user, Network: s.Network, Address: evm.Checksum(t.To),
				Contract: strings.ToLower(t.Contract), TxHash: strings.ToLower(t.TxHash), LogIndex: t.LogIndex,
				BlockNumber: b.Number, BlockHash: b.Hash, RawAmount: decimal.NewFromBigInt(t.Amount, 0),
				Status: domain.StatusDetected, DetectedAt: now,
			}
			net, known := byContract[d.Contract]
			if !known {
				d.Status, d.Reason = domain.StatusRejected, domain.ReasonUnsupportedToken
				out = append(out, d)
				continue
			}
			dec, cached := decimals[d.Contract]
			if !cached {
				var err error
				if dec, err = s.Chain.Decimals(ctx, d.Contract); err != nil {
					return nil, err
				}
				decimals[d.Contract] = dec
			}
			d.Asset, d.Required = net.Asset, max(net.Confirmations, 1)
			d.Amount = evm.FromWei(t.Amount, dec).Truncate(net.Decimals)
			switch {
			case d.Amount.IsZero():
				d.Status, d.Reason = domain.StatusRejected, domain.ReasonBelowMinimum
			case d.Amount.LessThan(net.MinDeposit):
				d.Unclaimed, d.Reason = true, domain.ReasonBelowMinimum
			}
			out = append(out, d)
		}
	}
	return out, nil
}

// detect records a deposit found by the scan. A known transfer changes
// only when a reorganization moved it: an orphan returns to DETECTED.
func (s *Scanner) detect(ctx context.Context, r ports.Repos, d domain.Deposit) error {
	known, err := r.Deposits().Find(ctx, d.Network, d.TxHash, d.LogIndex)
	if err != nil {
		return err
	}
	if known != nil {
		revived := known.Status == domain.StatusOrphaned
		if !known.Seen(d.BlockNumber, d.BlockHash) {
			return nil
		}
		if err := r.Deposits().Update(ctx, *known); err != nil {
			return err
		}
		if !revived {
			return nil
		}
		s.Log.InfoContext(ctx, "orphaned deposit seen again", "deposit_id", known.ID, "block", known.BlockNumber)
		return r.Emit(ctx, &walletv1.DepositDetected{Deposit: ToProto(*known)}, known.UserID)
	}
	if err := r.Deposits().Insert(ctx, d); err != nil {
		return err
	}
	s.detected.WithLabelValues(d.Status).Inc()
	s.Log.InfoContext(ctx, "deposit detected", "network", d.Network, "asset", d.Asset, "amount", d.Amount.String(),
		"block", d.BlockNumber, "deposit_id", d.ID, "status", d.Status, "reason", d.Reason)
	if d.Status == domain.StatusRejected {
		return r.Emit(ctx, &walletv1.DepositRejected{Deposit: ToProto(d)}, d.UserID)
	}
	return r.Emit(ctx, &walletv1.DepositDetected{Deposit: ToProto(d)}, d.UserID)
}

// reorganize goes back confirmations + 6 blocks before n (§11.5),
// orphaning the pending deposits from there; the next round rescans.
func (s *Scanner) reorganize(ctx context.Context, n uint64, nets []domain.Network) error {
	var depth uint32
	for _, net := range nets {
		depth = max(depth, net.Confirmations)
	}
	from := uint64(1)
	if back := uint64(depth) + 6; n > back {
		from = n - back
	}
	s.Log.WarnContext(ctx, "chain reorganized, rescanning", "network", s.Network, "at", n, "from", from)
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		list, err := r.Deposits().FromBlock(ctx, s.Network, from)
		if err != nil {
			return err
		}
		for _, d := range list {
			if err := d.Orphan(); err != nil {
				return err
			}
			s.orphaned.Inc()
			if err := r.Deposits().Update(ctx, d); err != nil {
				return err
			}
			if err := r.Emit(ctx, &walletv1.DepositOrphaned{Deposit: ToProto(d)}, d.UserID); err != nil {
				return err
			}
		}
		return r.Blocks().Rewind(ctx, s.Network, from)
	})
}

// confirm counts the confirmations of pending deposits up to head, the
// last verified block.
func (s *Scanner) confirm(ctx context.Context, head uint64) error {
	pending, err := s.Store.Read().Deposits().Pending(ctx, s.Network)
	if err != nil {
		return err
	}
	now := s.Now()
	var changed []domain.Deposit
	for _, d := range pending {
		before := d.Confirmations
		if d.Observe(head, now) || d.Confirmations != before {
			changed = append(changed, d)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		for _, d := range changed {
			if err := r.Deposits().Update(ctx, d); err != nil {
				return err
			}
			if d.Status == domain.StatusConfirmed {
				s.Log.InfoContext(ctx, "deposit confirmed", "deposit_id", d.ID, "confirmations", d.Confirmations)
			}
		}
		return nil
	})
}

// request sends confirmed deposits to the ledger (DepositConfirmed)
// while their asset takes deposits; the others wait.
func (s *Scanner) request(ctx context.Context, nets []domain.Network) error {
	held, err := requestCredits(ctx, s.Store, s.Eligibility, nil, s.Network, nets, s.Now, nil) // its addresses all have a user
	s.held.Set(float64(held))
	return err
}

// requestCredits sends the network's confirmed deposits to the ledger
// (DepositConfirmed) while their asset takes deposits, and returns how
// many wait. A closed account's deposit goes to UNCLAIMED_DEPOSIT (§5.4).
// A backfill the custodian's callback disagreed with waits for a person,
// who closes it (C5.5 ⑦).
// creditNobody books a deposit of nobody to UNCLAIMED_DEPOSIT by asking the
// ledger itself (B7a): announcing it, as the others are, would hand it to
// services that need a user. The ledger's key (deposit:<id>) makes a retry
// harmless; the deposit is marked once the journal is known, unless the
// ledger's own event marked it first.
func creditNobody(ctx context.Context, store ports.Store, ledger ports.Ledger, d domain.Deposit, now func() time.Time) error {
	if ledger == nil {
		return errors.New("no ledger to book a deposit of nobody")
	}
	journal, err := ledger.CreditUnclaimed(ctx, d.ID, d.Asset, d.Amount, d.Network, d.TxHash, d.Reason)
	if err != nil {
		return err
	}
	return store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Deposits().GetForUpdate(ctx, d.ID)
		if err != nil || cur == nil || !cur.RequestCredit(cur.Reason, now()) {
			return err
		}
		cur.Credit(journal, now())
		return r.Deposits().Update(ctx, *cur)
	})
}

// nobodyRetries paces the deposits of nobody the ledger would not book
// (review AL): one waits a minute, then twice as long after each failure,
// up to an hour, so a deposit refused for good is tried and reported about
// once an hour rather than every round. Kept in memory: after a restart
// each is tried again at once.
type nobodyRetries struct {
	mu   sync.Mutex
	next map[string]nobodyRetry
}

type nobodyRetry struct {
	Network string
	At      time.Time
	Delay   time.Duration
}

const (
	nobodyFirstRetry = time.Minute
	nobodyLastRetry  = time.Hour
)

// due reports whether deposit id may be tried at now.
func (n *nobodyRetries) due(id string, now time.Time) bool {
	if n == nil {
		return true
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	r, ok := n.next[id]
	return !ok || !now.Before(r.At)
}

// failed has deposit id of network wait before its next try.
func (n *nobodyRetries) failed(id, network string, now time.Time) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.next == nil {
		n.next = map[string]nobodyRetry{}
	}
	delay := nobodyFirstRetry
	if r, ok := n.next[id]; ok {
		delay = min(r.Delay*2, nobodyLastRetry)
	}
	n.next[id] = nobodyRetry{Network: network, At: now.Add(delay), Delay: delay}
}

// keep forgets network's deposits not in ids (booked, or closed by a
// person).
func (n *nobodyRetries) keep(network string, ids []string) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	maps.DeleteFunc(n.next, func(id string, r nobodyRetry) bool { return r.Network == network && !slices.Contains(ids, id) })
}

// waiting counts the deposits of nobody that wait to be tried again.
func (n *nobodyRetries) waiting() int {
	if n == nil {
		return 0
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.next)
}

func requestCredits(ctx context.Context, store ports.Store, eligibility ports.Eligibility, ledger ports.Ledger, network string,
	nets []domain.Network, now func() time.Time, retries *nobodyRetries,
) (int, error) {
	list, err := store.Read().Deposits().Unrequested(ctx, network)
	if err != nil {
		return 0, err
	}
	// A deposit of nobody no longer waiting (booked, or closed by a person)
	// leaves the retries even when the round below stops early (review AR).
	var pending []string
	for _, d := range list {
		if d.UserID == domain.NoOwner && d.Resolution == "" {
			pending = append(pending, d.ID)
		}
	}
	retries.keep(network, pending)
	enabled := make(map[string]bool, len(nets))
	for _, n := range nets {
		if n.Network == network {
			enabled[n.Asset] = n.Enabled
		}
	}
	held := 0
	var nobody []error
	var failing []string
	for _, d := range list {
		if d.Resolution != "" {
			continue // closed by an administrator: never credited
		}
		if !enabled[d.Asset] || d.Discrepancy != "" {
			held++
			continue
		}
		if d.UserID == domain.NoOwner {
			// One the ledger will not book (refused for good, say) waits
			// on its own, tried again later and reported then: the others'
			// credits go on, as a user's deposit the ledger cannot book
			// goes to its dead letters (reviews AJ, AL).
			if !retries.due(d.ID, now()) {
				failing = append(failing, d.ID)
				continue
			}
			if err := creditNobody(ctx, store, ledger, d, now); err != nil {
				retries.failed(d.ID, network, now())
				failing = append(failing, d.ID)
				nobody = append(nobody, fmt.Errorf("deposit %s of nobody: %w", d.ID, err))
			}
			continue
		}
		reason := d.Reason
		if !d.Unclaimed {
			allowed, code, err := eligibility.Check(ctx, d.UserID, FeatureDeposit)
			if err != nil {
				return held, err
			}
			switch {
			case allowed:
			case code == reasonUserClosed:
				reason = domain.ReasonAccountClosed
			default:
				reason = domain.ReasonNotEligible
			}
		}
		err := store.Tx(ctx, func(r ports.Repos) error {
			cur, err := r.Deposits().GetForUpdate(ctx, d.ID)
			if err != nil || cur == nil || !cur.RequestCredit(reason, now()) {
				return err
			}
			if err := r.Deposits().Update(ctx, *cur); err != nil {
				return err
			}
			return r.Emit(ctx, &walletv1.DepositConfirmed{Deposit: ToProto(*cur)}, cur.UserID)
		})
		if err != nil {
			return held, errors.Join(append(nobody, fmt.Errorf("deposit %s: %w", d.ID, err))...)
		}
	}
	retries.keep(network, failing)
	return held, errors.Join(nobody...)
}
