import { describe, expect, it } from "vitest";
import src from "../../../../../internal/platform/flags/flags.go?raw";
import { flagsZh } from "./flags";

// Every flag the backend knows (internal/platform/flags: flags.Known) has
// its Chinese description here, so the feature flags page never falls back
// to the backend's English in Chinese (user 2026-10-10).

/** known reads the keys of flags.Known: its Key constants' values. */
function known(): string[] {
  const values = new Map([...src.matchAll(/\b(Key\w+)\s*=\s*"([a-z0-9_.]+)"/g)].map((m) => [m[1]!, m[2]!]));
  const body = src.slice(src.indexOf("var Known = map[string]string{"));
  const names = [...body.slice(0, body.indexOf("\n}\n")).matchAll(/^\s*(Key\w+):/gm)].map((m) => m[1]!);
  return names.map((n) => values.get(n) ?? `unresolved ${n}`);
}

describe("flag descriptions", () => {
  it("covers every flag the backend knows, in Chinese", () => {
    const keys = known();
    expect(keys.length).toBeGreaterThan(30);
    const desc = flagsZh.admin.flagDesc as Record<string, Record<string, string>>;
    const missing = keys.filter((k) => {
      const [group, name] = k.split(".");
      return !group || !name || !/[一-鿿]/.test(desc[group]?.[name] ?? "");
    });
    expect(missing).toEqual([]);
  });
});
