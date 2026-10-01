//go:build ignore

// gen-top50 lists the top 50 coins and the 2026-10-02 extension in
// deploy/instruments/test.json (design §8.5, ADR-0013, ADR-0014): an asset
// and a USDT pair each, following Binance spot. It reads Binance's filters
// and prices once, at generation time, and keeps everything else in the
// file (fee tiers,
// USDT/BTC/ETH with their networks, ETH-BTC, SOL-BTC kept PREPARE for the
// end-to-end check of a pair not trading, the contracts):
//
//	go run deploy/instruments/gen-top50.go [-file deploy/instruments/test.json] [-api https://data-api.binance.vision]
//
// Rules:
//   - the tick is Binance's (times the multiplier), at least 0.000001; the
//     lot is Binance's step (divided by the multiplier), coarsened until
//     tick x lot has at most 6 decimals, so every price x quantity is
//     exact in USDT; prices shown from Binance's book can be ordered;
//   - minimum notional 5 USDT; the most per order is worth 1,000,000;
//     a price band of 10%; the default fee tier;
//   - coins below 0.001 USDT trade 1000 at a time (1000SHIB, ADR-0014);
//   - assets other than USDT, BTC and ETH are internal: no networks, no
//     deposits or withdrawals (ADR-0013); decimals 6 to 8 from the lot;
//   - new pairs are listed PREPARE (apply never changes a status): open
//     them once HOUSE's liquidity is on (docs/runbook/market-maker.md); a
//     listed pair keeps its tick, lot and quantity limits;
//   - a coin of the extension without a trading Binance USDT pair is
//     skipped (one of the top 50 stops the run).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// coin is one of the 50: its asset code, English name, Binance symbol,
// multiplier and sector tags (the web apps' markets.tags).
type coin struct {
	Code       string
	Name       string
	Remote     string
	Multiplier int64
	Categories []string
}

// top50 is design §8.5's list, by market capitalisation, stablecoins and
// wrapped coins left out.
var top50 = []coin{
	{"BTC", "Bitcoin", "BTCUSDT", 1, []string{"layer-1", "pow"}},
	{"ETH", "Ethereum", "ETHUSDT", 1, []string{"layer-1", "smart-contracts"}},
	{"BNB", "BNB", "BNBUSDT", 1, []string{"exchange", "layer-1"}},
	{"SOL", "Solana", "SOLUSDT", 1, []string{"layer-1", "smart-contracts"}},
	{"XRP", "XRP", "XRPUSDT", 1, []string{"payments"}},
	{"DOGE", "Dogecoin", "DOGEUSDT", 1, []string{"meme", "pow"}},
	{"ADA", "Cardano", "ADAUSDT", 1, []string{"layer-1", "pos"}},
	{"TRX", "TRON", "TRXUSDT", 1, []string{"layer-1", "smart-contracts"}},
	{"HYPE", "Hyperliquid", "HYPEUSDT", 1, []string{"defi", "layer-1"}}, // TONUSDT is suspended on Binance spot (2026-10-01)
	{"AVAX", "Avalanche", "AVAXUSDT", 1, []string{"layer-1", "smart-contracts"}},
	{"LINK", "Chainlink", "LINKUSDT", 1, []string{"oracle", "infrastructure"}},
	{"DOT", "Polkadot", "DOTUSDT", 1, []string{"layer-1", "interoperability"}},
	{"LTC", "Litecoin", "LTCUSDT", 1, []string{"pow", "payments"}},
	{"BCH", "Bitcoin Cash", "BCHUSDT", 1, []string{"pow", "payments"}},
	{"1000SHIB", "1000 Shiba Inu", "SHIBUSDT", 1000, []string{"meme"}},
	{"UNI", "Uniswap", "UNIUSDT", 1, []string{"defi"}},
	{"NEAR", "NEAR Protocol", "NEARUSDT", 1, []string{"layer-1", "ai"}},
	{"XLM", "Stellar", "XLMUSDT", 1, []string{"payments"}},
	{"ATOM", "Cosmos", "ATOMUSDT", 1, []string{"interoperability", "layer-1"}},
	{"ICP", "Internet Computer", "ICPUSDT", 1, []string{"layer-1", "infrastructure"}},
	{"APT", "Aptos", "APTUSDT", 1, []string{"layer-1"}},
	{"ARB", "Arbitrum", "ARBUSDT", 1, []string{"layer-2"}},
	{"OP", "Optimism", "OPUSDT", 1, []string{"layer-2"}},
	{"FIL", "Filecoin", "FILUSDT", 1, []string{"storage", "depin"}},
	{"ETC", "Ethereum Classic", "ETCUSDT", 1, []string{"pow", "layer-1"}},
	{"HBAR", "Hedera", "HBARUSDT", 1, []string{"layer-1"}},
	{"SUI", "Sui", "SUIUSDT", 1, []string{"layer-1"}},
	{"1000PEPE", "1000 Pepe", "PEPEUSDT", 1000, []string{"meme"}},
	{"RENDER", "Render", "RENDERUSDT", 1, []string{"ai", "depin"}},
	{"INJ", "Injective", "INJUSDT", 1, []string{"defi", "layer-1"}},
	{"TIA", "Celestia", "TIAUSDT", 1, []string{"infrastructure", "layer-1"}},
	{"SEI", "Sei", "SEIUSDT", 1, []string{"layer-1"}},
	{"AAVE", "Aave", "AAVEUSDT", 1, []string{"defi"}},
	{"GRT", "The Graph", "GRTUSDT", 1, []string{"infrastructure", "ai"}},
	{"ALGO", "Algorand", "ALGOUSDT", 1, []string{"layer-1", "pos"}},
	{"VET", "VeChain", "VETUSDT", 1, []string{"layer-1"}},
	{"POL", "Polygon", "POLUSDT", 1, []string{"layer-2"}},
	{"THETA", "Theta Network", "THETAUSDT", 1, []string{"depin"}},
	{"EGLD", "MultiversX", "EGLDUSDT", 1, []string{"layer-1"}},
	{"XTZ", "Tezos", "XTZUSDT", 1, []string{"layer-1", "pos"}},
	{"SAND", "The Sandbox", "SANDUSDT", 1, []string{"metaverse", "gaming"}},
	{"MANA", "Decentraland", "MANAUSDT", 1, []string{"metaverse", "gaming"}},
	{"AXS", "Axie Infinity", "AXSUSDT", 1, []string{"gaming"}},
	{"NEO", "Neo", "NEOUSDT", 1, []string{"layer-1", "smart-contracts"}},
	{"ZEC", "Zcash", "ZECUSDT", 1, []string{"privacy", "pow"}},
	{"TAO", "Bittensor", "TAOUSDT", 1, []string{"ai"}},
	{"WLD", "Worldcoin", "WLDUSDT", 1, []string{"ai", "infrastructure"}},
	{"ENA", "Ethena", "ENAUSDT", 1, []string{"defi"}},
	{"ONDO", "Ondo", "ONDOUSDT", 1, []string{"rwa", "defi"}},
	{"JUP", "Jupiter", "JUPUSDT", 1, []string{"defi"}},
}

// extension is design §8.5's 2026-10-02 extension: well-known coins after
// the top 50, spot only and internal like them.
var extension = []coin{
	{"TRUMP", "Official Trump", "TRUMPUSDT", 1, []string{"meme"}},
	{"WIF", "dogwifhat", "WIFUSDT", 1, []string{"meme"}},
	{"1000BONK", "1000 Bonk", "BONKUSDT", 1000, []string{"meme"}},
	{"1000FLOKI", "1000 Floki", "FLOKIUSDT", 1000, []string{"meme"}},
	{"PNUT", "Peanut the Squirrel", "PNUTUSDT", 1, []string{"meme"}},
	{"PENGU", "Pudgy Penguins", "PENGUUSDT", 1, []string{"meme", "nft"}},
	{"CAKE", "PancakeSwap", "CAKEUSDT", 1, []string{"defi"}},
	{"LDO", "Lido DAO", "LDOUSDT", 1, []string{"defi"}},
	{"CRV", "Curve DAO", "CRVUSDT", 1, []string{"defi"}},
	{"COMP", "Compound", "COMPUSDT", 1, []string{"defi"}},
	{"SNX", "Synthetix", "SNXUSDT", 1, []string{"defi"}},
	{"1INCH", "1inch", "1INCHUSDT", 1, []string{"defi"}},
	{"DYDX", "dYdX", "DYDXUSDT", 1, []string{"defi"}},
	{"PENDLE", "Pendle", "PENDLEUSDT", 1, []string{"defi"}},
	{"YFI", "yearn.finance", "YFIUSDT", 1, []string{"defi"}},
	{"TON", "Toncoin", "TONUSDT", 1, []string{"layer-1"}},
	{"STX", "Stacks", "STXUSDT", 1, []string{"layer-2"}},
	{"IMX", "Immutable", "IMXUSDT", 1, []string{"layer-2", "gaming"}},
	{"KAVA", "Kava", "KAVAUSDT", 1, []string{"layer-1", "defi"}},
	{"MINA", "Mina", "MINAUSDT", 1, []string{"layer-1"}},
	{"FLOW", "Flow", "FLOWUSDT", 1, []string{"layer-1", "nft"}},
	{"KSM", "Kusama", "KSMUSDT", 1, []string{"layer-1", "interoperability"}},
	{"ZIL", "Zilliqa", "ZILUSDT", 1, []string{"layer-1"}},
	{"IOTA", "IOTA", "IOTAUSDT", 1, []string{"layer-1", "infrastructure"}},
	{"CFX", "Conflux", "CFXUSDT", 1, []string{"layer-1"}},
	{"FET", "Artificial Superintelligence Alliance", "FETUSDT", 1, []string{"ai"}},
	{"AR", "Arweave", "ARUSDT", 1, []string{"storage"}},
	{"PYTH", "Pyth Network", "PYTHUSDT", 1, []string{"oracle"}},
	{"JTO", "Jito", "JTOUSDT", 1, []string{"defi"}},
	{"RUNE", "THORChain", "RUNEUSDT", 1, []string{"defi", "interoperability"}},
	{"GALA", "Gala", "GALAUSDT", 1, []string{"gaming"}},
	{"ENS", "Ethereum Name Service", "ENSUSDT", 1, []string{"infrastructure"}},
	{"APE", "ApeCoin", "APEUSDT", 1, []string{"nft", "metaverse"}},
	{"CHZ", "Chiliz", "CHZUSDT", 1, []string{"layer-1", "nft"}},
	{"QNT", "Quant", "QNTUSDT", 1, []string{"interoperability"}},
	{"DASH", "Dash", "DASHUSDT", 1, []string{"payments", "pow"}},
	{"BAT", "Basic Attention Token", "BATUSDT", 1, []string{"payments"}},
	{"ENJ", "Enjin Coin", "ENJUSDT", 1, []string{"gaming", "nft"}},
}

// backed are the assets with deposits and withdrawals; the file keeps
// their definitions.
var backed = map[string]bool{"USDT": true, "BTC": true, "ETH": true}

// quoteDecimals are USDT's.
const quoteDecimals = 6

type symbolInfo struct {
	Symbol  string `json:"symbol"`
	Status  string `json:"status"`
	Filters []struct {
		FilterType string `json:"filterType"`
		TickSize   string `json:"tickSize"`
		StepSize   string `json:"stepSize"`
	} `json:"filters"`
}

func get(api, path string, q url.Values, out any) error {
	c := &http.Client{Timeout: 20 * time.Second}
	resp, err := c.Get(api + path + "?" + q.Encode())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func main() {
	file := flag.String("file", "deploy/instruments/test.json", "reference data file to update")
	api := flag.String("api", "https://data-api.binance.vision", "Binance spot REST base URL")
	flag.Parse()

	// Every symbol's rules and price: asking for a list fails as a whole
	// when one of it is unknown.
	var info struct {
		Symbols []symbolInfo `json:"symbols"`
	}
	if err := get(*api, "/api/v3/exchangeInfo", url.Values{"permissions": {"SPOT"}}, &info); err != nil {
		log.Fatalf("exchange info: %v", err)
	}
	var prices []struct {
		Symbol string `json:"symbol"`
		Price  string `json:"price"`
	}
	if err := get(*api, "/api/v3/ticker/price", url.Values{}, &prices); err != nil {
		log.Fatalf("prices: %v", err)
	}
	filters := map[string]symbolInfo{}
	for _, s := range info.Symbols {
		filters[s.Symbol] = s
	}
	price := map[string]decimal.Decimal{}
	for _, p := range prices {
		price[p.Symbol] = decimal.RequireFromString(p.Price)
	}

	raw, err := os.ReadFile(*file)
	if err != nil {
		log.Fatal(err)
	}
	var doc struct {
		FeeSchedules []json.RawMessage `json:"fee_schedules"`
		Assets       []map[string]any  `json:"assets"`
		Pairs        []map[string]any  `json:"pairs"`
		Contracts    []json.RawMessage `json:"contracts"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		log.Fatal(err)
	}

	assets := map[string]map[string]any{}
	for _, a := range doc.Assets {
		assets[a["asset_code"].(string)] = a
	}
	pairs := map[string]map[string]any{}
	for _, p := range doc.Pairs {
		pairs[p["symbol"].(string)] = p
	}
	mult1000 := decimal.NewFromInt(1000)
	coins := append(append([]coin{}, top50...), extension...)
	var listed []coin
	for rank, c := range coins {
		s, ok := filters[c.Remote]
		p, priced := price[c.Remote]
		if !ok || !priced || s.Status != "TRADING" {
			if rank < len(top50) {
				log.Fatalf("%s: not a trading Binance spot symbol", c.Remote)
			}
			log.Printf("skip %s: not a trading Binance spot symbol", c.Remote)
			continue
		}
		listed = append(listed, c)
		var tick, step decimal.Decimal
		for _, f := range s.Filters {
			switch f.FilterType {
			case "PRICE_FILTER":
				tick = decimal.RequireFromString(f.TickSize)
			case "LOT_SIZE":
				step = decimal.RequireFromString(f.StepSize)
			}
		}
		m := decimal.NewFromInt(c.Multiplier)
		p, tick, step = p.Mul(m), tick.Mul(m), step.Div(m)
		if p.LessThan(decimal.RequireFromString("0.001")) {
			log.Fatalf("%s trades at %s: list it 1000 at a time (ADR-0014)", c.Code, p)
		}
		if c.Multiplier > 1 && p.Div(mult1000).GreaterThanOrEqual(decimal.RequireFromString("0.001")) {
			log.Printf("note: %s would trade above 0.001 one at a time", c.Code)
		}
		tick = decimal.Max(tick, decimal.New(1, -quoteDecimals))
		lot := step
		for decimals(tick)+decimals(lot) > quoteDecimals {
			lot = lot.Mul(decimal.NewFromInt(10))
		}
		// The most per order is worth 1,000,000 USDT, in whole lots.
		maxQty := decimal.NewFromInt(1000000).Div(p).Div(lot).Floor().Mul(lot)
		symbol := c.Code + "-USDT"

		if !backed[c.Code] {
			assets[c.Code] = map[string]any{
				"asset_code": c.Code, "name": c.Name, "decimals": min(8, max(6, decimals(lot)+3)), "rank": rank + rankOffset(rank),
				"categories": c.Categories, "deposit_enabled": false, "withdraw_enabled": false, "trading_enabled": true,
				"risk_restricted": false, "networks": []any{},
			}
		} else {
			a := assets[c.Code]
			a["rank"], a["categories"] = rank+rankOffset(rank), c.Categories
		}
		pair := pairs[symbol]
		status := "PREPARE"
		rules := map[string]any{"tick_size": tick.String(), "lot_size": lot.String(), "min_quantity": lot.String(), "max_quantity": maxQty.String()}
		if pair != nil {
			// A listed pair keeps its rules: a new tick or lot would strand
			// the resting orders.
			status = pair["status"].(string)
			for k := range rules {
				rules[k] = pair[k]
			}
		}
		pairs[symbol] = map[string]any{
			"symbol": symbol, "base_asset": c.Code, "quote_asset": "USDT", "reference_symbol": c.Remote,
			"tick_size": rules["tick_size"], "lot_size": rules["lot_size"], "min_quantity": rules["min_quantity"], "max_quantity": rules["max_quantity"],
			"min_notional": "5", "price_band": "0.1", "fee_tier": "default", "status": status,
		}
		if c.Multiplier > 1 {
			pairs[symbol]["reference_multiplier"] = m.String()
		}
		log.Printf("%-14s price %-14s tick %-10s lot %-10s max %s", symbol, p.StringFixed(6), tick, lot, maxQty)
	}

	// Assets and pairs in the file's order, then the new ones in list order.
	var outAssets []map[string]any
	seen := map[string]bool{}
	for _, a := range doc.Assets {
		code := a["asset_code"].(string)
		outAssets, seen[code] = append(outAssets, assets[code]), true
	}
	for _, c := range listed {
		if !seen[c.Code] {
			outAssets, seen[c.Code] = append(outAssets, assets[c.Code]), true
		}
	}
	var outPairs []map[string]any
	for _, p := range doc.Pairs {
		sym := p["symbol"].(string)
		outPairs, seen[sym] = append(outPairs, pairs[sym]), true
	}
	for _, c := range listed {
		if sym := c.Code + "-USDT"; !seen[sym] {
			outPairs, seen[sym] = append(outPairs, pairs[sym]), true
		}
	}

	var buf bytes.Buffer
	buf.WriteString("{\n")
	writeList(&buf, "fee_schedules", rawList(doc.FeeSchedules), false)
	writeList(&buf, "assets", anyList(outAssets), false)
	writeList(&buf, "pairs", anyList(outPairs), false)
	writeList(&buf, "contracts", rawList(doc.Contracts), true)
	buf.WriteString("}\n")
	var check any
	if err := json.Unmarshal(buf.Bytes(), &check); err != nil {
		log.Fatalf("generated JSON does not parse: %v", err)
	}
	if err := os.WriteFile(*file, buf.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("%s: %d assets, %d pairs", *file, len(outAssets), len(outPairs))
}

// rankOffset makes room for USDT at rank 3 among the coins.
func rankOffset(i int) int {
	if i >= 2 {
		return 2
	}
	return 1
}

func decimals(d decimal.Decimal) int {
	if e := d.Exponent(); e < 0 {
		// Trailing zeros of the coefficient do not count.
		s := strings.TrimRight(d.String(), "0")
		if i := strings.IndexByte(s, '.'); i >= 0 {
			return len(s) - i - 1
		}
		return 0
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
