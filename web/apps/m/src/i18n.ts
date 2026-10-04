// The mobile site's own strings. The shell's (this file) are merged over
// the shared ones at start-up; each area keeps its strings in
// ./i18n/<area>.ts under its own namespace (default export
// { "zh-CN": {...}, en: {...} }), loaded with the area's pages
// (routing.tsx lazyPage).

type Messages = { "zh-CN": Record<string, unknown>; en: Record<string, unknown> };

const base: Messages = {
  "zh-CN": {
    m: {
      perpetual: "永续",
      testMode: "测试模式",
      soon: "该页面正在建设中",
      pullToRefresh: "下拉刷新",
      releaseToRefresh: "松开刷新",
    },
  },
  en: {
    m: {
      perpetual: "Perpetual",
      testMode: "Test mode",
      soon: "This page is on its way",
      pullToRefresh: "Pull to refresh",
      releaseToRefresh: "Release to refresh",
    },
  },
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

/** mMessages are the shell's strings. */
export const mMessages: Messages = base;

/** withAreas merges areas' strings over the shell's (tests render pages without lazyPage). */
export function withAreas(...areas: Messages[]): Messages {
  return areas.reduce<Messages>(
    (all, m) => ({ "zh-CN": merge(all["zh-CN"], m["zh-CN"]), en: merge(all.en, m.en) }),
    { "zh-CN": { ...base["zh-CN"] }, en: { ...base.en } },
  );
}
