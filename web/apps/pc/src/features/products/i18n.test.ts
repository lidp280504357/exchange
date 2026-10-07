import { en as coreEn } from "@exchange/core/i18n/en";
import { zhCN as coreZh } from "@exchange/core/i18n/zh-CN";
import { PRODUCT_LINES } from "@exchange/core/platform/products";
import { describe, expect, it } from "vitest";
import products from "../../i18n/products";

// The strings of the closed product lines (pcProducts): every key the gate, the
// notice, the wind-down page and the assets overview name exists in both
// languages, and so do the keys built from data.

type Tree = Record<string, unknown>;

function lookup(tree: unknown, key: string): unknown {
  return key.split(".").reduce<unknown>((node, part) => (typeof node === "object" && node !== null ? (node as Tree)[part] : undefined), tree);
}

const zh = { ...coreZh, ...products["zh-CN"] };
const en = { ...coreEn, ...products.en };

describe("product line strings", () => {
  it("cover every key the pages name, in both languages", () => {
    const sources = {
      ...import.meta.glob<string>("./*.tsx", { query: "?raw", import: "default", eager: true }),
      ...import.meta.glob<string>(["../../pages/assets/ClosedProducts.tsx", "../../pages/assets/Overview.tsx"], { query: "?raw", import: "default", eager: true }),
    };
    const used = new Set<string>();
    for (const src of Object.values(sources)) for (const m of src.matchAll(/\bt\(\s*"(pcProducts\.[\w.-]+)"/g)) used.add(m[1]!);
    // Built from data: the lines' names, and the wind-down tables' heads.
    for (const line of PRODUCT_LINES) used.add(`pcProducts.lines.${line}`);
    for (const head of ["symbol", "side", "quantity", "entry", "mark", "pnl", "action", "market", "price", "filled", "asset", "total", "available", "account", "totalAsset", "totalLiability"]) {
      used.add(`pcProducts.page.${head}`);
    }
    expect(used.size).toBeGreaterThan(40);
    const missing = [...used].filter((k) => typeof lookup(zh, k) !== "string" || typeof lookup(en, k) !== "string");
    expect(missing).toEqual([]);
  });
});
