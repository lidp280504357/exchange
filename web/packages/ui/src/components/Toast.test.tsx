import "../test/setup";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TOAST_DURATION, TOAST_LIMIT, Toaster, ToastStore } from "./Toast";

describe("ToastStore", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("adds toasts and removes them after 4 seconds", () => {
    const store = new ToastStore();
    const id = store.add({ title: "Order placed", tone: "success" });
    expect(store.getSnapshot()).toHaveLength(1);
    expect(store.getSnapshot()[0]).toMatchObject({ id, title: "Order placed", tone: "success", duration: TOAST_DURATION });
    vi.advanceTimersByTime(TOAST_DURATION - 1);
    expect(store.getSnapshot()).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(store.getSnapshot()).toHaveLength(0);
  });

  it("defaults to the info tone and keeps duration 0 until dismissed", () => {
    const store = new ToastStore();
    const id = store.add({ title: "Sticky", duration: 0 });
    expect(store.getSnapshot()[0]?.tone).toBe("info");
    vi.advanceTimersByTime(60_000);
    expect(store.getSnapshot()).toHaveLength(1);
    store.dismiss(id);
    expect(store.getSnapshot()).toHaveLength(0);
  });

  it("pauses the clock while hovered and resumes with the time left", () => {
    const store = new ToastStore();
    const id = store.add({ title: "Hover me" });
    vi.advanceTimersByTime(3000);
    store.pause(id);
    vi.advanceTimersByTime(10_000);
    expect(store.getSnapshot()).toHaveLength(1);
    store.resume(id);
    vi.advanceTimersByTime(999);
    expect(store.getSnapshot()).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(store.getSnapshot()).toHaveLength(0);
  });

  it("replaces a toast with the same id in place and restarts its clock", () => {
    const store = new ToastStore();
    const id = store.add({ title: "Submitting…", duration: 0 });
    store.add({ title: "Other" });
    store.add({ id, title: "Submitted", tone: "success" });
    const list = store.getSnapshot();
    expect(list.map((t) => t.title)).toEqual(["Submitted", "Other"]);
    vi.advanceTimersByTime(TOAST_DURATION);
    expect(store.getSnapshot()).toHaveLength(0);
  });

  it("keeps at most the last few toasts and notifies subscribers", () => {
    const store = new ToastStore();
    const seen = vi.fn();
    const off = store.subscribe(seen);
    for (let i = 0; i < TOAST_LIMIT + 2; i++) store.add({ title: `T${i}` });
    expect(store.getSnapshot().map((t) => t.title)).toEqual(Array.from({ length: TOAST_LIMIT }, (_, i) => `T${i + 2}`));
    expect(seen).toHaveBeenCalledTimes(TOAST_LIMIT + 2);
    store.dismiss();
    expect(store.getSnapshot()).toHaveLength(0);
    off();
    store.add({ title: "Unseen" });
    expect(seen).toHaveBeenCalledTimes(TOAST_LIMIT + 3);
  });
});

describe("Toaster", () => {
  it("renders the store's toasts with their action and dismiss button", () => {
    const store = new ToastStore();
    const onAction = vi.fn();
    render(<Toaster store={store} />);
    act(() => {
      store.add({ title: "Order placed", description: "Buy 0.01 BTC", tone: "success", action: { label: "View orders", onClick: onAction } });
    });
    expect(screen.getByText("Order placed")).toBeTruthy();
    expect(screen.getByRole("status").textContent).toContain("Buy 0.01 BTC");
    fireEvent.click(screen.getByRole("button", { name: "View orders" }));
    expect(onAction).toHaveBeenCalled();
    expect(store.getSnapshot()).toHaveLength(0);

    act(() => {
      store.add({ title: "Insufficient balance", tone: "error", duration: 0 });
    });
    expect(screen.getByRole("alert").textContent).toContain("Insufficient balance");
    // The first toast may still be leaving (exit animation): use the alert's own button.
    act(() => {
      fireEvent.click(within(screen.getByRole("alert")).getByRole("button", { name: "Dismiss" }));
    });
    expect(store.getSnapshot()).toHaveLength(0);
  });
});
