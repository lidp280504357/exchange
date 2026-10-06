import { createElement, lazy, useEffect, type ComponentType, type ReactElement } from "react";

// Chunks a page will likely need soon but not on its first screen (a
// dialog's form) load once the page has loaded and the browser is idle:
// off the first screen, yet ready by the time the user opens them.

type IdleWindow = Window & {
  requestIdleCallback?: (cb: () => void, opts?: { timeout: number }) => number;
  cancelIdleCallback?: (id: number) => void;
};

/** The longest onIdle waits after the page's load event (all of the wait where there is no requestIdleCallback). */
export const IDLE_WITHIN = 3000;

/**
 * onIdle runs run once the page has loaded (its load event; at once when
 * it already has, as after a route change) and then the browser is idle:
 * requestIdleCallback with IDLE_WITHIN as its timeout, or IDLE_WITHIN
 * itself where there is none (Safari, iPhones included), which keeps it
 * out of the first screen's window there too. Returns the cancel.
 */
export function onIdle(run: () => void): () => void {
  const w = window as IdleWindow;
  let cancel = () => {};
  const idle = () => {
    if (w.requestIdleCallback && w.cancelIdleCallback) {
      const id = w.requestIdleCallback(run, { timeout: IDLE_WITHIN });
      cancel = () => w.cancelIdleCallback?.(id);
    } else {
      const id = window.setTimeout(run, IDLE_WITHIN);
      cancel = () => window.clearTimeout(id);
    }
  };
  if (document.readyState === "complete") idle();
  else {
    window.addEventListener("load", idle, { once: true });
    cancel = () => window.removeEventListener("load", idle);
  }
  return () => cancel();
}

/**
 * useIdleImport calls load (a dynamic import) once the page has loaded and
 * the browser is idle (onIdle) while enabled; an import already done
 * resolves at once, so calling it again costs nothing.
 */
export function useIdleImport(load: () => Promise<unknown>, enabled = true): void {
  useEffect(() => (enabled ? onIdle(() => void load().catch(() => {})) : undefined), [load, enabled]);
}

/** A component in a chunk of its own, with the import that preloads it. */
export type Preloadable<P> = ((props: P) => ReactElement) & {
  /** preload imports the chunk (once; again after a failure). */
  preload: () => Promise<unknown>;
};

/**
 * preloadable is React.lazy over the component pick takes from the module
 * load imports, rendered at once once its preload has resolved
 * (useIdleImport(C.preload)). A lazy component suspends on its first
 * render even with its chunk in, and React holds a Suspense boundary's
 * content back until 300 ms after its fallback showed (its reveal
 * throttle), so a lazy dialog took a third of a second to open the first
 * time however early its chunk had come (B114).
 */
export function preloadable<M, P extends object>(load: () => Promise<M>, pick: (m: M) => ComponentType<P>): Preloadable<P> {
  let loaded: ComponentType<P> | undefined;
  let pending: Promise<M> | undefined;
  const preload = () =>
    (pending ??= load().then(
      (m) => {
        loaded = pick(m);
        return m;
      },
      (e: unknown) => {
        pending = undefined;
        throw e;
      },
    ));
  const Lazy = lazy(() => preload().then((m) => ({ default: pick(m) })));
  const Component = (props: P) => (loaded ? createElement(loaded, props) : createElement(Lazy, props));
  return Object.assign(Component, { preload });
}
