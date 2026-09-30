import { isDecimal, round, type Rounding } from "./decimal";

// Display formatting of decimal strings (design §4.3): prices at the
// pair's tick decimals, amounts at the asset's, grouped thousands, a
// compact form for lists. Values stay strings; nothing is parsed into a
// float except for the compact form, which is approximate by nature.

const DASH = "—";

/** group inserts thousands separators into the integer part. */
export function group(v: string): string {
  const neg = v.startsWith("-");
  const [int = "0", frac] = (neg ? v.slice(1) : v).split(".");
  const grouped = int.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return (neg ? "-" : "") + grouped + (frac !== undefined ? `.${frac}` : "");
}

export type DecimalFormat = {
  /** Decimal places shown; padded with zeros. Omit to keep the value's own. */
  decimals?: number;
  /** How extra decimals go away (default down: never overstate). */
  rounding?: Rounding;
  /** Thousands separators (default on). */
  group?: boolean;
  /** A leading + on positive values. */
  sign?: boolean;
};

/** formatDecimal renders a decimal string; null, empty or invalid → "—". */
export function formatDecimal(v: string | null | undefined, f: DecimalFormat = {}): string {
  if (v === null || v === undefined || v === "" || !isDecimal(v.trim())) return DASH;
  let s = v.trim();
  if (f.decimals !== undefined) {
    s = round(s, f.decimals, f.rounding ?? "down");
    const [int, frac = ""] = s.split(".");
    s = f.decimals > 0 ? `${int}.${frac.padEnd(f.decimals, "0")}` : (int ?? "0");
  }
  if (f.group !== false) s = group(s);
  if (f.sign && !s.startsWith("-") && !/^0(\.0*)?$/.test(s.replaceAll(",", ""))) s = `+${s}`;
  return s;
}

/** formatPrice shows a price at the pair's price decimals. */
export function formatPrice(v: string | null | undefined, priceDecimals?: number): string {
  return formatDecimal(v, { decimals: priceDecimals, rounding: "half" });
}

/** formatAmount shows an amount at the asset's decimals, rounded down. */
export function formatAmount(v: string | null | undefined, decimals?: number): string {
  return formatDecimal(v, { decimals, rounding: "down" });
}

/**
 * formatPercent renders a fraction such as "0.01253" as "+1.25%".
 * decimals defaults to 2; sign adds "+" to gains (default on).
 */
export function formatPercent(fraction: string | null | undefined, decimals = 2, sign = true): string {
  if (fraction === null || fraction === undefined || fraction === "" || !isDecimal(fraction)) return DASH;
  const scaled = shiftDecimal(fraction, 2);
  return `${formatDecimal(scaled, { decimals, rounding: "half", group: false, sign })}%`;
}

// shiftDecimal multiplies by 10^n by moving the point.
function shiftDecimal(v: string, n: number): string {
  const neg = v.startsWith("-");
  const [int = "0", frac = ""] = (neg ? v.slice(1) : v).split(".");
  const digits = int + frac.padEnd(n, "0");
  const point = int.length + n;
  const whole = digits.slice(0, point).replace(/^0+(?=\d)/, "") || "0";
  const rest = digits.slice(point);
  return (neg ? "-" : "") + whole + (rest ? `.${rest}` : "");
}

const compactFormats = new Map<string, Intl.NumberFormat>();

/**
 * formatCompact shortens large values for lists: 1.23K / 4.56M in English,
 * 1.23万 / 4.56亿 in Chinese. Approximate by design.
 */
export function formatCompact(v: string | null | undefined, locale = "zh-CN", maxDecimals = 2): string {
  if (v === null || v === undefined || v === "" || !isDecimal(v)) return DASH;
  const key = `${locale}|${maxDecimals}`;
  let f = compactFormats.get(key);
  if (!f) {
    f = new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: maxDecimals });
    compactFormats.set(key, f);
  }
  return f.format(Number(v));
}
