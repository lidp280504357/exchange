import { computeIndicators, type IndicatorRequest, type IndicatorResult } from "./indicators";

export type IndicatorClient = {
  /** compute resolves with the moving averages of one request. */
  compute: (req: Omit<IndicatorRequest, "id">) => Promise<IndicatorResult>;
  /** Whether the work runs in a Web Worker (false: synchronous fallback). */
  readonly threaded: boolean;
  dispose: () => void;
};

/**
 * createIndicatorClient starts the indicators worker, or computes inline
 * where workers are unavailable (tests, old WebViews) or fail to start.
 */
export function createIndicatorClient(): IndicatorClient {
  let worker: Worker | null = null;
  try {
    if (typeof Worker !== "undefined") {
      worker = new Worker(new URL("./indicators.worker.ts", import.meta.url), { type: "module" });
    }
  } catch {
    worker = null;
  }
  let seq = 0;
  const pending = new Map<number, { req: IndicatorRequest; done: (r: IndicatorResult) => void }>();

  const inline = (req: IndicatorRequest) => Promise.resolve(computeIndicators(req));

  if (worker) {
    worker.onmessage = (e: MessageEvent<IndicatorResult>) => {
      const p = pending.get(e.data.id);
      pending.delete(e.data.id);
      p?.done(e.data);
    };
    // A worker that dies hands its pending work, and all later work, to the main thread.
    worker.onerror = () => {
      worker?.terminate();
      worker = null;
      for (const p of pending.values()) p.done(computeIndicators(p.req));
      pending.clear();
    };
  }

  return {
    get threaded() {
      return worker !== null;
    },
    compute(req) {
      const full: IndicatorRequest = { ...req, id: ++seq };
      if (!worker) return inline(full);
      return new Promise((resolve) => {
        pending.set(full.id, { req: full, done: resolve });
        worker?.postMessage(full);
      });
    },
    dispose() {
      worker?.terminate();
      worker = null;
      pending.clear();
    },
  };
}
