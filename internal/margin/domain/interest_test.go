package domain

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestTheFloatingCurve(t *testing.T) {
	f := DefaultFloating()
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ use, want string }{
		{"-0.5", "0.000005"}, // clamped
		{"0", "0.000005"},
		{"0.4", "0.0000175"}, // halfway up the gentle slope
		{"0.8", "0.00003"},   // the kink
		{"0.9", "0.000065"},  // halfway up the steep one
		{"1", "0.0001"},
		{"3", "0.0001"},                          // clamped
		{"0.3333333333333333", "0.000015416667"}, // rounded to RateDecimals
	} {
		if got := f.Rate(d(c.use)); !got.Equal(d(c.want)) {
			t.Errorf("use %s: rate %s, want %s", c.use, got, c.want)
		}
	}
	for _, bad := range []FloatingRate{
		{Base: d("-0.1"), Kink: d("0.8"), KinkRate: d("0.1"), MaxRate: d("0.2")},
		{Base: d("0.1"), Kink: d("1"), KinkRate: d("0.1"), MaxRate: d("0.2")},
		{Base: d("0.1"), Kink: d("0"), KinkRate: d("0.1"), MaxRate: d("0.2")},
		{Base: d("0.2"), Kink: d("0.8"), KinkRate: d("0.1"), MaxRate: d("0.3")},
		{Base: d("0.1"), Kink: d("0.8"), KinkRate: d("0.3"), MaxRate: d("0.2")},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v passed", bad)
		}
	}
}

func TestUse(t *testing.T) {
	for _, c := range []struct{ lent, cap, want string }{
		{"50", "200", "0.25"},
		{"300", "200", "1"},
		{"-5", "200", "0"},
		{"1", "0", "1"}, // no pool: used up
		{"0", "3", "0"},
	} {
		if got := Use(d(c.lent), d(c.cap)); !got.Equal(d(c.want)) {
			t.Errorf("Use(%s, %s) = %s, want %s", c.lent, c.cap, got, c.want)
		}
	}
}

func TestHourlyRateByModel(t *testing.T) {
	fixed := AssetTerms{Asset: "USDT", Model: InterestFixed, FixedRate: d("0.00001"), PoolCap: d("2000000"), Floating: DefaultFloating()}
	if got := fixed.HourlyRate(d("1900000")); !got.Equal(d("0.00001")) {
		t.Errorf("fixed rate %s", got)
	}
	floating := fixed
	floating.Model = InterestFloating
	if got := floating.HourlyRate(d("1600000")); !got.Equal(d("0.00003")) {
		t.Errorf("floating rate at 80%% use %s", got)
	}
}

func TestAssetTermsValidate(t *testing.T) {
	ok := AssetTerms{
		Asset: "BTC", Decimals: 8, Borrowable: true, Collateral: true, Haircut: d("0.95"), PoolCap: d("20"), UserCap: d("2"),
		Model: InterestFixed, FixedRate: d("0.000005"),
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*AssetTerms){
		"no asset":         func(a *AssetTerms) { a.Asset = "" },
		"zero haircut":     func(a *AssetTerms) { a.Haircut = decimal.Zero },
		"haircut above 1":  func(a *AssetTerms) { a.Haircut = d("1.01") },
		"user above pool":  func(a *AssetTerms) { a.UserCap = d("21") },
		"negative pool":    func(a *AssetTerms) { a.PoolCap = d("-1") },
		"unknown model":    func(a *AssetTerms) { a.Model = "STEP" },
		"negative fixed":   func(a *AssetTerms) { a.FixedRate = d("-0.1") },
		"bad float curve":  func(a *AssetTerms) { a.Model, a.Floating = InterestFloating, FloatingRate{} },
		"too many decimal": func(a *AssetTerms) { a.Decimals = 19 },
	} {
		a := ok
		change(&a)
		if a.Validate() == nil {
			t.Errorf("%s passed", name)
		}
	}
}

func TestInterestRoundsUpAndNeverCompounds(t *testing.T) {
	for _, c := range []struct {
		principal, rate string
		decimals        int32
		want            string
	}{
		{"1000", "0.00001", 8, "0.01"},
		{"0.12345678", "0.000005", 8, "0.00000062"}, // 0.0000006172839 up
		{"1", "0.00001", 2, "0.01"},                 // a charge never rounds to nothing
		{"0", "0.00001", 8, "0"},
		{"5", "0", 8, "0"},
	} {
		if got := Interest(d(c.principal), d(c.rate), c.decimals); !got.Equal(d(c.want)) {
			t.Errorf("Interest(%s, %s, %d) = %s, want %s", c.principal, c.rate, c.decimals, got, c.want)
		}
	}
}

func TestPrincipalOnTheHour(t *testing.T) {
	// 150 owed now, 50 of it borrowed after the hour: 100 was owed on it.
	if got := PrincipalAt(d("150"), d("50"), d("0")); !got.Equal(d("100")) {
		t.Errorf("borrowed since: %s", got)
	}
	// 80 owed now, 20 repaid after the hour: 100 was owed on it.
	if got := PrincipalAt(d("80"), d("0"), d("20")); !got.Equal(d("100")) {
		t.Errorf("repaid since: %s", got)
	}
	if got := PrincipalAt(d("10"), d("50"), d("0")); !got.IsZero() {
		t.Errorf("clamped: %s", got)
	}
	at := time.Date(2026, 10, 6, 10, 59, 59, 0, time.FixedZone("UTC+8", 8*3600))
	if got := Hour(at); !got.Equal(time.Date(2026, 10, 6, 2, 0, 0, 0, time.UTC)) || got.Location() != time.UTC {
		t.Errorf("Hour(%s) = %s", at, got)
	}
}
