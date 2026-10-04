import { createQueryClient, initI18n, LiveProvider, MarketStore, qk, type Locale, type TickerData, type WsClient } from "@exchange/core";
import { contentKeys, loadArticle, loadArticles } from "@exchange/core/content/index";
import { DEFAULT_PROFILE } from "@exchange/core/platform/index";
import { uiMessages } from "@exchange/ui";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { renderToString } from "react-dom/server";
import { MemoryRouter, Route, Routes } from "react-router";
import { beforeAll, describe, expect, it } from "vitest";
import { withAreas } from "../../i18n";
import contentMessages from "../../i18n/content";
import marketsMessages from "../../i18n/markets";
import Announcements from "../content/Announcements";
import Article from "../content/Article";
import Help from "../content/Help";
import Coin from "./Coin";
import Home from "./Home";
import Markets from "./Markets";

// A server render of each page with seeded data: the pages must render
// their content (not throw) with the markets of today's test server (3
// pairs, 2 contracts) and the repository's articles. A server render reads
// the stores' initial state, whose language comes from navigator.language,
// so each language has its own test file (pages.zh.test.tsx,
// pages.en.test.tsx) that sets it before the stores exist.

const listed = "2026-09-30T10:10:50Z";

function pair(symbol: string, over: Record<string, unknown> = {}) {
  const [base = "", quote = ""] = symbol.split("-");
  return {
    symbol, base_asset: base, quote_asset: quote, base_name: base === "BTC" ? "Bitcoin" : "Ethereum", rank: base === "BTC" ? 1 : 2,
    categories: base === "BTC" ? ["layer-1", "pow"] : ["layer-1", "smart-contracts"], tick_size: "0.01", lot_size: "0.0001", price_decimals: 2,
    qty_decimals: 4, min_quantity: "0.0001", max_quantity: "100", min_notional: "5", price_band: "0.1", maker_fee_rate: "0.001",
    taker_fee_rate: "0.001", status: "TRADING", reference_symbol: `${base}${quote}`, reference_multiplier: "1", listed_at: listed, ...over,
  };
}

function contract(symbol: string, index: string) {
  const base = symbol.split("-")[0]!;
  return {
    symbol, type: "PERPETUAL", base_asset: base, quote_asset: "USDT", index_symbol: index, tick_size: "0.1", lot_size: "0.001",
    min_quantity: "0.001", max_quantity: "100", min_notional: "5", price_band: "0.05", max_leverage: 50,
    risk_tiers: [{ max_notional: "50000", max_leverage: 50, mmr: "0.004" }], funding_interval_hours: 8, interest_rate: "0.0001",
    funding_cap: "0.0075", impact_notional: "10000", maker_fee_rate: "0.0002", taker_fee_rate: "0.0005", status: "TRADING",
  };
}

function ticker(symbol: string, last: string, change: string, quote_volume: string): TickerData {
  return { symbol, last, open: last, high: last, low: last, volume: "100", quote_volume, trade_count: 1, change, bid: null, ask: null, updated_at: new Date().toISOString() };
}

const tickers = [
  ticker("BTC-USDT", "85226.01", "0.01082438", "1352353240.3036124"),
  ticker("BTC-USDT-PERP", "85226.1", "0.01082438", "1352353240.3036124"),
  ticker("ETH-BTC", "0.0405", "0.00746269", "0.017478"),
  ticker("ETH-USDT", "2730.63", "-0.00247679", "735996609.375873"),
  ticker("ETH-USDT-PERP", "2730.6", "-0.00247679", "735996609.375873"),
];

let qc: QueryClient;
let market: MarketStore;

async function seed() {
  const pcMessages = withAreas(marketsMessages, contentMessages);
  initI18n({ "zh-CN": { ...uiMessages["zh-CN"], ...pcMessages["zh-CN"] }, en: { ...uiMessages.en, ...pcMessages.en } });
  qc = createQueryClient();
  // In test mode, as the test server: the test content (the pinned notice).
  qc.setQueryData(qk.platform, { ...DEFAULT_PROFILE, test_mode: { enabled: true, banner: true, text: { "zh-CN": "测试模式", en: "Test mode" } } });
  qc.setQueryData(qk.pairs, { pairs: [pair("BTC-USDT"), pair("ETH-BTC", { reference_symbol: null, price_decimals: 5 }), pair("ETH-USDT", { status: "PREPARE" })] });
  qc.setQueryData(qk.contracts, { contracts: [contract("BTC-USDT-PERP", "BTC-USDT"), contract("ETH-USDT-PERP", "ETH-USDT")] });
  qc.setQueryData(qk.tickers, { tickers });
  qc.setQueryData(qk.assets, { assets: [{ asset_code: "USDT", name: "Tether USD", decimals: 6, rank: 3, categories: ["stablecoin"], deposit_enabled: false, withdraw_enabled: false, trading_enabled: true, networks: [] }] });
  for (const locale of ["zh-CN", "en"] as const) {
    for (const section of ["announcements", "help"] as const) {
      const list = await loadArticles(section, locale, "test");
      qc.setQueryData(contentKeys.list(section, locale, "test"), list);
      for (const a of list) qc.setQueryData(contentKeys.article(section, locale, "test", a.slug), await loadArticle(section, a.slug, locale, "test"));
    }
    // What loadArticle answers for a slug without a file.
    qc.setQueryData(contentKeys.article("help", locale, "test", "nope"), await loadArticle("help", "nope", locale, "test"));
  }
  const ws = { subscribe: () => () => {} } as unknown as WsClient;
  market = new MarketStore(ws, (cb) => cb());
  market.seedTickers(tickers);
}

function render(path: string, pattern: string, page: ReactNode): string {
  return renderToString(
    <QueryClientProvider client={qc}>
      <LiveProvider ws={{} as WsClient} market={market}>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path={pattern} element={page} />
          </Routes>
        </MemoryRouter>
      </LiveProvider>
    </QueryClientProvider>,
  );
}

/** describePages renders every page in one language (the stores' initial one). */
export function describePages(locale: Locale) {
  describe(`pages in ${locale}`, () => {
    beforeAll(seed);

    it("home", () => {
      const html = render("/", "/", <Home />);
      expect(html).toContain("BTC/USDT");
      expect(html).toContain("85,226.01");
      expect(html).toContain(locale === "en" ? "Why Astras" : "为什么选择 Astras");
      // The latest announcements come from the Markdown.
      expect(html).toContain(locale === "en" ? "USDT perpetual futures are live" : "USDT 永续合约上线");
    });

    it("markets, with filters from the address", () => {
      const html = render("/markets", "/markets", <Markets />);
      for (const s of ["BTC", "ETH", "BTCUSDT"]) expect(html).toContain(s);
      expect(html).toContain("85,226.01");
      const futures = render("/markets?cat=futures&sort=change&dir=asc", "/markets", <Markets />);
      expect(futures).toContain("ETHUSDT");
      const none = render("/markets?q=nothing-matches", "/markets", <Markets />);
      expect(none).toContain(locale === "en" ? "No coins match" : "没有匹配的币种");
    });

    it("a coin, a quote asset and an unknown coin", () => {
      const btc = render("/coin/BTC", "/coin/:symbol", <Coin />);
      expect(btc).toContain(locale === "en" ? "Bitcoin" : "比特币");
      expect(btc).toContain("50x");
      const usdt = render("/coin/USDT", "/coin/:symbol", <Coin />);
      expect(usdt).toContain(locale === "en" ? "quote asset" : "计价资产");
      const unknown = render("/coin/NOPE", "/coin/:symbol", <Coin />);
      expect(unknown).toContain(locale === "en" ? "Coin not found" : "未找到该币种");
    });

    it("announcements and one of them", () => {
      const list = render("/announcements", "/announcements", <Announcements />);
      expect(list).toContain(locale === "en" ? "Pinned" : "置顶");
      const one = render("/announcements/usdt-perpetual-launch", "/announcements/:slug", <Article section="announcements" />);
      expect(one).toContain("BTC-USDT-PERP");
      expect(one).toContain("<table>");
      expect(one).not.toContain("<script");
    });

    it("the help centre, an article and a missing one", () => {
      const help = render("/help?q=%E5%85%85%E5%80%BC", "/help", <Help />);
      expect(help).toContain(locale === "en" ? "Help center" : "帮助中心");
      const article = render("/help/withdraw", "/help/:slug", <Article section="help" />);
      expect(article).toContain(locale === "en" ? "How to withdraw" : "如何提现");
      expect(article).toContain(locale === "en" ? "On this page" : "本文目录");
      const missing = render("/help/nope", "/help/:slug", <Article section="help" />);
      expect(missing).toContain(locale === "en" ? "Article not found" : "文章不存在");
    });
  });
}
