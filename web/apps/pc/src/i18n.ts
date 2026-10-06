// The PC site's own strings. The shell's (this file) are merged over the
// shared ones at start-up; each area keeps its strings in ./i18n/<area>.ts
// under its own namespace (default export { "zh-CN": {...}, en: {...},
// "zh-TW" }), loaded with the area's pages (routing.tsx lazyPage), so the
// first screen does not carry every page's text. The Traditional Chinese
// is generated from "zh-CN" beside each file (<name>.zh-TW.ts, core's
// scripts/gen-zh-tw.mjs: pnpm i18n).

import zhTW from "./i18n.zh-TW";

type Messages = { "zh-CN": Record<string, unknown>; "zh-TW": Record<string, unknown>; en: Record<string, unknown> };

const base: Messages = {
  "zh-CN": {
    pc: {
      heroTitle: "交易全球主流数字资产",
      heroSubtitle: "现货与永续合约，一个账户、实时行情、毫秒级撮合。",
      start: "立即注册",
      trade: "开始交易",
      overview: "市场概览",
      searchHint: "搜索币种",
      perpetual: "永续",
      usdtMargined: "U 本位",
      coinMargined: "币本位",
      menu: {
        spot: "现货",
        spotHint: "以最优价格买卖数字资产",
        margin: "杠杆交易",
        marginHint: "借币交易，放大资金使用效率",
        usdtFutures: "U本位合约",
        usdtFuturesHint: "以 USDT 作保证金的永续合约",
        coinFutures: "币本位合约",
        coinFuturesHint: "以币作保证金的永续合约",
        futuresData: "合约数据",
        futuresDataHint: "持仓量、多空比、资金费率与爆仓",
        overviewHint: "全部账户的资产与估值",
        depositHint: "从外部钱包转入 USDT、BTC、ETH",
        withdrawHint: "转出到外部地址",
        transferHint: "在现货、杠杆、合约账户之间划转",
        historyHint: "充提、划转、成交与资金费记录",
      },
      testMode: "测试模式",
    },
  },
  en: {
    pc: {
      heroTitle: "Trade the world's leading digital assets",
      heroSubtitle: "Spot and perpetual futures in one account, live markets, millisecond matching.",
      start: "Sign up",
      trade: "Start trading",
      overview: "Market overview",
      searchHint: "Search coins",
      perpetual: "Perpetual",
      usdtMargined: "USDT-M",
      coinMargined: "COIN-M",
      menu: {
        spot: "Spot",
        spotHint: "Buy and sell crypto at the best prices",
        margin: "Margin",
        marginHint: "Borrow to trade with more than you hold",
        usdtFutures: "USDT-M futures",
        usdtFuturesHint: "Perpetuals margined and settled in USDT",
        coinFutures: "COIN-M futures",
        coinFuturesHint: "Perpetuals margined and settled in the coin",
        futuresData: "Futures data",
        futuresDataHint: "Open interest, long/short ratios, funding and liquidations",
        overviewHint: "Every account's assets and their value",
        depositHint: "Bring USDT, BTC or ETH in from a wallet",
        withdrawHint: "Send to an outside address",
        transferHint: "Move funds between spot, margin and futures",
        historyHint: "Deposits, withdrawals, transfers, trades and funding",
      },
      testMode: "Test mode",
    },
  },
  // Generated from "zh-CN" (core's scripts/gen-zh-tw.mjs), as each area's.
  "zh-TW": zhTW,
};

function merge(into: Record<string, unknown>, from: Record<string, unknown>): Record<string, unknown> {
  for (const [k, v] of Object.entries(from)) {
    const cur = into[k];
    into[k] =
      cur && typeof cur === "object" && !Array.isArray(cur) && v && typeof v === "object" && !Array.isArray(v)
        ? merge({ ...(cur as Record<string, unknown>) }, v as Record<string, unknown>)
        : v;
  }
  return into;
}

/** pcMessages are the shell's strings. */
export const pcMessages: Messages = base;

/** withAreas merges areas' strings over the shell's (tests render pages without lazyPage). */
export function withAreas(...areas: Messages[]): Messages {
  return areas.reduce<Messages>(
    (all, m) => ({ "zh-CN": merge(all["zh-CN"], m["zh-CN"]), "zh-TW": merge(all["zh-TW"], m["zh-TW"]), en: merge(all.en, m.en) }),
    { "zh-CN": { ...base["zh-CN"] }, "zh-TW": { ...base["zh-TW"] }, en: { ...base.en } },
  );
}
