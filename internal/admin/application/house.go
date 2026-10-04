package application

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
)

// HouseDeps are what the HOUSE page reads (ADR-0013, ADR-0015); a nil one
// leaves its part out.
type HouseDeps struct {
	// User is HOUSE's account on the contracts (HOUSE_USER_ID).
	User      string
	Prices    ports.MarketPrices
	Trades    ports.HouseTrades
	Positions ports.HousePositions
}

// House is HOUSE's book: its spot inventory valued at the last prices,
// what it traded per pair and the result at those prices, and its
// contract positions.
type House struct {
	Assets    []HouseAsset    `json:"assets"`
	Pairs     []HousePair     `json:"pairs"`
	Contracts json.RawMessage `json:"contracts"`
	Totals    HouseTotals     `json:"totals"`
	// Partial names the parts that could not be read.
	Partial []string `json:"partial"`
}

// HouseAsset is one asset of HOUSE's spot inventory (MARKET_MAKER).
type HouseAsset struct {
	Asset string `json:"asset"`
	// Backed assets have deposits or withdrawals and must be held to be
	// sold; internal ones may go below zero (ADR-0013).
	Backed  bool    `json:"backed"`
	Balance string  `json:"balance"`
	Price   *string `json:"price"`
	Value   *string `json:"value_usdt"`
}

// HousePair is HOUSE's trading on a pair with its result: the base it
// holds from trading valued at the last price, plus the quote it got less
// what it paid.
type HousePair struct {
	ports.HousePair
	NetBase  string  `json:"net_base"`
	NetQuote string  `json:"net_quote"`
	Price    *string `json:"price"`
	PnL      *string `json:"pnl_usdt"`
}

// HouseTotals sum the page in USDT (assets and pairs without a price are
// left out).
type HouseTotals struct {
	Inventory string `json:"inventory_usdt"`
	Backed    string `json:"backed_usdt"`
	// Internal is below zero while HOUSE is short on internal assets.
	Internal string `json:"internal_usdt"`
	PnL      string `json:"pnl_usdt"`
}

const accountMarketMaker = "MARKET_MAKER"

// House reads HOUSE's book; a part that fails is left out and named in
// Partial.
func (s *Service) House(ctx context.Context, p Principal) (House, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return House{}, err
	}
	out := House{Assets: []HouseAsset{}, Pairs: []HousePair{}, Contracts: json.RawMessage("[]"), Partial: []string{}}
	partial := func(part string, err error) {
		s.Log.WarnContext(ctx, "house: part unavailable", "part", part, "error", err)
		out.Partial = append(out.Partial, part)
	}
	prices := ports.Prices{}
	if s.HouseBook.Prices != nil {
		var err error
		if prices, err = s.HouseBook.Prices.Prices(ctx, 0); err != nil {
			partial("prices", err)
			prices = ports.Prices{}
		}
	}
	backed := map[string]bool{}
	if raw, err := s.Catalog.List(ctx); err != nil {
		partial("assets", err)
	} else {
		var cat struct {
			Assets []struct {
				Code     string `json:"asset_code"`
				Deposit  bool   `json:"deposit_enabled"`
				Withdraw bool   `json:"withdraw_enabled"`
			} `json:"assets"`
		}
		if json.Unmarshal(raw, &cat) == nil {
			for _, a := range cat.Assets {
				backed[a.Code] = a.Deposit || a.Withdraw
			}
		}
	}
	usdt := func(asset string) (decimal.Decimal, bool) {
		if asset == "USDT" {
			return decimal.NewFromInt(1), true
		}
		px, ok := prices[asset+"-USDT"]
		return px, ok
	}
	inventory, backedSum, internalSum, pnl := decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero
	if list, err := s.Ledger.SystemBalances(ctx, ""); err != nil {
		partial("inventory", err)
	} else {
		for _, b := range list {
			if b.AccountType != accountMarketMaker {
				continue
			}
			a := HouseAsset{Asset: b.Asset, Backed: backed[b.Asset], Balance: b.Available}
			if px, ok := usdt(b.Asset); ok {
				bal, err := decimal.NewFromString(b.Available)
				if err == nil {
					v := bal.Mul(px).Round(2)
					a.Price, a.Value = strp(px.String()), strp(v.String())
					inventory = inventory.Add(v)
					if a.Backed {
						backedSum = backedSum.Add(v)
					} else {
						internalSum = internalSum.Add(v)
					}
				}
			}
			out.Assets = append(out.Assets, a)
		}
		slices.SortFunc(out.Assets, func(x, y HouseAsset) int {
			if x.Backed != y.Backed { // the backed assets first
				if x.Backed {
					return -1
				}
				return 1
			}
			return strings.Compare(x.Asset, y.Asset)
		})
	}
	if s.HouseBook.Trades != nil {
		if pairs, err := s.HouseBook.Trades.HousePairs(ctx); err != nil {
			partial("trades", err)
		} else {
			for _, hp := range pairs {
				v := housePair(hp, prices)
				if v.PnL != nil {
					pnl = pnl.Add(decimal.RequireFromString(*v.PnL))
				}
				out.Pairs = append(out.Pairs, v)
			}
		}
	}
	if s.HouseBook.Positions != nil && s.HouseBook.User != "" {
		if raw, err := s.HouseBook.Positions.Positions(ctx, s.HouseBook.User); err != nil {
			partial("contracts", err)
		} else {
			out.Contracts = raw
		}
	}
	out.Totals = HouseTotals{Inventory: inventory.String(), Backed: backedSum.String(), Internal: internalSum.String(), PnL: pnl.String()}
	return out, nil
}

// housePair works out a pair's result at its last price: the base HOUSE
// gained (bought − sold) at that price plus the quote it gained (got −
// paid); only USDT pairs are valued.
func housePair(hp ports.HousePair, prices ports.Prices) HousePair {
	dec := func(s string) decimal.Decimal {
		d, err := decimal.NewFromString(s)
		if err != nil {
			return decimal.Zero
		}
		return d
	}
	netBase := dec(hp.BoughtBase).Sub(dec(hp.SoldBase))
	netQuote := dec(hp.GotQuote).Sub(dec(hp.PaidQuote))
	v := HousePair{HousePair: hp, NetBase: netBase.String(), NetQuote: netQuote.String()}
	if px, ok := prices[hp.Symbol]; ok && strings.HasSuffix(hp.Symbol, "-USDT") {
		v.Price = strp(px.String())
		v.PnL = strp(netBase.Mul(px).Add(netQuote).Round(2).String())
	}
	return v
}

func strp(s string) *string { return &s }

// HealthReport is every service's readiness and, with details, the
// reference feed's state.
type HealthReport struct {
	Services []ports.ServiceHealth `json:"services"`
	// Feed is left out without details, or when market-data-service could
	// not be asked.
	Feed *ports.FeedStatus `json:"feed,omitempty"`
}

// Health returns every service's readiness; details adds each service's
// version, Kafka lag and DLQ count, and the reference feed.
func (s *Service) Health(ctx context.Context, p Principal, details bool) (HealthReport, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return HealthReport{}, err
	}
	out := HealthReport{Services: []ports.ServiceHealth{}}
	if s.Probe != nil {
		out.Services = s.Probe.Check(ctx, details)
	}
	if details && s.Market != nil {
		if feed, err := s.Market.Feed(ctx); err != nil {
			s.Log.WarnContext(ctx, "health: feed status unavailable", "error", err)
		} else {
			out.Feed = &feed
		}
	}
	return out, nil
}

// Reconciliation returns the ledger's latest invariant checks and the
// recent runs with mismatches.
func (s *Service) Reconciliation(ctx context.Context, p Principal) (ports.Reconciliation, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return ports.Reconciliation{}, err
	}
	return s.Reconciler.Reconciliation(ctx, 50)
}

// SystemBalances returns the platform's system accounts in an asset, of
// every asset when it is "".
func (s *Service) SystemBalances(ctx context.Context, p Principal, asset string) ([]ports.Balance, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	list, err := s.Ledger.SystemBalances(ctx, strings.ToUpper(strings.TrimSpace(asset)))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(list, func(a, b ports.Balance) int {
		if c := strings.Compare(a.AccountType, b.AccountType); c != 0 {
			return c
		}
		return strings.Compare(a.Asset, b.Asset)
	})
	return list, nil
}
