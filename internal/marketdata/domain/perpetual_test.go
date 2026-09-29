package domain

import (
	"errors"
	"testing"
)

func TestIndexIsTheWeightedMedianOfTheSourcesNearIt(t *testing.T) {
	src := func(name, price string, weight int32) SourcePrice {
		return SourcePrice{Source: name, Price: d(price), Weight: weight}
	}
	for _, c := range []struct {
		name     string
		prices   []SourcePrice
		min      int
		want     string
		excluded string
	}{
		{"one source", []SourcePrice{src("a", "60000.5", 1)}, 1, "60000.5", ""},
		{"odd count: the middle", []SourcePrice{src("a", "60010", 1), src("b", "60000", 1), src("c", "60030", 1)}, 2, "60010", ""},
		{"even count: the average of the middle two", []SourcePrice{src("a", "60000", 1), src("b", "60011", 1)}, 2, "60005.5", ""},
		{"weights move the median", []SourcePrice{src("a", "60000", 1), src("b", "60010", 1), src("c", "60020", 3)}, 2, "60020", ""},
		{"a source 3% away is dropped", []SourcePrice{src("a", "60000", 1), src("b", "60010", 1), src("c", "62000", 1)}, 2, "60005", "c"},
		{"exactly 3% away stays", []SourcePrice{src("a", "100", 1), src("b", "100", 1), src("c", "103", 1)}, 2, "100", ""},
		{"eight decimals", []SourcePrice{src("a", "0.123456789", 1)}, 1, "0.12345679", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, comps, err := Index(c.prices, c.min)
			if err != nil || got.String() != c.want {
				t.Fatalf("index %s, %v; want %s", got, err, c.want)
			}
			for _, comp := range comps {
				if comp.Included == (comp.Source == c.excluded) {
					t.Fatalf("component %+v", comp)
				}
			}
		})
	}
}

func TestIndexNeedsItsMinimumOfSources(t *testing.T) {
	prices := []SourcePrice{
		{Source: "a", Price: d("60000"), Weight: 1},
		{Source: "b", Price: d("70000"), Weight: 1}, // one of the two is dropped
		{Source: "c", Price: d("0"), Weight: 1},     // unusable
		{Source: "d", Price: d("60000"), Weight: 0}, // switched off
	}
	if _, _, err := Index(prices[:1], 2); !errors.Is(err, ErrTooFewSources) {
		t.Fatalf("one source of two: %v", err)
	}
	if _, _, err := Index(prices, 2); !errors.Is(err, ErrTooFewSources) {
		t.Fatalf("two sources too far apart: %v", err)
	}
	if _, _, err := Index(nil, 0); !errors.Is(err, ErrTooFewSources) {
		t.Fatalf("no sources: %v", err)
	}
}

func TestTheMarkPriceFollowsTheBookWithinOnePercent(t *testing.T) {
	var b Basis
	if got := Mark(d("60000"), b.EMA); !got.Equal(d("60000")) {
		t.Fatalf("mark before any sample %s", got)
	}
	// The book trades 0.5% above the index: the EMA approaches 0.005.
	sample := BasisSample(d("60000"), d("60290"), d("60310"))
	if !sample.Equal(d("0.005")) {
		t.Fatalf("sample %s", sample)
	}
	b.Add(sample)
	if !b.EMA.Equal(d("0.000322580645")) { // 2/31 of the way
		t.Fatalf("EMA after one sample %s", b.EMA)
	}
	for range 400 {
		b.Add(sample)
	}
	if got := Mark(d("60000"), b.EMA); got.Sub(d("60300")).Abs().GreaterThan(d("0.000001")) {
		t.Fatalf("mark after the EMA settled %s (EMA %s)", got, b.EMA)
	}
	// A book 5% away moves the mark 1% at most, either way.
	if got := Mark(d("60000"), d("0.05")); !got.Equal(d("60600")) {
		t.Fatalf("bounded above %s", got)
	}
	if got := Mark(d("60000"), d("-0.05")); !got.Equal(d("59400")) {
		t.Fatalf("bounded below %s", got)
	}
	if got := BasisSample(d("60000"), d("60290"), d("0")); !got.IsZero() {
		t.Fatalf("a one-sided book samples %s", got)
	}
}

func TestImpactPricesAndThePremium(t *testing.T) {
	bids := []Level{{d("100"), d("50")}, {d("99"), d("100")}}
	asks := []Level{{d("101"), d("10")}, {d("102"), d("100")}}
	// 10000 against the bids: 5000 at 100 (50), 5000 at 99 (50.50505...).
	bid, ok := ImpactPrice(bids, d("10000"))
	if !ok || !bid.Equal(d("99.49748744")) {
		t.Fatalf("impact bid %s %v", bid, ok)
	}
	ask, ok := ImpactPrice(asks, d("1010"))
	if !ok || !ask.Equal(d("101")) {
		t.Fatalf("impact ask within the first level %s %v", ask, ok)
	}
	if _, ok := ImpactPrice(asks, d("20000")); ok {
		t.Fatal("asks holding 11210 cannot fill 20000")
	}
	for _, c := range []struct {
		index, bid, ask string
		bidOK, askOK    bool
		want            string
	}{
		{"100", "99.5", "101", true, true, "0"},           // index inside the impact spread
		{"100", "100.5", "101", true, true, "0.005"},      // bids above the index
		{"100", "99", "99.8", true, true, "-0.002"},       // asks below the index
		{"100", "100.5", "0", true, false, "0.005"},       // thin asks add nothing
		{"100", "0", "0", false, false, "0"},              // an empty book
		{"3", "3.1", "3.2", true, true, "0.033333333333"}, // rounded to 12 decimals
	} {
		got := Premium(d(c.index), d(c.bid), d(c.ask), c.bidOK, c.askOK)
		if got.String() != c.want {
			t.Errorf("premium %+v = %s, want %s", c, got, c.want)
		}
	}
}

func TestFundingRate(t *testing.T) {
	interest, capRate := d("0.0001"), d("0.0075")
	for _, c := range []struct{ premium, want string }{
		{"0", "0.0001"},                  // no premium: the interest rate
		{"0.0003", "0.0001"},             // within 0.05% of the interest: the interest
		{"0.0008", "0.0003"},             // the premium, less the 0.05% bound
		{"-0.0008", "-0.0003"},           // shorts pay longs
		{"0.02", "0.0075"},               // the cap
		{"-0.02", "-0.0075"},             // either way
		{"0.000612345678", "0.00011235"}, // 8 decimals
	} {
		if got := FundingRate(d(c.premium), interest, capRate); got.String() != c.want {
			t.Errorf("premium %s: rate %s, want %s", c.premium, got, c.want)
		}
	}
	if got := AveragePremium(d("0.3"), 4); !got.Equal(d("0.075")) {
		t.Fatalf("average %s", got)
	}
	if got := AveragePremium(d("0"), 0); !got.IsZero() {
		t.Fatalf("average of nothing %s", got)
	}
}

func TestFundingPeriodsAlignToUTC(t *testing.T) {
	for _, c := range []struct {
		at    string
		hours int32
		want  string
	}{
		{"2026-09-30T07:59:59Z", 8, "2026-09-30T08:00:00Z"},
		{"2026-09-30T08:00:00Z", 8, "2026-09-30T16:00:00Z"}, // a boundary starts the next period
		{"2026-09-30T23:10:00Z", 8, "2026-10-01T00:00:00Z"},
		{"2026-09-30T13:10:00Z", 4, "2026-09-30T16:00:00Z"},
		{"2026-09-30T13:10:00Z", 1, "2026-09-30T14:00:00Z"},
	} {
		if got := NextFunding(at(c.at), c.hours); !got.Equal(at(c.want)) {
			t.Errorf("%s every %dh: %s, want %s", c.at, c.hours, got, c.want)
		}
	}
}
