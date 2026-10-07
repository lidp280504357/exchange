//go:build ignore

// gen-contracts lists the perpetuals of the coin-margined design
// (docs/设计-币本位永续与币安合约数据-2026-10-06.md §3.4, batch G1c) in
// deploy/instruments/test.json: for every coin the file trades against
// USDT, Binance's USDⓈ-M perpetual as <BASE>-USDT-PERP and its COIN-M
// perpetual as <BASE>-USD-PERP, each following its Binance contract
// (reference_symbol). It reads Binance's contract rules and the risk
// brackets its site publishes once, at generation time, and keeps
// everything else in the file:
//
//	go run deploy/instruments/gen-contracts.go [-file deploy/instruments/test.json] [-snapshot deploy/instruments/binance-contracts.json] [-offline] [-status PREPARE|TRADING] [-fapi https://fapi.binance.com] [-dapi https://dapi.binance.com] [-web https://www.binance.com]
//
// A run keeps what it read in -snapshot: the entries of the symbols the
// file's coins could list, as Binance wrote them, one a line; -offline
// replays the snapshot instead of reading Binance, to check a listing
// against its inputs or to regenerate it under changed rules (review EW,
// B126).
//
// Rules:
//   - a coin's contracts are the perpetuals trading on <BASE>USDT (USDⓈ-M;
//     a 1000x coin's base is its 1000 code, as our pair's) and
//     <BASE>USD_PERP (COIN-M); a coin Binance has none of gets none, and
//     the platform coin's are its own (ASTRA, listed by hand);
//   - the tick is Binance's, at least USDT's 0.000001; the lot Binance's
//     step (a COIN-M lot is one contract), coarsened until tick x lot has
//     at most USDT's 6 decimals (the instruments' rule); the minimum
//     quantity the larger of Binance's and the lot; the most per order
//     MARKET_LOT_SIZE's (here it bounds limit orders too); the minimum
//     notional 5 USDT, one contract's face value on COIN-M; the price band
//     PERCENT_PRICE's (at most 15%);
//   - the risk tiers are Binance's brackets as its site publishes them
//     (bapi/futures/v1/friendly/{future,delivery}/common/brackets, no
//     signature): each bracket's notional cap (USDT; the coin on COIN-M),
//     its top leverage and maintenance rate;
//   - the impact notional is 10,000 USD: 10,000 USDT, or 10,000 / the face
//     value in contracts (as BTC's and ETH's);
//   - funding as Binance's fundingInfo has it: its interval (4 or 1 hours,
//     8 on the platform's grid without an entry), interest 0.03% a day
//     (0.01% per 8 hours, per interval), its adjusted cap (0.75% without
//     an entry); the perp fee tier;
//   - new contracts are listed PREPARE (-status TRADING lists them open;
//     apply never changes a listed contract's status): they open in
//     batches once HOUSE is seeded for them and the reference streams
//     carry them (the contract backend's part of G1c, instruments
//     runbook); a listed contract keeps its whole entry (operators may
//     have changed it in the console) but its funding interval, interest
//     and cap, which follow Binance's fundingInfo as above while it lists
//     the contract (one it no longer lists keeps them): a contract
//     takes Binance's funding rate only for periods that end when
//     Binance's do (market-data runbook; Binance moved 23 of the coins'
//     USDⓈ-M perpetuals to 4 hours). The console's changes still win at
//     apply.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// quoteDecimals are USDT's: a contract's prices are in USDT (a COIN-M
// contract's in USD, priced as USDT).
const quoteDecimals = 6

type filter struct {
	FilterType     string `json:"filterType"`
	TickSize       string `json:"tickSize"`
	StepSize       string `json:"stepSize"`
	MinQty         string `json:"minQty"`
	MaxQty         string `json:"maxQty"`
	MultiplierUp   string `json:"multiplierUp"`
	MultiplierDown string `json:"multiplierDown"`
}

// remote is one of Binance's perpetuals.
type remote struct {
	Symbol         string   `json:"symbol"`
	BaseAsset      string   `json:"baseAsset"`
	QuoteAsset     string   `json:"quoteAsset"`
	MarginAsset    string   `json:"marginAsset"`
	ContractType   string   `json:"contractType"`
	Status         string   `json:"status"`         // USDⓈ-M
	ContractStatus string   `json:"contractStatus"` // COIN-M
	ContractSize   float64  `json:"contractSize"`   // COIN-M, USD
	Filters        []filter `json:"filters"`
}

func (r remote) filter(kind string) filter {
	for _, f := range r.Filters {
		if f.FilterType == kind {
			return f
		}
	}
	return filter{}
}

type bracket struct {
	NotionalCap float64 `json:"bracketNotionalCap"`
	MMR         float64 `json:"bracketMaintenanceMarginRate"`
	MaxLeverage int     `json:"maxOpenPosLeverage"`
}

// fundingRule is a perpetual's funding on Binance (fundingInfo): its
// interval and the cap of its rate (the floor is its negative).
type fundingRule struct {
	Hours int32
	Cap   decimal.Decimal
}

// defaultFunding is a perpetual's funding without an entry of its own.
var defaultFunding = fundingRule{Hours: 8, Cap: decimal.RequireFromString("0.0075")}

// interest is the interest rate of a funding interval: 0.03% a day.
func (f fundingRule) interest() decimal.Decimal {
	return decimal.RequireFromString("0.0003").Mul(decimal.NewFromInt32(f.Hours)).Div(decimal.NewFromInt(24))
}

// snapshot is what a run reads from Binance, kept to the symbols the
// file's coins could list: the entries of the two exchangeInfo answers, of
// the risk brackets of the site and of the two fundingInfo answers, each
// as Binance wrote it.
type snapshot struct {
	TakenAt         string            `json:"taken_at"`
	Linear          []json.RawMessage `json:"fapi_exchange_info"`
	Inverse         []json.RawMessage `json:"dapi_exchange_info"`
	LinearBrackets  []json.RawMessage `json:"future_brackets"`
	InverseBrackets []json.RawMessage `json:"delivery_brackets"`
	LinearFunding   []json.RawMessage `json:"fapi_funding_info"`
	InverseFunding  []json.RawMessage `json:"dapi_funding_info"`
}

func get(url string, out any) error {
	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// exchangeInfo is the entries of a market's exchangeInfo, one a symbol.
func exchangeInfo(url string) []json.RawMessage {
	var info struct {
		Symbols []json.RawMessage `json:"symbols"`
	}
	if err := get(url, &info); err != nil {
		log.Fatalf("exchange info: %v", err)
	}
	if len(info.Symbols) == 0 {
		log.Fatalf("exchange info %s: no symbols", url)
	}
	return info.Symbols
}

// siteBrackets is the entries of the risk brackets Binance's site
// publishes for a market, one a symbol. Its answers carry their own code:
// a refusal is an HTTP 200 without brackets, which would skip every
// contract quietly (review EW, B126).
func siteBrackets(url string) []json.RawMessage {
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Brackets []json.RawMessage `json:"brackets"`
		} `json:"data"`
	}
	if err := get(url, &body); err != nil {
		log.Fatalf("brackets: %v", err)
	}
	if body.Code != "000000" {
		log.Fatalf("brackets %s: code %q: %s", url, body.Code, body.Message)
	}
	if len(body.Data.Brackets) == 0 {
		log.Fatalf("brackets %s: none", url)
	}
	return body.Data.Brackets
}

// fundingInfo is the entries of a market's fundingInfo: one a perpetual
// whose funding Binance adjusted. USDⓈ-M's lists the COIN-M perpetuals
// too (BTCUSD_PERP's cap 0.3%, every 8 hours); COIN-M's is empty today.
func fundingInfo(url string) []json.RawMessage {
	var entries []json.RawMessage
	if err := get(url, &entries); err != nil {
		log.Fatalf("funding info: %v", err)
	}
	return entries
}

// fundingRules are the funding rules among a market's fundingInfo
// entries, by symbol. One the instruments cannot take (an interval other
// than 1, 4 or 8 hours, a cap past 5%) is left out, logged: its contract
// funds by the default rule and does not take Binance's rate.
func fundingRules(entries []json.RawMessage) map[string]fundingRule {
	out := map[string]fundingRule{}
	for _, e := range entries {
		var f struct {
			Symbol string `json:"symbol"`
			Cap    string `json:"adjustedFundingRateCap"`
			Floor  string `json:"adjustedFundingRateFloor"`
			Hours  int32  `json:"fundingIntervalHours"`
		}
		if err := json.Unmarshal(e, &f); err != nil {
			log.Fatalf("funding info %s: %v", symbolOf(e), err)
		}
		limit, err1 := decimal.NewFromString(f.Cap)
		floor, err2 := decimal.NewFromString(f.Floor)
		if err1 != nil || err2 != nil {
			log.Fatalf("funding info %s: cap %q, floor %q", f.Symbol, f.Cap, f.Floor)
		}
		// One cap bounds both ways: the wider side, so Binance's rate is
		// never cut.
		limit = decimal.Max(limit, floor.Neg())
		switch {
		case !slices.Contains([]int32{1, 4, 8}, f.Hours):
			log.Printf("funding of %s: every %d hours, which the instruments do not take: the default rule", f.Symbol, f.Hours)
		case !limit.IsPositive() || limit.GreaterThan(decimal.RequireFromString("0.05")):
			log.Printf("funding of %s: a cap of %s, which the instruments do not take: the default rule", f.Symbol, limit)
		default:
			out[f.Symbol] = fundingRule{Hours: f.Hours, Cap: limit}
		}
	}
	return out
}

// symbolOf is the symbol an entry of Binance's is about.
func symbolOf(entry json.RawMessage) string {
	var v struct {
		Symbol string `json:"symbol"`
	}
	if err := json.Unmarshal(entry, &v); err != nil || v.Symbol == "" {
		log.Fatalf("an entry without a symbol: %.80s", entry)
	}
	return v.Symbol
}

// keep is the entries about symbols, compacted and sorted by symbol.
func keep(entries []json.RawMessage, symbols map[string]bool) []json.RawMessage {
	var out []json.RawMessage
	for _, e := range entries {
		if !symbols[symbolOf(e)] {
			continue
		}
		var b bytes.Buffer
		if err := json.Compact(&b, e); err != nil {
			log.Fatal(err)
		}
		out = append(out, b.Bytes())
	}
	slices.SortFunc(out, func(a, b json.RawMessage) int { return strings.Compare(symbolOf(a), symbolOf(b)) })
	return out
}

// perpetuals are the trading perpetuals among a market's entries, by
// symbol.
func perpetuals(entries []json.RawMessage) map[string]remote {
	out := map[string]remote{}
	for _, e := range entries {
		var s remote
		if err := json.Unmarshal(e, &s); err != nil {
			log.Fatalf("exchange info %s: %v", symbolOf(e), err)
		}
		if s.ContractType == "PERPETUAL" && (s.Status == "TRADING" || s.ContractStatus == "TRADING") {
			out[s.Symbol] = s
		}
	}
	return out
}

// brackets are the risk brackets among the site's entries, by symbol.
func brackets(entries []json.RawMessage) map[string][]bracket {
	out := map[string][]bracket{}
	for _, e := range entries {
		var b struct {
			Symbol       string    `json:"symbol"`
			RiskBrackets []bracket `json:"riskBrackets"`
		}
		if err := json.Unmarshal(e, &b); err != nil {
			log.Fatalf("brackets %s: %v", symbolOf(e), err)
		}
		out[b.Symbol] = b.RiskBrackets
	}
	return out
}

// writeSnapshot keeps s at path, an entry a line.
func writeSnapshot(path string, s snapshot) {
	lines := func(in []json.RawMessage) []string {
		out := make([]string, len(in))
		for i, e := range in {
			out[i] = string(e)
		}
		return out
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "{\n  \"taken_at\": %q,\n", s.TakenAt)
	writeList(&buf, "fapi_exchange_info", lines(s.Linear), false)
	writeList(&buf, "dapi_exchange_info", lines(s.Inverse), false)
	writeList(&buf, "future_brackets", lines(s.LinearBrackets), false)
	writeList(&buf, "delivery_brackets", lines(s.InverseBrackets), false)
	writeList(&buf, "fapi_funding_info", lines(s.LinearFunding), false)
	writeList(&buf, "dapi_funding_info", lines(s.InverseFunding), true)
	buf.WriteString("}\n")
	if !json.Valid(buf.Bytes()) {
		log.Fatalf("the snapshot does not parse")
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}

func main() {
	file := flag.String("file", "deploy/instruments/test.json", "reference data file to update")
	fapi := flag.String("fapi", "https://fapi.binance.com", "Binance USDⓈ-M REST base URL")
	dapi := flag.String("dapi", "https://dapi.binance.com", "Binance COIN-M REST base URL")
	web := flag.String("web", "https://www.binance.com", "Binance's site, for the public risk brackets")
	snap := flag.String("snapshot", "deploy/instruments/binance-contracts.json", "what the run read from Binance (written, or read with -offline)")
	offline := flag.Bool("offline", false, "replay -snapshot instead of reading Binance")
	status := flag.String("status", "PREPARE", "status of the contracts new to the file: PREPARE, or TRADING to list them open")
	flag.Parse()
	if *status != "PREPARE" && *status != "TRADING" {
		log.Fatalf("-status %s: PREPARE or TRADING", *status)
	}

	raw, err := os.ReadFile(*file)
	if err != nil {
		log.Fatal(err)
	}
	var doc struct {
		FeeSchedules []json.RawMessage `json:"fee_schedules"`
		Assets       []map[string]any  `json:"assets"`
		Pairs        []map[string]any  `json:"pairs"`
		Contracts    []map[string]any  `json:"contracts"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		log.Fatal(err)
	}
	decimalsOf := map[string]int{}
	for _, a := range doc.Assets {
		decimalsOf[a["asset_code"].(string)] = int(a["decimals"].(float64))
	}
	listed := map[string]bool{}
	for _, c := range doc.Contracts {
		listed[c["symbol"].(string)] = true
	}

	// The symbols the file's coins could list (the rules above).
	wanted := map[string]bool{}
	for _, p := range doc.Pairs {
		base, quote := p["base_asset"].(string), p["quote_asset"].(string)
		if quote != "USDT" || base == "ASTRA" {
			continue
		}
		wanted[base+"USDT"] = true
		if !strings.HasPrefix(base, "1000") {
			wanted[base+"USD_PERP"] = true
		}
	}
	var in snapshot
	if *offline {
		b, err := os.ReadFile(*snap)
		if err != nil {
			log.Fatal(err)
		}
		if err := json.Unmarshal(b, &in); err != nil {
			log.Fatalf("%s: %v", *snap, err)
		}
		log.Printf("replaying %s, taken at %s", *snap, in.TakenAt)
	} else {
		in = snapshot{
			TakenAt:         time.Now().UTC().Format(time.RFC3339),
			Linear:          keep(exchangeInfo(*fapi+"/fapi/v1/exchangeInfo"), wanted),
			Inverse:         keep(exchangeInfo(*dapi+"/dapi/v1/exchangeInfo"), wanted),
			LinearBrackets:  keep(siteBrackets(*web+"/bapi/futures/v1/friendly/future/common/brackets"), wanted),
			InverseBrackets: keep(siteBrackets(*web+"/bapi/futures/v1/friendly/delivery/common/brackets"), wanted),
			LinearFunding:   keep(fundingInfo(*fapi+"/fapi/v1/fundingInfo"), wanted),
			InverseFunding:  keep(fundingInfo(*dapi+"/dapi/v1/fundingInfo"), wanted),
		}
		writeSnapshot(*snap, in)
	}
	linear, inverse := perpetuals(in.Linear), perpetuals(in.Inverse)
	linearTiers, inverseTiers := brackets(in.LinearBrackets), brackets(in.InverseBrackets)
	funding := fundingRules(in.LinearFunding)
	maps.Copy(funding, fundingRules(in.InverseFunding))
	fundingOf := func(symbol string) fundingRule {
		if f, ok := funding[symbol]; ok {
			return f
		}
		return defaultFunding
	}

	// A listed contract's funding follows Binance's (the rules above),
	// while Binance lists it: one its fundingInfo does not (gone from
	// Binance, or a snapshot from before funding was read) keeps what the
	// file has, not the default (review B142).
	contracts := slices.Clone(doc.Contracts)
	refreshed := 0
	for _, c := range contracts {
		ref, _ := c["reference_symbol"].(string)
		if ref == "" {
			continue
		}
		f, ok := funding[ref]
		if !ok {
			if len(in.LinearFunding)+len(in.InverseFunding) > 0 {
				log.Printf("%-18s Binance's fundingInfo has no %s: its funding kept", c["symbol"], ref)
			}
			continue
		}
		hours, interest, limit := float64(f.Hours), f.interest().String(), f.Cap.String()
		if c["funding_interval_hours"] == hours && c["interest_rate"] == interest && c["funding_cap"] == limit {
			continue
		}
		log.Printf("%-18s funding every %v hours, interest %v, cap %v -> every %d hours, interest %s, cap %s",
			c["symbol"], c["funding_interval_hours"], c["interest_rate"], c["funding_cap"], f.Hours, interest, limit)
		c["funding_interval_hours"], c["interest_rate"], c["funding_cap"] = hours, interest, limit
		refreshed++
	}
	added := 0
	for _, p := range doc.Pairs {
		base, quote := p["base_asset"].(string), p["quote_asset"].(string)
		if quote != "USDT" || base == "ASTRA" {
			continue
		}
		if r, ok := linear[base+"USDT"]; ok && r.QuoteAsset == "USDT" && r.MarginAsset == "USDT" {
			if c := contract(r, base, decimal.Zero, decimalsOf[base], linearTiers[r.Symbol], fundingOf(r.Symbol)); c != nil && !listed[c["symbol"].(string)] {
				c["status"] = *status
				contracts, added = append(contracts, c), added+1
			}
		} else {
			log.Printf("skip %s-USDT-PERP: Binance has no USDⓈ-M perpetual %sUSDT", base, base)
		}
		if strings.HasPrefix(base, "1000") {
			continue // COIN-M has no 1000x contracts
		}
		if r, ok := inverse[base+"USD_PERP"]; ok && r.MarginAsset == base && r.ContractSize > 0 {
			size := decimal.NewFromFloat(r.ContractSize)
			if c := contract(r, base, size, decimalsOf[base], inverseTiers[r.Symbol], fundingOf(r.Symbol)); c != nil && !listed[c["symbol"].(string)] {
				c["status"] = *status
				contracts, added = append(contracts, c), added+1
			}
		}
	}

	var buf bytes.Buffer
	buf.WriteString("{\n")
	writeList(&buf, "fee_schedules", rawList(doc.FeeSchedules), false)
	writeList(&buf, "assets", anyList(doc.Assets), false)
	writeList(&buf, "pairs", anyList(doc.Pairs), false)
	writeList(&buf, "contracts", anyList(contracts), true)
	buf.WriteString("}\n")
	var check any
	if err := json.Unmarshal(buf.Bytes(), &check); err != nil {
		log.Fatalf("generated JSON does not parse: %v", err)
	}
	if err := os.WriteFile(*file, buf.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	n := map[string]int{}
	for _, c := range contracts {
		n[c["margin_type"].(string)]++
	}
	log.Printf("%s: %d contracts (%d USDT-margined, %d coin-margined), %d new, %d with their funding refreshed", *file, len(contracts), n["USDT"], n["COIN"], added, refreshed)
}

// contract is the entry of Binance's perpetual r on base: a linear one
// (size zero) or a coin-margined one of face value size, funding by fund;
// nil when its rules cannot be kept (logged).
func contract(r remote, base string, size decimal.Decimal, baseDecimals int, tiers []bracket, fund fundingRule) map[string]any {
	inverse := size.IsPositive()
	tick := decimal.RequireFromString(r.filter("PRICE_FILTER").TickSize)
	lot, minQty := decimal.NewFromInt(1), decimal.NewFromInt(1)
	if !inverse {
		lot = decimal.RequireFromString(r.filter("LOT_SIZE").StepSize)
		minQty = decimal.RequireFromString(r.filter("LOT_SIZE").MinQty)
	}
	maxQty := decimal.RequireFromString(r.filter("MARKET_LOT_SIZE").MaxQty)
	tick = decimal.Max(tick, decimal.New(1, -quoteDecimals))
	for decimals(tick)+decimals(lot) > quoteDecimals {
		lot = lot.Mul(decimal.NewFromInt(10))
	}
	minQty = decimal.Max(minQty, lot).Div(lot).Ceil().Mul(lot)
	maxQty = maxQty.Div(lot).Floor().Mul(lot)
	switch {
	case !inverse && decimals(lot) > baseDecimals:
		log.Printf("skip %s: its lot %s has more decimals than %s's %d", r.Symbol, lot, base, baseDecimals)
		return nil
	case !maxQty.GreaterThan(minQty):
		log.Printf("skip %s: no room between %s and %s", r.Symbol, minQty, maxQty)
		return nil
	case len(tiers) == 0:
		log.Printf("skip %s: Binance publishes no risk brackets for it", r.Symbol)
		return nil
	}
	band := decimal.NewFromFloat(0.05)
	if up := r.filter("PERCENT_PRICE").MultiplierUp; up != "" {
		band = decimal.Min(decimal.Max(decimal.RequireFromString(up).Sub(decimal.NewFromInt(1)), band), decimal.NewFromFloat(0.15))
	}
	var riskTiers []map[string]any
	for _, b := range tiers {
		riskTiers = append(riskTiers, map[string]any{
			"max_notional": decimal.NewFromFloat(b.NotionalCap).String(), "max_leverage": b.MaxLeverage,
			"mmr": decimal.NewFromFloat(b.MMR).String(),
		})
	}
	symbol, quote, margin, settle := base+"-USDT-PERP", "USDT", "USDT", "USDT"
	minNotional, impact := decimal.NewFromInt(5), decimal.NewFromInt(10000)
	if inverse {
		symbol, quote, margin, settle = base+"-USD-PERP", "USD", "COIN", base
		minNotional, impact = size, decimal.NewFromInt(10000).Div(size).Ceil()
	}
	log.Printf("%-18s tick %-10s lot %-8s max %-12s band %-5s %2d tiers to %dx", symbol, tick, lot, maxQty, band, len(riskTiers), tiers[0].MaxLeverage)
	return map[string]any{
		"symbol": symbol, "type": "PERPETUAL", "base_asset": base, "quote_asset": quote, "index_symbol": base + "-USDT",
		"tick_size": tick.String(), "lot_size": lot.String(), "min_quantity": minQty.String(), "max_quantity": maxQty.String(),
		"min_notional": minNotional.String(), "price_band": band.String(), "risk_tiers": riskTiers,
		"funding_interval_hours": fund.Hours, "interest_rate": fund.interest().String(), "funding_cap": fund.Cap.String(), "impact_notional": impact.String(),
		"fee_tier": "perp", "status": "PREPARE", "margin_type": margin, "settle_asset": settle, "contract_size": size.String(),
		"reference_symbol": r.Symbol,
	}
}

func decimals(d decimal.Decimal) int {
	if d.Exponent() >= 0 {
		return 0
	}
	// Trailing zeros of the coefficient do not count.
	s := strings.TrimRight(d.String(), "0")
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return len(s) - i - 1
	}
	return 0
}

func rawList(in []json.RawMessage) []string {
	out := make([]string, len(in))
	for i, r := range in {
		var v any
		_ = json.Unmarshal(r, &v)
		b, _ := json.Marshal(v)
		out[i] = string(b)
	}
	return out
}

func anyList(in []map[string]any) []string {
	out := make([]string, len(in))
	for i, v := range in {
		b, err := json.Marshal(v)
		if err != nil {
			log.Fatal(err)
		}
		out[i] = string(b)
	}
	return out
}

// writeList writes one object a line, the file's compact style.
func writeList(buf *bytes.Buffer, key string, items []string, last bool) {
	fmt.Fprintf(buf, "  %q: [\n", key)
	for i, it := range items {
		sep := ","
		if i == len(items)-1 {
			sep = ""
		}
		fmt.Fprintf(buf, "    %s%s\n", it, sep)
	}
	if last {
		buf.WriteString("  ]\n")
	} else {
		buf.WriteString("  ],\n")
	}
}
