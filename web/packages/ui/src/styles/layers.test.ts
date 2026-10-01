import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// Floating layers open above whatever opened them (design §5.1): a list
// opened under a sticky header, in a sheet or in a dialog stays on top.
// Vitest runs in the package's directory and loads CSS imports empty.
describe("layers", () => {
  const css = readFileSync(resolve(process.cwd(), "src/styles/tokens.css"), "utf8");
  const z = (name: string) => Number(css.match(new RegExp(`--z-${name}:\\s*(\\d+)`))?.[1]);

  it("put menus, lists and popovers above sticky headers, sheets and dialogs", () => {
    const order = ["sticky", "sheet", "dialog", "dropdown", "toast"].map(z);
    expect(order.every(Number.isFinite)).toBe(true);
    expect([...order].sort((a, b) => a - b)).toEqual(order);
    expect(new Set(order).size).toBe(order.length);
  });
});
