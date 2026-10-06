import { createQueryClient, initI18n, LiveProvider, LOCALES, MarketStore, qk, type Locale, type TickerData, type WsClient } from "@exchange/core";
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
// pairs, 2 contracts), with a list long enough to be virtual, and with the
// repository's articles. A server render reads the stores' initial state,
// whose language comes from navigator.language, so each language has its
// own test file (pages.zh.test.tsx, pages.en.test.tsx) that sets it first.

const listed = "2026-09-30T10:10:50Z";

function pair(symbol: string, over: Record<string, unknown> = {}) {
  const [base = "", quote = ""] = symbol.split("-");
  return {
    symbol, base_asset: base, quote_asset: quote, base_name: base === "BTC" ? "Bitcoin" : base === "ETH" ? "Ethereum" : base, rank: base === "BTC" ? 1 : base === "ETH" ? 2 : null,
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

/** 55 pairs X00-USDT … X54-USDT: more than the list shows at once. */
const many = Array.from({ length: 55 }, (_, i) => pair(`X${String(i).padStart(2, "0")}-USDT`, { rank: null, categories: [] }));

let qc: QueryClient;
let big: QueryClient;
let market: MarketStore;

/** memoryStorage stands in for localStorage: a visitor who never signed in on this device. */
function memoryStorage(): Storage {
  const data = new Map<string, string>();
  return {
    get length() {
      return data.size;
    },
    clear: () => data.clear(),
    getItem: (k) => data.get(k) ?? null,
    key: (i) => [...data.keys()][i] ?? null,
    removeItem: (k) => void data.delete(k),
    setItem: (k, v) => void data.set(k, String(v)),
  };
}

async function seed() {
  const messages = withAreas(marketsMessages, contentMessages);
  initI18n({
    "zh-CN": { ...uiMessages["zh-CN"], ...messages["zh-CN"] },
    "zh-TW": { ...uiMessages["zh-TW"], ...messages["zh-TW"] },
    en: { ...uiMessages.en, ...messages.en },
  });
  // A server render reads the session store's initial state (restoring):
  // with no sign-in hint on the device the home page shows the sign-up card.
  Object.defineProperty(globalThis, "localStorage", { value: memoryStorage(), configurable: true, writable: true });
  qc = createQueryClient();
  // The platform profile with welcome credits (design 2026-10-04 §4.2): the sign-up card promises them.
  // In test mode, as the test server: the test content and the 测试模式 badges.
  qc.setQueryData(qk.platform, {
    ...DEFAULT_PROFILE,
    test_mode: { enabled: true, banner: true, text: { "zh-CN": "测试模式", en: "Test mode" } },
    welcome_credits: [{ asset: "USDT", amount: "10000" }],
  });
  qc.setQueryData(qk.pairs, { pairs: [pair("BTC-USDT"), pair("ETH-BTC", { reference_symbol: null, price_decimals: 5 }), pair("ETH-USDT", { status: "PREPARE" })] });
  qc.setQueryData(qk.contracts, { contracts: [contract("BTC-USDT-PERP", "BTC-USDT"), contract("ETH-USDT-PERP", "ETH-USDT")] });
  qc.setQueryData(qk.tickers, { tickers });
  qc.setQueryData(qk.assets, {
    assets: [{ asset_code: "USDT", name: "Tether USD", decimals: 6, rank: 3, categories: ["stablecoin"], deposit_enabled: false, withdraw_enabled: false, trading_enabled: true, networks: [] }],
  });
  for (const locale of LOCALES) {
    for (const section of ["announcements", "help"] as const) {
      const list = await loadArticles(section, locale, "test");
      qc.setQueryData(contentKeys.list(section, locale, "test"), list);
      for (const a of list) qc.setQueryData(contentKeys.article(section, locale, "test", a.slug), await loadArticle(section, a.slug, locale, "test"));
    }
    // What loadArticle answers for a slug without a file.
    qc.setQueryData(contentKeys.article("help", locale, "test", "nope"), await loadArticle("help", "nope", locale, "test"));
  }
  big = createQueryClient();
  big.setQueryData(qk.pairs, { pairs: many });
  big.setQueryData(qk.contracts, { contracts: [] });
  big.setQueryData(qk.tickers, { tickers: [] });
  const ws = { subscribe: () => () => {} } as unknown as WsClient;
  market = new MarketStore(ws, (cb) => cb());
  market.seedTickers(tickers);
}

function render(path: string, pattern: string, page: ReactNode, client: QueryClient = qc): string {
  return renderToString(
    <QueryClientProvider client={client}>
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
  // The text expected in each language: Simplified, Traditional, English.
  const say = (zh: string, tw: string, en: string) => (locale === "en" ? en : locale === "zh-TW" ? tw : zh);
  describe(`mobile pages in ${locale}`, () => {
    beforeAll(seed);

    it("home", () => {
      const html = render("/", "/", <Home />);
      expect(html).toContain(say("注册即领 10,000 USDT", "註冊即領 10,000 USDT", "Sign up for 10,000 USDT"));
      expect(html).toContain("BTC/USDT");
      expect(html).toContain("85,226.01");
      // The gainers board (BTC-USDT is the one trading USDT pair).
      expect(html).toContain("+1.08%");
      expect(html).toContain(say("为什么选择 Astras", "為什麼選擇 Astras", "Why Astras"));
      // The banner starts with the pinned announcement.
      expect(html).toContain(say("关于测试环境与模拟资金", "關於測試環境與模擬資金", "About the test environment and simulated funds"));
    });

    it("markets, with filters from the address", () => {
      const html = render("/markets", "/markets", <Markets />);
      for (const s of ["BTC", "ETH", "BTCUSDT"]) expect(html).toContain(s);
      expect(html).toContain("85,226.01");
      expect(html).toContain(say("即将上线", "即將上線", "Coming soon"));
      const futures = render("/markets?cat=futures&sort=change&dir=asc", "/markets", <Markets />);
      expect(futures).toContain("ETHUSDT");
      expect(futures).not.toContain("ETH-BTC");
      const none = render("/markets?q=nothing-matches", "/markets", <Markets />);
      expect(none).toContain(say("没有匹配的币种", "沒有匹配的幣種", "No coins match"));
      const favorites = render("/markets?cat=favorites", "/markets", <Markets />);
      expect(favorites).toContain(say("还没有自选", "還沒有自選", "No favorites yet"));
    });

    it("a long markets list renders only its first rows", () => {
      const html = render("/markets", "/markets", <Markets />, big);
      expect(html).toContain('role="list"');
      expect(html).toContain(">X00<");
      expect(html).toContain(">X15<");
      expect(html).not.toContain(">X40<");
    });

    it("a coin, a quote asset and an unknown coin", () => {
      const btc = render("/coin/BTC", "/coin/:symbol", <Coin />);
      expect(btc).toContain(say("比特币", "比特幣", "Bitcoin"));
      expect(btc).toContain(say("最高 50 倍", "最高 50 倍", "Up to 50x"));
      expect(btc).toContain(say(">去交易<", ">去交易<", ">Trade<"));
      const usdt = render("/coin/USDT", "/coin/:symbol", <Coin />);
      expect(usdt).toContain(say("计价资产", "計價資產", "quote asset"));
      const unknown = render("/coin/NOPE", "/coin/:symbol", <Coin />);
      expect(unknown).toContain(say("未找到该币种", "未找到該幣種", "Coin not found"));
      // A lower-case or pair address redirects (nothing to render on the server).
      expect(() => render("/coin/btc-usdt", "/coin/:symbol", <Coin />)).not.toThrow();
    });

    it("announcements and one of them", () => {
      const list = render("/announcements", "/announcements", <Announcements />);
      expect(list).toContain(say("置顶", "置頂", "Pinned"));
      const one = render("/announcements/usdt-perpetual-launch", "/announcements/:slug", <Article section="announcements" />);
      expect(one).toContain(say("USDT 永续合约上线", "USDT 永續合約上線", "USDT perpetual futures are live"));
      expect(one).toContain("<table>");
      expect(one).not.toContain("<script");
    });

    it("the help centre, a search, an article and a missing one", () => {
      const help = render("/help", "/help", <Help />);
      expect(help).toContain(say("充值与提现", "充值與提現", "Deposits and withdrawals"));
      const search = render(say("/help?q=%E5%85%85%E5%80%BC", "/help?q=%E5%85%85%E5%80%BC", "/help?q=deposit"), "/help", <Help />);
      expect(search).toContain(say("如何充值", "如何充值", "How to deposit"));
      const article = render("/help/withdraw", "/help/:slug", <Article section="help" />);
      expect(article).toContain(say("如何提现", "如何提現", "How to withdraw"));
      expect(article).toContain(say("本文目录", "本文目錄", "On this page"));
      const missing = render("/help/nope", "/help/:slug", <Article section="help" />);
      expect(missing).toContain(say("文章不存在", "文章不存在", "Article not found"));
    });
  });
}
