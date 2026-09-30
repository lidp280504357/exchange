import { en as coreEn } from "@exchange/core/i18n/en";
import { zhCN as coreZh } from "@exchange/core/i18n/zh-CN";
import { uiMessages } from "@exchange/ui";
import { describe, expect, it } from "vitest";
import { withAreas } from "../../i18n";
import content from "../../i18n/content";
import markets from "../../i18n/markets";
import { SORT_OPTIONS } from "./logic";

// The strings of the mobile home, markets, coin, announcement and help
// pages: both languages have the same keys, and every key the pages (and
// the components they use) name exists in both.

const mine = withAreas(markets, content);

type Tree = Record<string, unknown>;

function keys(tree: unknown, prefix = ""): string[] {
  if (typeof tree !== "object" || tree === null) return [prefix];
  return Object.entries(tree as Tree).flatMap(([k, v]) => keys(v, prefix ? `${prefix}.${k}` : k));
}

function lookup(tree: unknown, key: string): unknown {
  return key.split(".").reduce<unknown>((node, part) => (typeof node === "object" && node !== null ? (node as Tree)[part] : undefined), tree);
}

function merged(lng: "zh-CN" | "en"): Tree {
  const core = lng === "en" ? coreEn : coreZh;
  return { ...core, ...uiMessages[lng], ...mine[lng] };
}

describe("page strings", () => {
  it("have the same keys in Chinese and English", () => {
    expect(keys(markets.en).sort()).toEqual(keys(markets["zh-CN"]).sort());
    expect(keys(content.en).sort()).toEqual(keys(content["zh-CN"]).sort());
  });

  it("cover every key the pages and components name, in both languages", () => {
    const sources = {
      ...import.meta.glob<string>("./*.tsx", { query: "?raw", import: "default", eager: true }),
      ...import.meta.glob<string>("../content/*.tsx", { query: "?raw", import: "default", eager: true }),
      ...import.meta.glob<string>("../../components/*.tsx", { query: "?raw", import: "default", eager: true }),
    };
    const used = new Set<string>();
    for (const src of Object.values(sources)) {
      for (const m of src.matchAll(/\bt\(\s*"([A-Za-z][\w.-]*)"/g)) used.add(m[1]!);
    }
    expect(used.size).toBeGreaterThan(60);
    const zh = merged("zh-CN");
    const en = merged("en");
    const missing = [...used].filter((k) => typeof lookup(zh, k) !== "string" || typeof lookup(en, k) !== "string");
    expect(missing).toEqual([]);
  });

  it("cover the keys built from data", () => {
    const zh = merged("zh-CN");
    const en = merged("en");
    const dynamic = [
      ...["wallet", "futures", "liquidity", "security"].flatMap((id) => [`mMarkets.home.why.${id}.title`, `mMarkets.home.why.${id}.desc`]),
      ...SORT_OPTIONS.map((o) => `mMarkets.markets.sorts.${o.id}`),
      ...["notice", "product", "security", "account", "funds", "trading", "futures", "faq"].map((c) => `mContent.categories.${c}`),
      ...["m", "h", "d", "w", "M"].map((u) => `ui.chart.intervals.${u}`),
      "mMarkets.pairs_one",
    ];
    for (const k of dynamic) {
      expect(typeof lookup(zh, k), k).toBe("string");
      expect(typeof lookup(en, k), k).toBe("string");
    }
  });

  it("name every sector tag the PC site names", () => {
    expect(Object.keys(markets["zh-CN"].mMarkets.tags).length).toBeGreaterThanOrEqual(20);
  });
});
