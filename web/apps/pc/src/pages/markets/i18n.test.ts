import { en as coreEn } from "@exchange/core/i18n/en";
import { zhCN as coreZh } from "@exchange/core/i18n/zh-CN";
import { describe, expect, it } from "vitest";
import { withAreas } from "../../i18n";
import content from "../../i18n/content";
import futures from "../../i18n/futures";
import markets from "../../i18n/markets";

// The futures category's columns and groups come from the futures area (batch F3).
const pcMessages = withAreas(markets, content, futures);

// The strings of the home, markets, coin, announcement and help pages:
// both languages have the same keys, and every key the pages name exists.

type Tree = Record<string, unknown>;

function keys(tree: unknown, prefix = ""): string[] {
  if (typeof tree !== "object" || tree === null) return [prefix];
  return Object.entries(tree as Tree).flatMap(([k, v]) => keys(v, prefix ? `${prefix}.${k}` : k));
}

function lookup(tree: unknown, key: string): unknown {
  return key.split(".").reduce<unknown>((node, part) => (typeof node === "object" && node !== null ? (node as Tree)[part] : undefined), tree);
}

describe("page strings", () => {
  it("have the same keys in Chinese and English", () => {
    expect(keys(markets.en).sort()).toEqual(keys(markets["zh-CN"]).sort());
    expect(keys(content.en).sort()).toEqual(keys(content["zh-CN"]).sort());
  });

  it("cover every key the pages name, in both languages", () => {
    const sources = {
      ...import.meta.glob<string>("./*.tsx", { query: "?raw", import: "default", eager: true }),
      ...import.meta.glob<string>("../content/*.tsx", { query: "?raw", import: "default", eager: true }),
    };
    const used = new Set<string>();
    for (const src of Object.values(sources)) {
      for (const m of src.matchAll(/\bt\(\s*"([A-Za-z][\w.-]*)"/g)) used.add(m[1]!);
    }
    expect(used.size).toBeGreaterThan(50);
    const zh = { ...coreZh, ...pcMessages["zh-CN"] };
    const en = { ...coreEn, ...pcMessages.en };
    const missing = [...used].filter((k) => typeof lookup(zh, k) !== "string" || typeof lookup(en, k) !== "string");
    expect(missing).toEqual([]);
  });

  it("cover the keys built from data", () => {
    const zh = pcMessages["zh-CN"];
    const en = pcMessages.en;
    const dynamic = [
      ...["wallet", "futures", "liquidity", "security"].flatMap((id) => [`pcMarkets.home.why.${id}.title`, `pcMarkets.home.why.${id}.desc`]),
      ...["register", "deposit", "trade"].flatMap((id) => ["title", "desc", "action"].map((f) => `pcMarkets.home.steps.${id}.${f}`)),
      ...["notice", "product", "security", "account", "funds", "trading", "futures", "faq"].map((c) => `pcContent.categories.${c}`),
    ];
    for (const k of dynamic) {
      expect(typeof lookup(zh, k), k).toBe("string");
      expect(typeof lookup(en, k), k).toBe("string");
    }
  });
});
