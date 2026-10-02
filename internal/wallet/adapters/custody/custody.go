// Package custody connects wallet-service to the Udun custody wallet
// (ADR-0011) over its gateway protocol (internal/platform/udun).
package custody

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/udun"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

// Udun implements ports.Custody. Its gateway posts the callbacks of the
// addresses and withdrawals created here to CallbackURL.
type Udun struct {
	Client      *udun.Client
	CallbackURL string
	// WalletID picks one of the merchant's wallets; empty for the default.
	WalletID string
}

var _ ports.Custody = (*Udun)(nil)

// Provider is domain.ProviderUdun.
func (u *Udun) Provider() string { return domain.ProviderUdun }

func mainCoin(net domain.Network) (string, string, error) {
	main, coin, err := udun.SplitCoin(net.ProviderCoin)
	if err != nil {
		return "", "", fmt.Errorf("network %s/%s: %w", net.Asset, net.Network, err)
	}
	return main, coin, nil
}

// CreateAddress asks for an address of the network's chain, named after
// the user and the network in the custodian's console.
func (u *Udun) CreateAddress(ctx context.Context, net domain.Network, userID string) (string, error) {
	main, _, err := mainCoin(net)
	if err != nil {
		return "", err
	}
	n, err := strconv.Atoi(main)
	if err != nil {
		return "", err
	}
	a, err := u.Client.CreateAddress(ctx, n, u.CallbackURL, u.WalletID, userID+":"+net.Network)
	if err != nil {
		return "", err
	}
	return a.Address, nil
}

// Submit hands a withdrawal over with its ID as the business ID: a repeat
// is refused as a duplicate, which means the custodian has it. Business
// refusals (codes 4000 to 4999) are for good; anything else is retried.
func (u *Udun) Submit(ctx context.Context, w domain.Withdrawal, net domain.Network) error {
	main, coin, err := mainCoin(net)
	if err != nil {
		return err
	}
	err = u.Client.Withdraw(ctx, udun.Withdrawal{
		Address: w.Address, Amount: w.Amount.String(), MainCoinType: main, CoinType: coin, CallURL: u.CallbackURL, BusinessID: w.ID,
		WalletID: u.WalletID,
	})
	var refusal *udun.Error
	switch {
	case err == nil, udun.IsCode(err, udun.CodeDuplicateBusiness):
		return nil
	case errors.As(err, &refusal) && refusal.Code >= 4000 && refusal.Code < 5000:
		return domain.ErrCustodyRefused.WithDetail("reason", fmt.Sprintf("code %d: %s", refusal.Code, refusal.Message))
	}
	return err
}

// CheckAddress asks the custodian about an address of the network's chain.
func (u *Udun) CheckAddress(ctx context.Context, net domain.Network, address string) (bool, error) {
	main, _, err := mainCoin(net)
	if err != nil {
		return false, err
	}
	return u.Client.CheckAddress(ctx, main, address)
}

// Coins lists the merchant's coins with their balances.
func (u *Udun) Coins(ctx context.Context) ([]ports.CustodyCoin, error) {
	list, err := u.Client.SupportCoins(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make([]ports.CustodyCoin, 0, len(list))
	for _, c := range list {
		coin := ports.CustodyCoin{Code: c.Code(), Symbol: c.Symbol, Token: c.TokenStatus == "1"}
		if d, err := strconv.ParseInt(string(c.Decimals), 10, 32); err == nil {
			coin.Decimals = int32(d)
		}
		// A balance with more decimals than the coin has is not in coins;
		// left out, it fails the check instead of skewing it.
		if b, err := decimal.NewFromString(string(c.Balance)); err == nil && (coin.Decimals == 0 || b.Equal(b.Truncate(coin.Decimals))) {
			coin.Balance = &b
		}
		out = append(out, coin)
	}
	return out, nil
}

// words is what a withdrawal's callback status means.
var words = map[int]string{
	udun.StatusReview:   domain.CustodyReview,
	udun.StatusApproved: domain.CustodyApproved,
	udun.StatusRefused:  domain.CustodyRejected,
	udun.StatusSuccess:  domain.CustodySuccess,
	udun.StatusFailed:   domain.CustodyFailed,
}

// Parse verifies a callback (form or JSON) and reads its trade. A trade
// that does not verify is still returned as far as it reads, for the log.
func (u *Udun) Parse(contentType string, raw []byte, now time.Time, window time.Duration) (ports.CustodyTrade, error) {
	env, err := udun.ParseEnvelope(contentType, raw)
	if err != nil {
		return ports.CustodyTrade{}, domain.ErrCallbackMalformed
	}
	out, terr := tradeOf(env)
	switch verr := env.Verify(u.Client.Key, now, window); {
	case errors.Is(verr, udun.ErrSignature):
		return out, domain.ErrCallbackSignature
	case errors.Is(verr, udun.ErrStale):
		return out, domain.ErrCallbackStale
	case verr != nil:
		return out, domain.ErrCallbackMalformed
	case terr != nil:
		return out, domain.ErrCallbackMalformed.WithDetail("reason", terr.Error())
	}
	return out, nil
}

func tradeOf(env udun.Envelope) (ports.CustodyTrade, error) {
	t, err := udun.ParseTrade(env)
	if err != nil {
		return ports.CustodyTrade{}, err
	}
	out := ports.CustodyTrade{
		TradeID: string(t.TradeID), Status: -1, Coin: t.Coin(), Address: string(t.Address), Memo: string(t.Memo), TxHash: string(t.TxID),
		BusinessID: string(t.BusinessID),
	}
	kind, status, err := t.Kind()
	if err != nil {
		return out, err
	}
	out.Status = status
	switch kind {
	case udun.TradeDeposit:
		out.Kind = domain.CallbackDeposit
		if status == udun.StatusSuccess {
			out.Word = domain.CustodySuccess
		}
	case udun.TradeWithdrawal:
		out.Kind, out.Word = domain.CallbackWithdrawal, words[status]
	default:
		return out, fmt.Errorf("tradeType %d", kind)
	}
	if out.Amount, out.Fee, err = t.Value(); err != nil {
		return out, err
	}
	if d, err := strconv.ParseInt(string(t.Decimals), 10, 32); err == nil {
		out.Decimals = int32(d)
	}
	if t.Amount != "" {
		if out.RawAmount, err = decimal.NewFromString(string(t.Amount)); err != nil {
			return out, fmt.Errorf("amount %q", t.Amount)
		}
	}
	if t.BlockHigh != "" {
		if b, err := strconv.ParseUint(string(t.BlockHigh), 10, 64); err == nil {
			out.Block = b
		}
	}
	return out, nil
}
