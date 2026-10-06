import { describe, expect, it } from "vitest";
import { columnPath, formatTicks, indexAt, labelIndexes, niceStep, niceTicks, seriesDomain, tickDecimals, type SeriesPoint } from "./scale";

const pts = (rows: Record<string, number>[]): SeriesPoint[] => rows.map((v, i) => ({ t: i * 300_000, v }));

describe("series chart scale", () => {
  it("takes each form's domain", () => {
    expect(seriesDomain({ kind: "line", key: "a" }, pts([{ a: 5 }, { a: 3 }, { a: 9 }]))).toEqual([3, 9]);
    expect(seriesDomain({ kind: "line", key: "a" }, [])).toEqual([0, 0]);
    expect(seriesDomain({ kind: "columns", key: "a" }, pts([{ a: 0.0001 }, { a: -0.0002 }]))).toEqual([-0.0002, 0.0001]);
    expect(seriesDomain({ kind: "columns", key: "a" }, pts([{ a: 2 }, { a: 3 }]))).toEqual([0, 3]);
    expect(seriesDomain({ kind: "share", up: "l", down: "s" }, pts([{ l: 0.7, s: 0.3 }]))).toEqual([0, 1]);
    expect(seriesDomain({ kind: "mirror", up: "b", down: "s" }, pts([{ b: 10, s: 30 }, { b: 20, s: 5 }]))).toEqual([-30, 30]);
  });

  it("rounds steps and spreads ticks over whole steps", () => {
    expect(niceStep(0.33)).toBe(0.5);
    expect(niceStep(184_653)).toBe(200_000);
    expect(niceStep(2.2)).toBe(2.5);
    expect(niceTicks(0, 1, 4)).toEqual({ min: 0, max: 1, step: 0.5, ticks: [0, 0.5, 1] });
    expect(niceTicks(96_143.788, 96_257.545, 4)).toEqual({ min: 96_100, max: 96_300, step: 50, ticks: [96_100, 96_150, 96_200, 96_250, 96_300] });
    expect(niceTicks(-0.00002485, 0.00006752, 4).ticks).toEqual([-0.00005, 0, 0.00005, 0.0001]);
    // A flat line opens around its value.
    expect(niceTicks(5, 5, 4).ticks).toEqual([4.95, 5, 5.05]);
    expect(niceTicks(0, 0, 4).ticks).toEqual([-1, 0, 1]);
  });

  it("formats ticks apart from each other", () => {
    expect(tickDecimals(50)).toBe(0);
    expect(tickDecimals(0.25)).toBe(2);
    expect(tickDecimals(2.5)).toBe(1);
    expect(tickDecimals(0.00002, true)).toBe(3);
    expect(formatTicks([0, 0.5, 1], 0.5, "percent", "zh-CN")).toEqual(["0%", "50%", "100%"]);
    expect(formatTicks([-0.00002, 0, 0.00002], 0.00002, "percent", "zh-CN")).toEqual(["-0.002%", "0.000%", "0.002%"]);
    // A narrow range of a large figure is written out in full.
    expect(formatTicks([96_100, 96_150, 96_200], 50, "compact", "en")).toEqual(["96,100", "96,150", "96,200"]);
    expect(formatTicks([0, 200_000, 400_000], 200_000, "compact", "en")).toEqual(["0", "200,000", "400,000"]);
    expect(formatTicks([12_560_000, 12_580_000], 20_000, "compact", "en")).toEqual(["12.56M", "12.58M"]);
    expect(formatTicks([0, 10_000_000, 20_000_000], 10_000_000, "compact", "en")).toEqual(["0", "10M", "20M"]);
    expect(formatTicks([12_560_000, 12_580_000], 20_000, "compact", "zh-CN")).toEqual(["1256万", "1258万"]);
    expect(formatTicks([-60, -50, -40], 10, "plain", "zh-CN")).toEqual(["-60", "-50", "-40"]);
  });

  it("draws columns with a rounded data end", () => {
    expect(columnPath(10, 100, 40, 8)).toBe("M6,100 L6,44 A4,4 0 0 1 10,40 L10,40 A4,4 0 0 1 14,44 L14,100 Z");
    expect(columnPath(10, 100, 140, 8)).toBe("M6,100 L6,136 A4,4 0 0 0 10,140 L10,140 A4,4 0 0 0 14,136 L14,100 Z");
    // The radius never exceeds half the width or the height; nothing to draw at zero height.
    expect(columnPath(10, 100, 98, 8)).toBe("M6,100 L6,100 A2,2 0 0 1 8,98 L12,98 A2,2 0 0 1 14,100 L14,100 Z");
    expect(columnPath(10, 100, 100, 8)).toBe("");
  });

  it("finds the point under the pointer and the points the axis labels", () => {
    expect(indexAt(0, 300, 30)).toBe(0);
    expect(indexAt(299.9, 300, 30)).toBe(29);
    expect(indexAt(155, 300, 30)).toBe(15);
    expect(indexAt(-1, 300, 30)).toBeNull();
    expect(indexAt(10, 300, 0)).toBeNull();
    expect(labelIndexes(30, 320, 80)).toEqual([0, 10, 19, 29]);
    expect(labelIndexes(2, 320, 80)).toEqual([0, 1]);
    expect(labelIndexes(30, 50, 80)).toEqual([29]);
    expect(labelIndexes(0, 320)).toEqual([]);
  });
});
