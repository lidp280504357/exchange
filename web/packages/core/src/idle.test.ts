import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { IDLE_WITHIN, onIdle, preloadable } from "./idle";

type Ric = (cb: () => void, opts?: { timeout: number }) => number;

const idleKeys = ["requestIdleCallback", "cancelIdleCallback"] as const;
const saved = idleKeys.map((k) => [k, Object.getOwnPropertyDescriptor(window, k)] as const);

// withIdle gives the window requestIdleCallback and cancelIdleCallback, or
// takes them away (Safari) when called without them.
function withIdle(ric?: Ric, cic?: (id: number) => void) {
  Object.defineProperty(window, "requestIdleCallback", { value: ric, configurable: true, writable: true });
  Object.defineProperty(window, "cancelIdleCallback", { value: cic, configurable: true, writable: true });
}

// loading makes the document read as still loading, until its load event.
function loading() {
  Object.defineProperty(document, "readyState", { value: "loading", configurable: true });
}

describe("onIdle", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
    for (const [k, d] of saved) {
      if (d) Object.defineProperty(window, k, d);
      else delete (window as unknown as Record<string, unknown>)[k];
    }
    delete (document as unknown as Record<string, unknown>).readyState;
  });

  it("without requestIdleCallback, runs IDLE_WITHIN after the page's load event", () => {
    withIdle();
    loading();
    const run = vi.fn();
    onIdle(run);
    vi.advanceTimersByTime(10_000);
    expect(run).not.toHaveBeenCalled();
    window.dispatchEvent(new Event("load"));
    vi.advanceTimersByTime(IDLE_WITHIN - 1);
    expect(run).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(run).toHaveBeenCalledTimes(1);
  });

  it("on a page already loaded (a route change), runs IDLE_WITHIN later, unless cancelled first", () => {
    withIdle();
    const run = vi.fn();
    onIdle(run);
    const skipped = vi.fn();
    onIdle(skipped)();
    vi.advanceTimersByTime(IDLE_WITHIN);
    expect(run).toHaveBeenCalledTimes(1);
    expect(skipped).not.toHaveBeenCalled();
  });

  it("cancelled before the load event, never runs", () => {
    withIdle();
    loading();
    const run = vi.fn();
    onIdle(run)();
    window.dispatchEvent(new Event("load"));
    vi.advanceTimersByTime(10_000);
    expect(run).not.toHaveBeenCalled();
  });

  it("with requestIdleCallback, runs from the idle callback, IDLE_WITHIN its timeout, and cancels it", () => {
    let idle: (() => void) | undefined;
    const ric = vi.fn<Ric>((cb) => {
      idle = cb;
      return 7;
    });
    const cic = vi.fn();
    withIdle(ric, cic);
    loading();
    const run = vi.fn();
    const cancel = onIdle(run);
    expect(ric).not.toHaveBeenCalled();
    window.dispatchEvent(new Event("load"));
    expect(ric).toHaveBeenCalledWith(run, { timeout: IDLE_WITHIN });
    vi.advanceTimersByTime(10_000);
    expect(run).not.toHaveBeenCalled();
    idle?.();
    expect(run).toHaveBeenCalledTimes(1);
    cancel();
    expect(cic).toHaveBeenCalledWith(7);
  });
});

describe("preloadable", () => {
  it("renders through React.lazy until its preload resolved, then the component itself; imports once", async () => {
    const Form = (p: { n: number }) => String(p.n);
    const load = vi.fn(() => Promise.resolve({ Form }));
    const C = preloadable(load, (m) => m.Form);
    expect(C({ n: 1 }).type).not.toBe(Form);
    await C.preload();
    const shown = C({ n: 2 });
    expect(shown.type).toBe(Form);
    expect(shown.props).toEqual({ n: 2 });
    await C.preload();
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("imports again after a failed preload", async () => {
    const Form = () => null;
    const load = vi.fn<() => Promise<{ Form: typeof Form }>>().mockRejectedValueOnce(new Error("offline")).mockResolvedValue({ Form });
    const C = preloadable(load, (m) => m.Form);
    await expect(C.preload()).rejects.toThrow("offline");
    expect(C({}).type).not.toBe(Form);
    await C.preload();
    expect(C({}).type).toBe(Form);
    expect(load).toHaveBeenCalledTimes(2);
  });
});
