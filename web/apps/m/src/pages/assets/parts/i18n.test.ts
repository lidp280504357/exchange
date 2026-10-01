import { LEDGER_ENTRY_TYPES } from "@exchange/core/assets/ledger";
import { en as coreEn } from "@exchange/core/i18n/en";
import { zhCN as coreZh } from "@exchange/core/i18n/zh-CN";
import { describe, expect, it } from "vitest";
import { mMessages } from "../../../i18n";
import assets from "../../../i18n/assets";

// The strings of the mobile assets pages: both languages have the same
// keys, every key the pages name exists, and so do the keys built from data
// and the enum labels the pages show.

type Tree = Record<string, unknown>;

function keys(tree: unknown, prefix = ""): string[] {
  if (typeof tree !== "object" || tree === null) return [prefix];
  return Object.entries(tree as Tree).flatMap(([k, v]) => keys(v, prefix ? `${prefix}.${k}` : k));
}

function lookup(tree: unknown, key: string): unknown {
  return key.split(".").reduce<unknown>((node, part) => (typeof node === "object" && node !== null ? (node as Tree)[part] : undefined), tree);
}

// A key used with a count may only have its plural forms.
function has(tree: unknown, key: string): boolean {
  return typeof lookup(tree, key) === "string" || typeof lookup(tree, `${key}_other`) === "string";
}

// The shared words, the shell's (m.*) and this area's (mAssets.*).
const zh = { ...coreZh, ...mMessages["zh-CN"], ...assets["zh-CN"] };
const en = { ...coreEn, ...mMessages.en, ...assets.en };

describe("assets page strings", () => {
  it("have the same keys in Chinese and English", () => {
    expect(keys(assets.en).sort()).toEqual(keys(assets["zh-CN"]).sort());
  });

  it("cover every key the pages name, in both languages", () => {
    const sources = {
      ...import.meta.glob<string>("../*.tsx", { query: "?raw", import: "default", eager: true }),
      ...import.meta.glob<string>("./*.tsx", { query: "?raw", import: "default", eager: true }),
    };
    const used = new Set<string>();
    for (const src of Object.values(sources)) {
      for (const m of src.matchAll(/\bt\(\s*"([A-Za-z][\w.-]*)"/g)) used.add(m[1]!);
      // Keys picked by a condition: t(cond ? "a.b" : "a.c").
      for (const m of src.matchAll(/\?\s*"(mAssets\.[\w.-]+)"\s*:\s*"(mAssets\.[\w.-]+)"/g)) {
        used.add(m[1]!);
        used.add(m[2]!);
      }
    }
    expect(used.size).toBeGreaterThan(150);
    const missing = [...used].filter((k) => !has(zh, k) || !has(en, k));
    expect(missing).toEqual([]);
  });

  it("cover the keys built from data", () => {
    const dynamic = [
      ...["SPOT", "FUTURES"].map((a) => `mAssets.common.account.${a}`),
      ...["confirming", "crediting", "credited", "failed"].map((p) => `mAssets.deposit.phase.${p}`),
      ...["detected", "confirming", "credited", "failed"].map((s) => `mAssets.deposit.steps.${s}`),
      ...["BELOW_MINIMUM", "ACCOUNT_CLOSED", "NOT_ELIGIBLE", "UNSUPPORTED_TOKEN"].map((r) => `mAssets.deposit.reasons.${r}`),
      ...["risk", "review", "sign", "broadcast", "confirm", "custody", "done"].map((s) => `mAssets.withdraw.steps.${s}`),
      ...["ADDRESS_FORMAT", "ADDRESS_CHECKSUM", "ADDRESS_NETWORK", "MEMO_REQUIRED", "ADDRESS_OWN"].map((r) => `mAssets.withdraw.reasons.${r}`),
      ...["NEW_ACCOUNT", "NEW_DEVICE", "SECURITY_CHANGE", "NEW_ADDRESS", "LARGE_AMOUNT", "DAILY_SHARE"].map((r) => `mAssets.withdraw.risk.${r}`),
      ...["all", "7d", "30d", "90d", "custom"].map((p) => `mAssets.history.ranges.${p}`),
      ...["available", "frozen"].map((k) => `mAssets.overview.${k}`),
    ];
    for (const k of dynamic) {
      expect(has(zh, k), k).toBe(true);
      expect(has(en, k), k).toBe(true);
    }
  });

  it("label every enum the pages show", () => {
    const codes = [
      ...LEDGER_ENTRY_TYPES,
      ...["SPOT", "FUTURES", "AVAILABLE", "FROZEN", "CHAIN", "INTERNAL", "COMPLETED", "FAILED"],
      ...["REQUESTED", "PENDING_REVIEW", "APPROVED", "SIGNING", "BROADCAST", "CONFIRMING", "SUBMITTED", "CONFIRMED", "INTERNAL_TRANSFER", "REJECTED", "CANCELED"],
    ];
    for (const c of codes) {
      expect(typeof lookup(coreZh, `codes.${c}`), c).toBe("string");
      expect(typeof lookup(coreEn, `codes.${c}`), c).toBe("string");
    }
  });
});
