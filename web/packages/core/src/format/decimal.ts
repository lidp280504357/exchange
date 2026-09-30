// Amounts are decimal strings end to end (ADR-0008): arithmetic here is
// exact, on scaled BigInts, never on floating point. toNumber exists only
// for drawing (charts, bars), where a float's error cannot reach the user
// as an amount.

const DECIMAL = /^-?(0|[1-9]\d*)(\.\d+)?$/;
const INPUT = /^(0|[1-9]\d*)(\.\d+)?$/;

/** isDecimal reports whether v is a decimal string such as "-12.340". */
export function isDecimal(v: string): boolean {
  return DECIMAL.test(v);
}

type Scaled = { digits: bigint; scale: number };

function parse(v: string): Scaled {
  const s = v.trim();
  if (!/^-?\d+(\.\d+)?$/.test(s)) throw new Error(`not a decimal: ${v}`);
  const neg = s.startsWith("-");
  const [int = "0", frac = ""] = (neg ? s.slice(1) : s).split(".");
  const digits = BigInt(int + frac);
  return { digits: neg ? -digits : digits, scale: frac.length };
}

function align(a: Scaled, b: Scaled): [bigint, bigint, number] {
  const scale = Math.max(a.scale, b.scale);
  return [a.digits * 10n ** BigInt(scale - a.scale), b.digits * 10n ** BigInt(scale - b.scale), scale];
}

function toString(digits: bigint, scale: number): string {
  const neg = digits < 0n;
  let s = (neg ? -digits : digits).toString().padStart(scale + 1, "0");
  if (scale > 0) s = `${s.slice(0, -scale)}.${s.slice(-scale)}`.replace(/\.?0+$/, "");
  return s === "0" ? "0" : (neg ? "-" : "") + s;
}

/** add returns a + b. */
export function add(a: string, b: string): string {
  const [x, y, scale] = align(parse(a), parse(b));
  return toString(x + y, scale);
}

/** sub returns a - b. */
export function sub(a: string, b: string): string {
  const [x, y, scale] = align(parse(a), parse(b));
  return toString(x - y, scale);
}

/** mul returns a × b exactly. */
export function mul(a: string, b: string): string {
  const x = parse(a);
  const y = parse(b);
  return toString(x.digits * y.digits, x.scale + y.scale);
}

export type Rounding = "down" | "up" | "half";

// roundDigits divides digits by 10^drop, rounding toward zero ("down"),
// away from zero ("up") or half away from zero ("half").
function roundDigits(digits: bigint, drop: number, mode: Rounding): bigint {
  if (drop <= 0) return digits * 10n ** BigInt(-drop);
  const unit = 10n ** BigInt(drop);
  const neg = digits < 0n;
  const abs = neg ? -digits : digits;
  let q = abs / unit;
  const r = abs % unit;
  if ((mode === "up" && r > 0n) || (mode === "half" && r * 2n >= unit)) q += 1n;
  return neg ? -q : q;
}

/** div returns a / b to `scale` decimals, rounded by mode (default down). */
export function div(a: string, b: string, scale: number, mode: Rounding = "down"): string {
  const x = parse(a);
  const y = parse(b);
  if (y.digits === 0n) throw new Error("division by zero");
  // a / b = x.digits × 10^(y.scale − x.scale + scale) / y.digits, at scale.
  let num = x.digits;
  let den = y.digits;
  const shift = y.scale - x.scale + scale;
  if (shift >= 0) num *= 10n ** BigInt(shift);
  else den *= 10n ** BigInt(-shift);
  const negative = num < 0n !== den < 0n;
  if (num < 0n) num = -num;
  if (den < 0n) den = -den;
  let q = num / den;
  const r = num % den;
  if ((mode === "up" && r > 0n) || (mode === "half" && r * 2n >= den)) q += 1n;
  return toString(negative ? -q : q, scale);
}

/** cmp compares two decimal strings: -1, 0 or 1. */
export function cmp(a: string, b: string): -1 | 0 | 1 {
  const [x, y] = align(parse(a), parse(b));
  return x < y ? -1 : x > y ? 1 : 0;
}

export const gt = (a: string, b: string) => cmp(a, b) > 0;
export const gte = (a: string, b: string) => cmp(a, b) >= 0;
export const lt = (a: string, b: string) => cmp(a, b) < 0;
export const lte = (a: string, b: string) => cmp(a, b) <= 0;
export const eq = (a: string, b: string) => cmp(a, b) === 0;

/** sign returns -1, 0 or 1. */
export function sign(v: string): -1 | 0 | 1 {
  const d = parse(v).digits;
  return d < 0n ? -1 : d > 0n ? 1 : 0;
}

export const isZero = (v: string) => sign(v) === 0;
export const neg = (v: string) => toString(-parse(v).digits, parse(v).scale);
export const abs = (v: string) => (sign(v) < 0 ? neg(v) : normalize(v));
export const max = (a: string, b: string) => (cmp(a, b) >= 0 ? a : b);
export const min = (a: string, b: string) => (cmp(a, b) <= 0 ? a : b);

/** normalize drops trailing zeros: "1.2300" → "1.23", "-0.0" → "0". */
export function normalize(v: string): string {
  const x = parse(v);
  return toString(x.digits, x.scale);
}

/** round keeps `decimals` places (default rounding down). */
export function round(v: string, decimals: number, mode: Rounding = "down"): string {
  const x = parse(v);
  return toString(roundDigits(x.digits, x.scale - decimals, mode), decimals);
}

/** decimalsOf counts the decimals a step such as "0.0100" allows (2). */
export function decimalsOf(step: string): number {
  const frac = step.split(".")[1] ?? "";
  return frac.replace(/0+$/, "").length;
}

/** quantize moves v down (or up) to a multiple of step, e.g. a lot size. */
export function quantize(v: string, step: string, mode: Exclude<Rounding, "half"> = "down"): string {
  const [x, s, scale] = align(parse(v), parse(step));
  if (s === 0n) return normalize(v);
  let q = x / s;
  if (mode === "up" && x % s !== 0n) q += x > 0n ? 1n : 0n;
  return toString(q * s, scale);
}

/** isMultipleOf reports whether v is a whole multiple of step. */
export function isMultipleOf(v: string, step: string): boolean {
  const [x, s] = align(parse(v), parse(step));
  return s !== 0n && x % s === 0n;
}

export type AmountCheck = "ok" | "format" | "zero" | "precision";

/** checkAmount validates user input: a positive decimal within decimals. */
export function checkAmount(input: string, decimals: number): AmountCheck {
  const s = input.trim();
  if (!INPUT.test(s)) return "format";
  const frac = s.split(".")[1] ?? "";
  if (frac.length > decimals) return "precision";
  if (/^0(\.0+)?$/.test(s)) return "zero";
  return "ok";
}

/** toNumber is for drawing only (charts, bars): never for amounts. */
export function toNumber(v: string | null | undefined): number {
  if (!v) return 0;
  const n = Number(v);
  return Number.isFinite(n) ? n : 0;
}
