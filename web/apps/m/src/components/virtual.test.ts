import { describe, expect, it } from "vitest";
import { sameRange, visibleRange } from "./virtual";

const base = { rowHeight: 64, count: 100, overscan: 0 };

describe("visibleRange", () => {
  it("covers the rows on screen from the top of the page", () => {
    // The list starts 200 px down; a 640 px screen at the top shows 440 px of it.
    expect(visibleRange({ ...base, scrollTop: 0, viewport: 640, offset: 200 })).toEqual({ start: 0, end: 7 });
  });

  it("follows the scroll and adds the overscan on both sides", () => {
    // 1000 px scrolled: rows from (1000-200)/64 = 12.5 to (1640-200)/64 = 22.5.
    expect(visibleRange({ ...base, scrollTop: 1000, viewport: 640, offset: 200 })).toEqual({ start: 12, end: 23 });
    expect(visibleRange({ ...base, overscan: 4, scrollTop: 1000, viewport: 640, offset: 200 })).toEqual({ start: 8, end: 27 });
  });

  it("clamps to the list", () => {
    expect(visibleRange({ ...base, overscan: 6, scrollTop: 0, viewport: 640, offset: 0 })).toEqual({ start: 0, end: 16 });
    // Scrolled to the end of a 100-row list.
    expect(visibleRange({ ...base, overscan: 6, scrollTop: 6400 - 640, viewport: 640, offset: 0 })).toEqual({ start: 84, end: 100 });
  });

  it("renders nothing for a list far below or above the screen", () => {
    expect(visibleRange({ ...base, scrollTop: 0, viewport: 640, offset: 5000 })).toEqual({ start: 0, end: 0 });
    expect(visibleRange({ ...base, scrollTop: 20_000, viewport: 640, offset: 0 })).toEqual({ start: 100, end: 100 });
  });

  it("handles an empty list and bad sizes", () => {
    expect(visibleRange({ ...base, count: 0, scrollTop: 0, viewport: 640, offset: 0 })).toEqual({ start: 0, end: 0 });
    expect(visibleRange({ ...base, rowHeight: 0, scrollTop: 0, viewport: 640, offset: 0 })).toEqual({ start: 0, end: 0 });
    expect(visibleRange({ ...base, scrollTop: 0, viewport: -10, offset: 0 })).toEqual({ start: 0, end: 0 });
  });

  it("compares ranges", () => {
    expect(sameRange({ start: 1, end: 5 }, { start: 1, end: 5 })).toBe(true);
    expect(sameRange({ start: 1, end: 5 }, { start: 1, end: 6 })).toBe(false);
  });
});
