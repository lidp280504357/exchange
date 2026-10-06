import { en as coreEn } from "@exchange/core/i18n/en";
import { zhCN as coreZh } from "@exchange/core/i18n/zh-CN";
import { FUTURES_METRICS, FUTURES_PERIODS, METRIC_VALUES } from "@exchange/core/futures/index";
import { uiMessages } from "@exchange/ui";
import { describe, expect, it } from "vitest";
import { withAreas } from "../../i18n";
import futures from "../../i18n/futures";

// The futures data strings (namespace mFutures, batch F): the languages
// have the same keys, every key the overview page, the terminal's data tab
// and the boards name exists, and so does every key built from a
// statistic, a value or a period.

type Tree = Record<string, unknown>;

function keys(tree: unknown, prefix = ""): string[] {
  if (typeof tree !== "object" || tree === null) return [prefix];
  return Object.entries(tree as Tree).flatMap(([k, v]) => keys(v, prefix ? `${prefix}.${k}` : k));
}

function lookup(tree: unknown, key: string): unknown {
  return key.split(".").reduce<unknown>((node, part) => (typeof node === "object" && node !== null ? (node as Tree)[part] : undefined), tree);
}

const m = withAreas(futures);
const zh = { ...coreZh, ...uiMessages["zh-CN"], ...m["zh-CN"] };
const en = { ...coreEn, ...uiMessages.en, ...m.en };

describe("futures data strings", () => {
  it("have the same keys in every language", () => {
    expect(keys(futures.en).sort()).toEqual(keys(futures["zh-CN"]).sort());
    expect(keys(futures["zh-TW"]).sort()).toEqual(keys(futures["zh-CN"]).sort());
  });

  it("cover every key the pages name", () => {
    const sources = {
      ...import.meta.glob<string>("./*.tsx", { query: "?raw", import: "default", eager: true }),
      ...import.meta.glob<string>("../../features/futures/*.{ts,tsx}", { query: "?raw", import: "default", eager: true }),
      ...import.meta.glob<string>("../trade/parts/FuturesDataTab.tsx", { query: "?raw", import: "default", eager: true }),
    };
    const used = new Set<string>();
    for (const src of Object.values(sources)) {
      for (const m of src.matchAll(/\bt\(\s*"([A-Za-z][\w.-]*)"/g)) used.add(m[1]!);
    }
    expect(used.size).toBeGreaterThan(30);
    const missing = [...used].filter((k) => typeof lookup(zh, k) !== "string" || typeof lookup(en, k) !== "string");
    expect(missing).toEqual([]);
  });

  it("cover the keys built from the statistics, their values and the periods", () => {
    const dynamic = [
      ...FUTURES_METRICS.flatMap((m) => [`mFutures.metrics.${m}.title`, `mFutures.metrics.${m}.hint`]),
      ...FUTURES_METRICS.flatMap((m) => METRIC_VALUES[m].map((v) => `mFutures.values.${v.key}`)),
      ...FUTURES_PERIODS.map((p) => `mFutures.periods.${p}`),
    ];
    for (const k of dynamic) {
      expect(typeof lookup(zh, k), k).toBe("string");
      expect(typeof lookup(en, k), k).toBe("string");
    }
  });
});
