// Moving averages of the candle chart (design §4.4: computed off the main
// thread in indicators.worker.ts; this module is shared by the worker and
// the synchronous fallback). Numbers, not decimals: these only draw lines.

export type IndicatorRequest = {
  id: number;
  /** Closing prices, oldest first. */
  closes: number[];
  /** Periods of the simple moving averages, e.g. [7, 25, 99]. */
  ma: number[];
  /** Periods of the exponential moving averages, e.g. [12, 26]. */
  ema: number[];
};

export type IndicatorResult = {
  id: number;
  /** One series per MA period; null until the period is filled. */
  ma: (number | null)[][];
  ema: (number | null)[][];
};

/** sma is the simple moving average with a running sum, O(n). */
export function sma(values: readonly number[], period: number): (number | null)[] {
  const out: (number | null)[] = new Array(values.length).fill(null);
  if (period <= 0) return out;
  let sum = 0;
  for (let i = 0; i < values.length; i++) {
    sum += values[i] ?? 0;
    if (i >= period) sum -= values[i - period] ?? 0;
    if (i >= period - 1) out[i] = sum / period;
  }
  return out;
}

/** ema is the exponential moving average, seeded with the SMA of the first period. */
export function ema(values: readonly number[], period: number): (number | null)[] {
  const out: (number | null)[] = new Array(values.length).fill(null);
  if (period <= 0 || values.length < period) return out;
  const k = 2 / (period + 1);
  let prev = 0;
  for (let i = 0; i < period; i++) prev += values[i] ?? 0;
  prev /= period;
  out[period - 1] = prev;
  for (let i = period; i < values.length; i++) {
    prev = (values[i] ?? 0) * k + prev * (1 - k);
    out[i] = prev;
  }
  return out;
}

/** computeIndicators answers one request (in the worker or inline). */
export function computeIndicators(req: IndicatorRequest): IndicatorResult {
  return {
    id: req.id,
    ma: req.ma.map((p) => sma(req.closes, p)),
    ema: req.ema.map((p) => ema(req.closes, p)),
  };
}
