import { useEffect } from "react";

// Chunks a page will likely need soon but not on its first screen (a
// dialog's form) load once the browser is idle: off the first screen, yet
// ready by the time the user opens them.

type IdleWindow = Window & {
  requestIdleCallback?: (cb: () => void, opts?: { timeout: number }) => number;
  cancelIdleCallback?: (id: number) => void;
};

/** onIdle runs run once the browser is idle (a few seconds at most); returns the cancel. */
export function onIdle(run: () => void): () => void {
  const w = window as IdleWindow;
  if (w.requestIdleCallback && w.cancelIdleCallback) {
    const id = w.requestIdleCallback(run, { timeout: 3000 });
    return () => w.cancelIdleCallback?.(id);
  }
  const id = window.setTimeout(run, 1500);
  return () => window.clearTimeout(id);
}

/**
 * useIdleImport calls load (a dynamic import) once the browser is idle
 * while enabled; an import already done resolves at once, so calling it
 * again costs nothing.
 */
export function useIdleImport(load: () => Promise<unknown>, enabled = true): void {
  useEffect(() => (enabled ? onIdle(() => void load().catch(() => {})) : undefined), [load, enabled]);
}
