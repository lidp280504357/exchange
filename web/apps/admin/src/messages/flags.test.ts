import { describe, expect, it } from "vitest";
import src from "../../../../../internal/platform/flags/flags.go?raw";
import { flagsEn, flagsZh } from "./flags";

// Every flag the backend knows (internal/platform/flags: flags.Known) has
// its description here in both languages, so the feature flags page never
// falls back to what is stored with the flag (user 2026-10-10, A103).

/** known reads the keys of flags.Known: its Key constants' values. */
function known(): string[] {
  const values = new Map([...src.matchAll(/\b(Key\w+)\s*=\s*"([a-z0-9_.]+)"/g)].map((m) => [m[1]!, m[2]!]));
  const body = src.slice(src.indexOf("var Known = map[string]string{"));
  const names = [...body.slice(0, body.indexOf("\n}\n")).matchAll(/^\s*(Key\w+):/gm)].map((m) => m[1]!);
  return names.map((n) => values.get(n) ?? `unresolved ${n}`);
}

/** missing are the keys without a description that passes ok. */
function missing(keys: string[], descs: unknown, ok: (s: string) => boolean): string[] {
  const d = descs as Record<string, Record<string, string>>;
  return keys.filter((k) => {
    const [group, name] = k.split(".");
    return !group || !name || !ok(d[group]?.[name] ?? "");
  });
}

describe("flag descriptions", () => {
  it("cover every flag the backend knows, in Chinese and in English", () => {
    const keys = known();
    expect(keys.length).toBeGreaterThan(30);
    expect(missing(keys, flagsZh.admin.flagDesc, (s) => /[一-鿿]/.test(s))).toEqual([]);
    expect(missing(keys, flagsEn.admin.flagDesc, (s) => /^[A-Z]/.test(s) && !/[一-鿿]/.test(s))).toEqual([]);
  });
});
