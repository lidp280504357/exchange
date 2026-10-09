import { describe, expect, it } from "vitest";
import { leverageMarks } from "./LeverageDialog";

describe("leverageMarks", () => {
  it("spreads the marks by the contract's own maximum, as Binance's slider has them (F28)", () => {
    expect(leverageMarks(1, 150)).toEqual([1, 30, 60, 90, 120, 150]);
    expect(leverageMarks(1, 125)).toEqual([1, 25, 50, 75, 100, 125]);
    expect(leverageMarks(1, 100)).toEqual([1, 20, 40, 60, 80, 100]);
    expect(leverageMarks(1, 75)).toEqual([1, 15, 30, 45, 60, 75]);
    expect(leverageMarks(1, 50)).toEqual([1, 10, 20, 30, 40, 50]);
    expect(leverageMarks(1, 20)).toEqual([1, 5, 10, 15, 20]);
  });
  it("lists every step of a small range", () => {
    expect(leverageMarks(1, 5)).toEqual([1, 2, 3, 4, 5]);
  });
});
