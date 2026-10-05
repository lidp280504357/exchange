package domain

import "testing"

func TestParseAccount(t *testing.T) {
	if a, err := ParseAccount("MARGIN_CROSS", ""); err != nil || !a.IsCross() || a.Key() != "MARGIN_CROSS" {
		t.Fatalf("cross: %+v %v", a, err)
	}
	if a, err := ParseAccount("MARGIN_ISOLATED", " btc-usdt "); err != nil || a.IsCross() || a.Key() != "MARGIN_ISOLATED:BTC-USDT" {
		t.Fatalf("isolated: %+v %v", a, err)
	}
	for _, c := range [][2]string{
		{"MARGIN_CROSS", "BTC-USDT"},
		{"MARGIN_ISOLATED", ""},
		{"MARGIN_ISOLATED", "BTCUSDT"},
		{"MARGIN_ISOLATED", "BTC-USDT-PERP"},
		{"SPOT", ""},
	} {
		if a, err := ParseAccount(c[0], c[1]); err == nil {
			t.Errorf("%s %q: %+v", c[0], c[1], a)
		}
	}
}

func TestHolding(t *testing.T) {
	h := Holding{Asset: "USDT", Free: d("90"), Locked: d("10"), Borrowed: d("50"), Interest: d("0.5")}
	if !h.Total().Equal(d("100")) || !h.Debt().Equal(d("50.5")) || !h.Net().Equal(d("49.5")) || h.Empty() {
		t.Fatalf("%+v", h)
	}
	if !(Holding{Asset: "BTC"}).Empty() {
		t.Fatal("an empty holding")
	}
}

func TestMayHold(t *testing.T) {
	btc := testTerms["BTC"]
	pair := &Pair{Symbol: "BTC-USDT", Base: "BTC", Quote: "USDT", Isolated: true}
	if !MayHold(Cross(), nil, btc, true) || MayHold(Cross(), nil, btc, false) {
		t.Error("cross: listed assets only")
	}
	if !MayHold(Isolated("BTC-USDT"), pair, btc, true) || MayHold(Isolated("BTC-USDT"), pair, testTerms["ETH"], true) {
		t.Error("isolated: the pair's two assets only")
	}
	closed := *pair
	closed.Isolated = false
	if MayHold(Isolated("BTC-USDT"), &closed, btc, true) || MayHold(Isolated("ETH-USDT"), pair, btc, true) {
		t.Error("isolated: an open pair, its own")
	}
}
