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
