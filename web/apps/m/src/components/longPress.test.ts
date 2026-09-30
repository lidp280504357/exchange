import { describe, expect, it, vi } from "vitest";
import { LongPress, movedBeyond, type Timers } from "./longPress";

/** A fake clock: run(ms) fires the timers due by then. */
function fakeTimers() {
  let now = 0;
  let next = 1;
  const pending = new Map<number, { at: number; fn: () => void }>();
  const timers: Timers = {
    set: (fn, ms) => {
      const id = next++;
      pending.set(id, { at: now + ms, fn });
      return id;
    },
    clear: (id) => {
      pending.delete(id as number);
    },
  };
  const run = (ms: number) => {
    now += ms;
    for (const [id, t] of [...pending]) {
      if (t.at <= now) {
        pending.delete(id);
        t.fn();
      }
    }
  };
  return { timers, run, pending: () => pending.size };
}

describe("movedBeyond", () => {
  it("measures the distance against the slop", () => {
    expect(movedBeyond({ x: 0, y: 0 }, { x: 6, y: 8 }, 10)).toBe(false); // exactly 10
    expect(movedBeyond({ x: 0, y: 0 }, { x: 6, y: 9 }, 10)).toBe(true);
    expect(movedBeyond({ x: 5, y: 5 }, { x: 5, y: 5 }, 0)).toBe(false);
  });
});

describe("LongPress", () => {
  it("fires after the delay and swallows the click that follows, once", () => {
    const clock = fakeTimers();
    const fire = vi.fn();
    const press = new LongPress(fire, { delay: 450, slop: 10, timers: clock.timers });
    press.start({ x: 10, y: 10 });
    expect(press.pressing).toBe(true);
    clock.run(449);
    expect(fire).not.toHaveBeenCalled();
    clock.run(1);
    expect(fire).toHaveBeenCalledTimes(1);
    expect(press.pressing).toBe(false);
    press.end();
    expect(press.consumeClick()).toBe(true);
    expect(press.consumeClick()).toBe(false);
  });

  it("does not fire for a tap, and the tap's click goes through", () => {
    const clock = fakeTimers();
    const fire = vi.fn();
    const press = new LongPress(fire, { timers: clock.timers });
    press.start({ x: 0, y: 0 });
    clock.run(120);
    press.end();
    clock.run(1000);
    expect(fire).not.toHaveBeenCalled();
    expect(clock.pending()).toBe(0);
    expect(press.consumeClick()).toBe(false);
  });

  it("gives up when the finger moves away (the page scrolls) or the browser cancels", () => {
    const clock = fakeTimers();
    const fire = vi.fn();
    const press = new LongPress(fire, { delay: 450, slop: 10, timers: clock.timers });
    press.start({ x: 0, y: 0 });
    press.move({ x: 3, y: 4 }); // within the slop
    expect(press.pressing).toBe(true);
    press.move({ x: 0, y: 30 });
    expect(press.pressing).toBe(false);
    clock.run(1000);
    expect(fire).not.toHaveBeenCalled();

    press.start({ x: 0, y: 0 });
    press.cancel();
    clock.run(1000);
    expect(fire).not.toHaveBeenCalled();
  });

  it("forgets a fired press whose click never came when the next press starts", () => {
    const clock = fakeTimers();
    const fire = vi.fn();
    const press = new LongPress(fire, { timers: clock.timers });
    press.start({ x: 0, y: 0 });
    clock.run(1000);
    expect(fire).toHaveBeenCalledTimes(1);
    press.end();
    // No click followed; the next tap must open the row.
    press.start({ x: 0, y: 0 });
    press.end();
    expect(press.consumeClick()).toBe(false);
  });
});
