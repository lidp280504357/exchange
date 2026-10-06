import { createQueryClient, initI18n, LiveProvider, MarketStore, qk, type Locale, type WsClient } from "@exchange/core";
import { futuresKeys, type ContractSpec, type FuturesOverviewItem } from "@exchange/core/futures/index";
import { DEFAULT_PROFILE } from "@exchange/core/platform/index";
import { uiMessages } from "@exchange/ui";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { renderToString } from "react-dom/server";
import { MemoryRouter, Route, Routes } from "react-router";
import { beforeAll, describe, expect, it } from "vitest";
import { FuturesDataBoard } from "../../features/futures/FuturesDataBoard";
import { withAreas } from "../../i18n";
import futuresMessages from "../../i18n/futures";
import marketsMessages from "../../i18n/markets";
import { FuturesCenter } from "../trade/parts/FuturesCenter";
import FuturesData from "./FuturesData";

// A server render of the futures data pages with the test server's
// contracts and figures (2026-10-07): the overview lists the linear
// contracts while the terminal does not open the coin-margined ones, and
// all of them once it does; the board names every statistic of a contract
// with data and says there is none for the platform coin's. The language
// comes from navigator.language, set by each language's test file.

function contract(symbol: string, over: Partial<ContractSpec> = {}): ContractSpec {
  const [base = "", quote = ""] = symbol.split("-");
  const coin = quote === "USD";
  return {
    symbol, type: "PERPETUAL", base_asset: base, quote_asset: quote, index_symbol: `${base}-USDT`, tick_size: base === "ASTRA" ? "0.0001" : "0.1",
    lot_size: coin ? "1" : "0.001", min_quantity: coin ? "1" : "0.001", max_quantity: "1000", min_notional: coin ? "100" : "5", price_band: "0.05",
    max_leverage: 125, risk_tiers: [], funding_interval_hours: 8, interest_rate: "0.0001", funding_cap: "0.0075", impact_notional: "10000",
    maker_fee_rate: "0.0002", taker_fee_rate: "0.0005", status: "TRADING", margin_type: coin ? "COIN" : "USDT", settle_asset: coin ? base : "USDT",
    contract_size: coin ? "100" : "0", reference_symbol: base === "ASTRA" ? null : coin ? `${base}USD_PERP` : `${base}USDT`, ...over,
  } as ContractSpec;
}

function item(symbol: string, over: Partial<FuturesOverviewItem> = {}): FuturesOverviewItem {
  return {
    symbol, mark_price: "85757.5", index_price: "85796.40282609", funding_rate: "-0.00000808", next_funding_time: "2026-10-07T08:00:00Z",
    open_interest: "96106.962", open_interest_value: "8241892793.72", change: "0.00483685", quote_volume: "9526117101.79", futures_data: true, ...over,
  };
}

const linear = [contract("BTC-USDT-PERP"), contract("ASTRA-USDT-PERP")];
const all = [...linear, contract("BTC-USD-PERP")];
const overview = [
  item("BTC-USDT-PERP"),
  item("BTC-USD-PERP", { open_interest: "12570113", open_interest_value: "1257011300", funding_rate: "-0.00000495" }),
  item("ASTRA-USDT-PERP", { mark_price: "1.04132153", open_interest: null, open_interest_value: null, funding_rate: "0.0001", futures_data: false }),
];

let qc: QueryClient;
let market: MarketStore;

function seed(terminal: ContractSpec[]) {
  qc = createQueryClient();
  qc.setQueryData(qk.platform, DEFAULT_PROFILE);
  qc.setQueryData(qk.pairs, { pairs: [] });
  qc.setQueryData(qk.contracts, { contracts: terminal });
  qc.setQueryData(futuresKeys.overview, { contracts: overview });
}

function render(path: string, pattern: string, page: ReactNode): string {
  return renderToString(
    <QueryClientProvider client={qc}>
      <LiveProvider ws={{ subscribe: () => () => {}, onStatus: () => () => {} } as unknown as WsClient} market={market}>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path={pattern} element={page} />
          </Routes>
        </MemoryRouter>
      </LiveProvider>
    </QueryClientProvider>,
  );
}

/** describePages renders the futures data pages in one language (the stores' initial one). */
export function describePages(locale: Locale) {
  const say = (zh: string, tw: string, en: string) => (locale === "en" ? en : locale === "zh-TW" ? tw : zh);
  describe(`futures data pages in ${locale}`, () => {
    beforeAll(() => {
      const pc = withAreas(marketsMessages, futuresMessages);
      initI18n({
        "zh-CN": { ...uiMessages["zh-CN"], ...pc["zh-CN"] },
        "zh-TW": { ...uiMessages["zh-TW"], ...pc["zh-TW"] },
        en: { ...uiMessages.en, ...pc.en },
      });
      const ws = { subscribe: () => () => {} } as unknown as WsClient;
      market = new MarketStore(ws, (cb) => cb());
    });

    it("the overview, before and after the terminal opens the coin-margined contracts", () => {
      seed(linear);
      const html = render("/futures/data", "/futures/data", <FuturesData />);
      expect(html).toContain(say("合约数据", "合約數據", "Futures data"));
      expect(html).toContain("BTCUSDT");
      expect(html).toContain("ASTRAUSDT");
      expect(html).not.toContain("BTCUSD<");
      // The open interest's value, and the board of the first contract with data.
      expect(html).toContain(locale === "en" ? "8.24B" : "82.42");
      expect(html).toContain(say("BTCUSDT 合约数据", "BTCUSDT 合約數據", "BTCUSDT futures data"));
      expect(html).toContain(say("大户账户数多空比", "大戶帳戶數多空比", "Top trader long/short (accounts)"));

      seed(all);
      const coin = render("/futures/data?margin=coin", "/futures/data", <FuturesData />);
      expect(coin).toContain("BTCUSD<");
      expect(coin).toContain(say("币本位", "幣本位", "COIN-M"));
      expect(coin).not.toContain("ASTRAUSDT");
    });

    it("the board: every statistic, and nothing asked for the platform coin's contract", () => {
      seed(linear);
      const html = render("/", "/", <FuturesDataBoard contract={linear[0]!} layout="page" />);
      for (const m of ["持仓量", "主动买卖量", "基差", "资金费率历史", "爆仓"]) {
        if (locale === "zh-CN") expect(html).toContain(m);
      }
      expect(html).toContain(say("5分钟", "5分鐘", "5m"));
      const astra = render("/", "/", <FuturesDataBoard contract={linear[1]!} />);
      expect(astra).toContain(say("暂无数据", "暫無數據", "No data"));
    });

    it("the terminal's centre column opens on the chart", () => {
      seed(linear);
      const html = render("/futures/BTC-USDT-PERP", "/futures/:symbol", <FuturesCenter contract={linear[0]!}>chart here</FuturesCenter>);
      expect(html).toContain("chart here");
      expect(html).toContain(say("数据", "數據", "Data"));
      expect(html).not.toContain('data-testid="futures-data"');
    });
  });
}
