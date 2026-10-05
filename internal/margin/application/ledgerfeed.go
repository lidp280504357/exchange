package application

import (
	"context"
	"crypto/sha256"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/event"
)

// AutoRepayPrefix starts the ledger key of an automatic repayment: the
// settlement of a trade whose order has side_effect AUTO_REPAY repays the
// margin account's debt of what it received (review CH ⑤).
const AutoRepayPrefix = "trade-repay:"

// Entry is a ledger journal as ledger.events carries it.
type Entry struct {
	JournalID string
	EntryType string
	IdemKey   string
	Memo      string
	At        time.Time
	Lines     []EntryLine
}

// EntryLine is one line of a journal.
type EntryLine struct {
	UserID      string
	AccountType string
	Scope       string
	Asset       string
	Amount      decimal.Decimal
}

// OnEntry applies a journal margin-service did not post itself: an
// automatic repayment in a trade's settlement (MARGIN_REPAY under
// trade-repay:<trade>:<side>) is recorded as a DONE repayment of reason
// AUTO_REPAY, the loan and the pool follow it and MarginRepaid goes out —
// once per journal, the repayment's key being the ledger's. Every other
// journal is left alone.
func (s *Service) OnEntry(ctx context.Context, e Entry) error {
	if e.EntryType != "MARGIN_REPAY" || !strings.HasPrefix(e.IdemKey, AutoRepayPrefix) {
		return nil
	}
	p := ports.Repay{
		ID: uuid.Must(uuid.NewV7()).String(), Reason: ports.RepayAuto, IdemKey: e.IdemKey, Status: ports.OpDone,
		Interest: decimal.Zero, Principal: decimal.Zero, CreatedAt: e.At, DoneAt: e.At, OrderID: orderOf(e.Memo),
	}
	for _, l := range e.Lines {
		switch {
		case strings.HasSuffix(l.AccountType, "_INTEREST"):
			p.Interest = p.Interest.Add(l.Amount)
		case strings.HasSuffix(l.AccountType, "_DEBT"):
			p.Principal = p.Principal.Add(l.Amount)
		case l.AccountType == string(domain.AccountCross) || l.AccountType == string(domain.AccountIsolated):
			p.UserID, p.Account, p.Asset = l.UserID, domain.Account{Type: domain.AccountType(l.AccountType), Symbol: l.Scope}, l.Asset
		}
	}
	if p.UserID == "" || !p.Interest.Add(p.Principal).IsPositive() {
		s.Log.WarnContext(ctx, "an automatic repayment without its account or amount", "journal_id", e.JournalID)
		return nil
	}
	hash := sha256.Sum256([]byte(e.JournalID))
	p.RequestHash = hash[:]
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, p.UserID); err != nil {
			return err
		}
		if _, ok, err := r.Repays().ByKey(ctx, p.UserID, p.IdemKey); err != nil || ok {
			return err // seen before
		}
		if _, err := r.Accounts().Ensure(ctx, p.UserID, p.Account, e.At); err != nil {
			return err
		}
		if err := r.Repays().Insert(ctx, p); err != nil {
			return err
		}
		if err := r.Loans().Add(ctx, p.UserID, p.Account, p.Asset, p.Principal.Neg(), p.Interest.Neg(), e.At); err != nil {
			return err
		}
		if p.Principal.IsPositive() {
			if _, err := r.Pools().LockLent(ctx, p.Asset); err != nil {
				return err
			}
			if err := r.Pools().AddLent(ctx, p.Asset, p.Principal.Neg()); err != nil {
				return err
			}
		}
		loan, err := r.Loans().Get(ctx, p.UserID, p.Account, p.Asset)
		if err != nil {
			return err
		}
		s.count("auto-repay", "done")
		return r.Emit(ctx, event.TopicMargin, &marginv1.MarginRepaid{
			RepayId: p.ID, UserId: p.UserID, AccountType: string(p.Account.Type), Symbol: p.Account.Symbol, Asset: p.Asset,
			InterestRepaid: p.Interest.String(), PrincipalRepaid: p.Principal.String(), Principal: loan.Principal.String(),
			Interest: loan.Interest.String(), Reason: p.Reason, OrderId: p.OrderID, JournalId: e.JournalID,
			RepaidAt: timestamppb.New(e.At),
		}, "user", p.UserID)
	})
}

// orderOf reads the order of an automatic repayment from its memo
// ("auto-repay order <id> trade <symbol> <id>"); "" when it is not there.
func orderOf(memo string) string {
	f := strings.Fields(memo)
	if len(f) >= 3 && f[0] == "auto-repay" && f[1] == "order" {
		if _, err := uuid.Parse(f[2]); err == nil {
			return f[2]
		}
	}
	return ""
}
