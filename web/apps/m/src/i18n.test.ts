import { en as coreEn } from "@exchange/core/i18n/en";
import { zhCN as coreZh } from "@exchange/core/i18n/zh-CN";
import { zhTW as coreTw } from "@exchange/core/i18n/zh-TW";
import { uiMessages } from "@exchange/ui";
import { describe, expect, it } from "vitest";
import { mMessages } from "./i18n";

// Every language has the same keys (design 2026-10-06 繁体中文 §2.2): the
// shared strings, the components', the shell's and each area's. The
// Traditional Chinese ones are generated from the Simplified (pnpm i18n);
// a key missing in English would show in Chinese.

type Tree = Record<string, unknown>;
type Messages = { "zh-CN": Tree; "zh-TW": Tree; en: Tree };

function keys(tree: unknown, prefix = ""): string[] {
  if (typeof tree !== "object" || tree === null) return [prefix];
  return Object.entries(tree as Tree)
    .flatMap(([k, v]) => keys(v, prefix ? `${prefix}.${k}` : k))
    .sort();
}

const areas = Object.entries(import.meta.glob<{ default: Messages }>("./i18n/*.ts", { eager: true })).filter(([path]) => !path.endsWith(".zh-TW.ts"));

describe("strings", () => {
  it("have the same keys in Simplified Chinese, Traditional Chinese and English", () => {
    const all: [string, Messages][] = [
      ["core", { "zh-CN": coreZh, "zh-TW": coreTw, en: coreEn }],
      ["ui", uiMessages],
      ["shell", mMessages],
      ...areas.map(([path, m]): [string, Messages] => [path, m.default]),
    ];
    expect(areas.length).toBeGreaterThanOrEqual(7);
    for (const [name, m] of all) {
      expect(keys(m["zh-TW"]), name).toEqual(keys(m["zh-CN"]));
      expect(keys(m.en), name).toEqual(keys(m["zh-CN"]));
    }
  });
});
