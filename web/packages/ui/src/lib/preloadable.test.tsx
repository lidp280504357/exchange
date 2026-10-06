import "../test/setup";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Component, Suspense, useState, type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { preloadable } from "./preloadable";

const Form = ({ n }: { n: number }) => <p>form {n}</p>;

function Amount() {
  const [v, setV] = useState("");
  return <input aria-label="amount" value={v} onChange={(e) => setV(e.target.value)} />;
}

class Boundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? <p>failed</p> : this.props.children;
  }
}

describe("preloadable", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders at once once preloaded, without the fallback, and imports once", async () => {
    const load = vi.fn(() => Promise.resolve({ Form }));
    const C = preloadable(load, (m) => m.Form);
    await C.preload();
    render(
      <Suspense fallback={<p>loading</p>}>
        <C n={1} />
      </Suspense>,
    );
    expect(screen.getByText("form 1")).toBeTruthy();
    expect(screen.queryByText("loading")).toBeNull();
    await C.preload();
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("gives the one element type before and after its import, so nothing mounted is remounted", async () => {
    const C = preloadable(() => Promise.resolve({ Form }), (m) => m.Form);
    const before = C({ n: 1 }).type;
    await C.preload();
    expect(C({ n: 1 }).type).toBe(before);
  });

  it("mounted before its import ended, suspends, then keeps its state across the parent's renders", async () => {
    let resolve: (m: { Amount: typeof Amount }) => void = () => {};
    const C = preloadable(() => new Promise<{ Amount: typeof Amount }>((r) => (resolve = r)), (m) => m.Amount);
    const Parent = ({ tick }: { tick: number }) => (
      <Suspense fallback={<p>loading</p>}>
        <C />
        <span>tick {tick}</span>
      </Suspense>
    );
    const { rerender } = render(<Parent tick={0} />);
    expect(screen.getByText("loading")).toBeTruthy();
    await act(async () => resolve({ Amount }));
    fireEvent.change(await screen.findByLabelText("amount"), { target: { value: "12" } });
    rerender(<Parent tick={1} />);
    expect(screen.getByText("tick 1")).toBeTruthy();
    expect((screen.getByLabelText("amount") as HTMLInputElement).value).toBe("12");
  });

  it("imports again after a failed preload", async () => {
    const load = vi.fn<() => Promise<{ Form: typeof Form }>>().mockRejectedValueOnce(new Error("offline")).mockResolvedValue({ Form });
    const C = preloadable(load, (m) => m.Form);
    await expect(C.preload()).rejects.toThrow("offline");
    render(
      <Suspense fallback={<p>loading</p>}>
        <C n={3} />
      </Suspense>,
    );
    expect(await screen.findByText("form 3")).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("is not stuck with an import that failed while rendering", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const load = vi.fn<() => Promise<{ Form: typeof Form }>>().mockRejectedValueOnce(new Error("offline")).mockResolvedValue({ Form });
    const C = preloadable(load, (m) => m.Form);
    const page = (
      <Boundary>
        <Suspense fallback={<p>loading</p>}>
          <C n={4} />
        </Suspense>
      </Boundary>
    );
    // React 19 renders once more after an error, which takes the fresh lazy
    // component already; a boundary that caught the failure recovers on the
    // next mount.
    const first = render(page);
    await waitFor(() => expect(screen.queryByText("form 4") ?? screen.queryByText("failed")).toBeTruthy());
    if (screen.queryByText("failed")) {
      first.unmount();
      render(page);
    }
    expect(await screen.findByText("form 4")).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(2);
  });
});
