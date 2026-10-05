package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestMaxBorrow(t *testing.T) {
	cross := DefaultTerms(AccountCross, 3)
	own := Valuation{TotalAsset: d("1000"), TotalLiability: decimal.Zero}
	usdt := testTerms["USDT"]
	for name, c := range map[string]struct {
		room  BorrowRoom
		want  string
		limit Limit
	}{
		// 1000 of net assets at 3x: 2000 more; the level is then 1.5.
		"cross USDT": {BorrowRoom{Valuation: own, Terms: cross, Asset: usdt, Price: d("1"), PoolLeft: d("2000000")}, "2000", LimitLeverage},
		// 10x isolated borrows 9 times the net assets, the level then
		// 10/9, above the warning level 1.10.
		"isolated 10x": {BorrowRoom{
			Valuation: own, Terms: DefaultTerms(AccountIsolated, 10), Asset: usdt, Price: d("1"),
			PoolLeft: d("2000000"),
		}, "9000", LimitLeverage},
		"cross 5x": {BorrowRoom{
			Valuation: own, Terms: DefaultTerms(AccountCross, 5), Asset: usdt, Price: d("1"),
			PoolLeft: d("2000000"),
		}, "4000", LimitLeverage},
		// A warning level above L / (L - 1) (1.15 at 10x, the first draft's)
		// stops the loan before the leverage: at 1000 / 0.15.
		"warning above the leverage's level": {BorrowRoom{Valuation: own, Terms: Terms{
			Leverage: 10, WarnLevel: d("1.15"),
			LiquidationLevel: d("1.05"),
		}, Asset: usdt, Price: d("1"), PoolLeft: d("2000000")}, "6666.666666", LimitLevel},
		// BTC counts at 0.95: x <= 2000 / (30000 x (1 + 0.05 x 2)).
		"cross BTC": {
			BorrowRoom{Valuation: own, Terms: cross, Asset: testTerms["BTC"], Price: d("30000"), PoolLeft: d("20")},
			"0.06060606", LimitLeverage,
		},
		"pool": {BorrowRoom{Valuation: own, Terms: cross, Asset: usdt, Price: d("1"), PoolLeft: d("500")}, "500", LimitPool},
		"user cap": {BorrowRoom{
			Valuation: own, Terms: cross, Asset: usdt, Price: d("1"), PoolLeft: d("2000000"),
			UserOwed: d("199900"),
		}, "100", LimitUserCap},
		"already beyond": {BorrowRoom{
			Valuation: Valuation{TotalAsset: d("2500"), TotalLiability: d("2000")}, Terms: cross,
			Asset: usdt, Price: d("1"), PoolLeft: d("2000000"),
		}, "0", LimitLeverage},
		"not borrowable": {BorrowRoom{Valuation: own, Terms: cross, Asset: AssetTerms{
			Asset: "ASTRA", Collateral: true,
			Haircut: d("0.7"),
		}, Price: d("1"), PoolLeft: d("100")}, "0", LimitLeverage},
		"no price": {BorrowRoom{Valuation: own, Terms: cross, Asset: usdt, PoolLeft: d("100")}, "0", LimitLeverage},
	} {
		got, limit := MaxBorrow(c.room)
		if !got.Equal(d(c.want)) || limit != c.limit {
			t.Errorf("%s: %s %s, want %s %s", name, got, limit, c.want, c.limit)
		}
	}

	// Borrowing the whole room lands at the bound: 3x leaves the level at
	// 1.5 and the liabilities at twice the net assets.
	x, _ := MaxBorrow(BorrowRoom{Valuation: own, Terms: cross, Asset: testTerms["BTC"], Price: d("30000"), PoolLeft: d("20")})
	after := Valuation{TotalAsset: d("1000").Add(x.Mul(d("30000")).Mul(d("0.95"))), TotalLiability: x.Mul(d("30000"))}
	if limit := after.Net().Mul(d("2")); after.TotalLiability.GreaterThan(limit) {
		t.Errorf("liabilities %s above 2 x net %s", after.TotalLiability, after.Net())
	}
	if level, _ := after.Level(); level.LessThan(cross.WarnLevel) {
		t.Errorf("level %s under the warning level", level)
	}
}

func TestMaxTransferOut(t *testing.T) {
	cross := DefaultTerms(AccountCross, 3)
	none := Valuation{TotalAsset: d("1000"), TotalLiability: decimal.Zero}
	if got := MaxTransferOut(Holding{Asset: "USDT", Free: d("1000")}, none, cross, d("1"), d("1"), 6); !got.Equal(d("1000")) {
		t.Errorf("no debt: %s", got)
	}
	if got := MaxTransferOut(Holding{Asset: "USDT", Free: d("500"), Locked: d("500")}, none, cross, d("1"), d("1"), 6); !got.Equal(d("500")) {
		t.Errorf("orders hold half: %s", got)
	}
	// 1000 own and 2000 borrowed: the borrowed 2000 stay, and of the rest
	// only what keeps 3000 - x >= 1.3 x 2000.
	h := Holding{Asset: "USDT", Free: d("3000"), Borrowed: d("2000")}
	v := Valuation{TotalAsset: d("3000"), TotalLiability: d("2000")}
	if got := MaxTransferOut(h, v, cross, d("1"), d("1"), 6); !got.Equal(d("400")) {
		t.Errorf("with a debt: %s", got)
	}
	// BTC held beside a USDT debt: (A - w·D) / (h·p) = (5850 - 2600) / 28500.
	btc := Holding{Asset: "BTC", Free: d("0.2")}
	v = Valuation{TotalAsset: d("5850"), TotalLiability: d("2000")}
	if got := MaxTransferOut(btc, v, cross, d("30000"), d("0.95"), 8); !got.Equal(d("0.11403508")) {
		t.Errorf("BTC beside a debt: %s", got)
	}
	// Under the warning level nothing leaves.
	v = Valuation{TotalAsset: d("2400"), TotalLiability: d("2000")}
	if got := MaxTransferOut(h, v, cross, d("1"), d("1"), 6); !got.IsZero() {
		t.Errorf("under the warning level: %s", got)
	}
}

func TestRepayInterestFirst(t *testing.T) {
	h := Holding{Asset: "USDT", Free: d("50"), Borrowed: d("100"), Interest: d("3")}
	r, err := Repay(d("10"), h)
	if err != nil || !r.Interest.Equal(d("3")) || !r.Principal.Equal(d("7")) || !r.Total().Equal(d("10")) {
		t.Fatalf("repay 10: %+v %v", r, err)
	}
	if r, err = Repay(d("2"), h); err != nil || !r.Interest.Equal(d("2")) || !r.Principal.IsZero() {
		t.Fatalf("repay 2: %+v %v", r, err)
	}
	if _, err = Repay(d("103.000001"), h); err == nil {
		t.Fatal("repaid more than the debt")
	}
	if _, err = Repay(d("0"), h); err == nil {
		t.Fatal("repaid nothing")
	}
	if r = RepayAll(h); !r.Interest.Equal(d("3")) || !r.Principal.Equal(d("47")) {
		t.Fatalf("all, as far as 50 goes: %+v", r)
	}
	h.Free = d("500")
	if r = RepayAll(h); !r.Total().Equal(d("103")) {
		t.Fatalf("all: %+v", r)
	}
	if r = RepayAll(Holding{Asset: "USDT", Borrowed: d("5")}); !r.Total().IsZero() {
		t.Fatalf("all without funds: %+v", r)
	}
}

func TestIsolatedLiquidationPrice(t *testing.T) {
	level := d("1.15")
	// Long: 0.1 BTC held against 2000 USDT owed: 0.95 x 0.1 x p = 1.15 x 2000.
	long, _ := LiquidationPrice(Holding{Asset: "BTC", Free: d("0.1")}, Holding{Asset: "USDT", Borrowed: d("2000")}, d("0.95"), d("1"), level, 2)
	if !long.Equal(d("24210.53")) {
		t.Errorf("long: %s", long)
	}
	// Short: 1 BTC owed, 40000 USDT held: 40000 = 1.15 x p.
	short, _ := LiquidationPrice(Holding{Asset: "BTC", Borrowed: d("1")}, Holding{Asset: "USDT", Free: d("40000")}, d("0.95"), d("1"), level, 2)
	if !short.Equal(d("34782.61")) {
		t.Errorf("short: %s", short)
	}
	// Enough USDT to cover the debt at any price, and no debt at all.
	if p, ok := LiquidationPrice(Holding{Asset: "BTC", Free: d("0.1")}, Holding{Asset: "USDT", Free: d("5000"), Borrowed: d("2000")},
		d("0.95"), d("1"), level, 2); ok {
		t.Errorf("covered long: %s", p)
	}
	if _, ok := LiquidationPrice(Holding{Asset: "BTC", Free: d("1")}, Holding{Asset: "USDT"}, d("0.95"), d("1"), level, 2); ok {
		t.Error("no debt")
	}
}
