import { describe, expect, it } from "vitest";
import { fitRows } from "./bookRows";

describe("fitRows", () => {
  it("cuts the rooms of review CE as it should", () => {
    expect(fitRows(0)).toBeNull();
    expect(fitRows(199.9)).toEqual({ levels: 5, rowHeight: 20 });
    expect(fitRows(600)).toEqual({ levels: 15, rowHeight: 20 });
    expect(fitRows(615)).toEqual({ levels: 15, rowHeight: 20.5 });
    expect(fitRows(970)).toEqual({ levels: 20, rowHeight: 24 });
    expect(fitRows(1000)).toEqual({ levels: 20, rowHeight: 24 });
  });
  it("shares the room out exactly in rows of 20 to 24 px", () => {
    // The rooms the PC book got on 2026-10-05 (B74).
    expect(fitRows(325)).toEqual({ levels: 8, rowHeight: 20.3125 });
    expect(fitRows(226)).toEqual({ levels: 5, rowHeight: 22.6 });
    expect(fitRows(645)).toEqual({ levels: 16, rowHeight: 20.15625 });
    for (let room = 200; room <= 960; room += 0.5) {
      const { levels, rowHeight } = fitRows(room)!;
      expect(rowHeight).toBeGreaterThanOrEqual(20);
      expect(rowHeight).toBeLessThanOrEqual(24);
      expect(Math.abs(2 * levels * rowHeight - room)).toBeLessThan(1e-9);
    }
  });
  it("keeps 5 rows of 20 px in a room too low for them, to be cut off", () => {
    expect(fitRows(150)).toEqual({ levels: 5, rowHeight: 20 });
  });
  it("stops at 20 rows of 24 px, leaving a taller room's rest blank", () => {
    expect(fitRows(1200)).toEqual({ levels: 20, rowHeight: 24 });
  });
});
