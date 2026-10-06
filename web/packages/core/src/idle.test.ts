import { describe, expect, it, vi } from "vitest";
import { onIdle } from "./idle";

describe("onIdle", () => {
  it("runs once the browser is idle, unless cancelled first", () => {
    vi.useFakeTimers();
    const run = vi.fn();
    onIdle(run);
    const skipped = vi.fn();
    onIdle(skipped)();
    vi.advanceTimersByTime(5000);
    expect(run).toHaveBeenCalledTimes(1);
    expect(skipped).not.toHaveBeenCalled();
    vi.useRealTimers();
  });
});
