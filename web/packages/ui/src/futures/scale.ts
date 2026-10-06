import { formatDecimal } from "@exchange/core";

// The arithmetic of SeriesChart: its y domain by chart form, round ticks,
// how a tick reads, columns with a rounded data end, and the point under
// the pointer. Pure, so it is tested without a browser.

export type SeriesForm =
  | { kind: "line"; key: string; area?: boolean }
  | { kind: "columns"; key: string }
  | { kind: "share"; up: string; down: string }
  | { kind: "mirror"; up: string; down: string };

export type SeriesPoint = { t: number; v: Record<string, number> };

export type AxisStyle = "compact" | "percent" | "plain";

/**
 * seriesDomain is the range of values a form draws: a line's lowest to
 * highest value; columns and mirrored columns from zero (mirrored ones
 * symmetric); shares 0 to 1.
 */
export function seriesDomain(form: SeriesForm, points: readonly SeriesPoint[]): [number, number] {
  const of = (key: string) => points.map((p) => p.v[key]).filter((v): v is number => v !== undefined && Number.isFinite(v));
  switch (form.kind) {
    case "share":
      return [0, 1];
    case "mirror": {
      const m = Math.max(0, ...of(form.up), ...of(form.down));
      return [-m, m];
    }
    case "columns": {
      const vs = of(form.key);
      return [Math.min(0, ...vs), Math.max(0, ...vs)];
    }
    case "line": {
      const vs = of(form.key);
      return vs.length ? [Math.min(...vs), Math.max(...vs)] : [0, 0];
    }
  }
}

/** niceStep rounds a step up to 1, 2, 2.5 or 5 times a power of ten. */
export function niceStep(raw: number): number {
  if (!(raw > 0) || !Number.isFinite(raw)) return 1;
  const p = 10 ** Math.floor(Math.log10(raw));
  const m = raw / p;
  return (m <= 1 ? 1 : m <= 2 ? 2 : m <= 2.5 ? 2.5 : m <= 5 ? 5 : 10) * p;
}

export type Ticks = { min: number; max: number; step: number; ticks: number[] };

/**
 * niceTicks spreads about `count` round ticks over [lo, hi], widened to
 * whole steps; a flat range (one value) is opened around its value, and a
 * line's range gets a little air above and below.
 */
export function niceTicks(lo: number, hi: number, count = 4, pad = 0): Ticks {
  let a = Math.min(lo, hi);
  let b = Math.max(lo, hi);
  if (!Number.isFinite(a) || !Number.isFinite(b)) [a, b] = [0, 1];
  if (a === b) {
    const d = Math.abs(a) * 0.01 || 1;
    [a, b] = [a - d, b + d];
  } else if (pad > 0) {
    const d = (b - a) * pad;
    [a, b] = [a - d, b + d];
  }
  const step = niceStep((b - a) / Math.max(1, count - 1));
  // Rounding the bounds to whole steps (and the ticks below) keeps float
  // noise such as 0.30000000000000004 out of the labels.
  const fix = (v: number) => Number(v.toPrecision(12));
  const min = fix(Math.floor(a / step) * step);
  const max = fix(Math.ceil(b / step) * step);
  const ticks: number[] = [];
  for (let v = min; v <= max + step / 2; v += step) ticks.push(fix(v));
  return { min, max, step, ticks };
}

/** tickDecimals is how many decimals a tick needs at its step (as shown: percentages are the step × 100). */
export function tickDecimals(step: number, percent = false): number {
  const s = percent ? step * 100 : step;
  if (!(s > 0)) return 0;
  return Math.max(0, Math.ceil(-Math.log10(s) - 1e-9) + (s / 10 ** Math.floor(Math.log10(s)) === 2.5 ? 1 : 0));
}

function plainTick(v: number, decimals: number): string {
  return formatDecimal(v.toFixed(Math.min(decimals + 2, 12)), { decimals, rounding: "half" });
}

const compacts = new Map<string, Intl.NumberFormat>();

// compactTick writes a large tick short (1258万, 12.58M) with exactly `decimals` decimals.
function compactTick(v: number, decimals: number, locale: string): string {
  const key = `${locale}|${decimals}`;
  let f = compacts.get(key);
  if (!f) {
    f = new Intl.NumberFormat(locale, { notation: "compact", minimumFractionDigits: decimals, maximumFractionDigits: decimals });
    compacts.set(key, f);
  }
  return f.format(v);
}

/** Plain tick labels up to this long stay plain in a compact axis (96,100; -400,000). */
const PLAIN_MAX = 8;

/**
 * formatTicks renders a scale's ticks: as percentages or plain numbers at
 * the decimals the step needs; a compact axis writes its ticks in full
 * while they are short (96,100 / 96,150), and short (1256万, 12.56M) with
 * the fewest decimals, the same for every tick, that keep them apart.
 */
export function formatTicks(ticks: readonly number[], step: number, style: AxisStyle, locale: string): string[] {
  if (style === "percent") {
    const d = tickDecimals(step, true);
    return ticks.map((v) => `${formatDecimal((v * 100).toFixed(Math.min(d + 2, 12)), { decimals: d, rounding: "half", group: false })}%`);
  }
  const plain = ticks.map((v) => plainTick(v, tickDecimals(step)));
  if (style === "plain" || Math.max(...plain.map((l) => l.length)) <= PLAIN_MAX) return plain;
  for (const decimals of [0, 1, 2, 3]) {
    const compact = ticks.map((v) => compactTick(v, decimals, locale));
    if (new Set(compact).size === compact.length) return compact;
  }
  return plain;
}

/**
 * columnPath draws a column from the baseline y0 to the value's end y1,
 * width w centred on x, with the data end rounded (radius r, at most half
 * the width or the height) and the baseline end square.
 */
export function columnPath(x: number, y0: number, y1: number, w: number, r = 4): string {
  const h = Math.abs(y1 - y0);
  const left = x - w / 2;
  const right = x + w / 2;
  if (h === 0 || w <= 0) return "";
  const rr = Math.max(0, Math.min(r, w / 2, h));
  const up = y1 < y0;
  const end = up ? y1 + rr : y1 - rr;
  const sweep = up ? 1 : 0;
  return [
    `M${left},${y0}`,
    `L${left},${end}`,
    `A${rr},${rr} 0 0 ${sweep} ${left + rr},${y1}`,
    `L${right - rr},${y1}`,
    `A${rr},${rr} 0 0 ${sweep} ${right},${end}`,
    `L${right},${y0}`,
    "Z",
  ].join(" ");
}

/** indexAt is the point nearest to an x offset in a plot of n points spread over width (null outside it). */
export function indexAt(x: number, width: number, n: number): number | null {
  if (n <= 0 || width <= 0 || x < 0 || x > width) return null;
  return Math.min(n - 1, Math.max(0, Math.floor((x / width) * n)));
}

/** labelIndexes picks the points whose times label the axis: about one per `every` px, the last always. */
export function labelIndexes(n: number, width: number, every = 80): number[] {
  if (n <= 0) return [];
  const count = Math.max(1, Math.min(n, Math.floor(width / every)));
  if (count === 1) return [n - 1];
  const step = (n - 1) / (count - 1);
  const out = new Set<number>();
  for (let i = 0; i < count; i++) out.add(Math.round(n - 1 - i * step));
  return [...out].sort((a, b) => a - b);
}
