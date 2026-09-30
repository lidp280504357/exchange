import { useSyncExternalStore } from "react";

// A shared clock per period: every "3 minutes ago" on a page follows one
// interval instead of one timer per instance, and the interval stops when
// the last reader unmounts.

type Clock = { now: number; listeners: Set<() => void>; timer?: ReturnType<typeof setInterval> };

const clocks = new Map<number, Clock>();

function clockOf(period: number): Clock {
  let c = clocks.get(period);
  if (!c) {
    c = { now: Date.now(), listeners: new Set() };
    clocks.set(period, c);
  }
  return c;
}

/** subscribeClock calls fn every `period` ms while subscribed. */
export function subscribeClock(period: number, fn: () => void): () => void {
  const c = clockOf(period);
  c.listeners.add(fn);
  if (!c.timer) {
    c.now = Date.now();
    c.timer = setInterval(() => {
      c.now = Date.now();
      for (const l of c.listeners) l();
    }, period);
  }
  return () => {
    c.listeners.delete(fn);
    if (c.listeners.size === 0 && c.timer) {
      clearInterval(c.timer);
      c.timer = undefined;
    }
  };
}

/** clockNow is the time of the clock's last tick (stable between ticks). */
export function clockNow(period: number): number {
  return clockOf(period).now;
}

/** useNow re-renders the caller once per `period` (default 30 s). */
export function useNow(period = 30_000): number {
  return useSyncExternalStore(
    (fn) => subscribeClock(period, fn),
    () => clockNow(period),
    () => clockNow(period),
  );
}
