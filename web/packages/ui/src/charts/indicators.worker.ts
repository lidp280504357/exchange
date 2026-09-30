import { computeIndicators, type IndicatorRequest } from "./indicators";

// The indicators worker: moving averages of thousands of candles never
// block the main thread, which only draws (design §4.4).

type WorkerScope = {
  onmessage: ((e: MessageEvent<IndicatorRequest>) => void) | null;
  postMessage: (message: unknown) => void;
};

const scope = self as unknown as WorkerScope;

scope.onmessage = (e) => {
  scope.postMessage(computeIndicators(e.data));
};
