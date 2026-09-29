import { mul } from "./market";

// Perpetual contracts in the H5: amounts stay decimal strings (ADR-0008);
// the few figures computed here (an order's cost estimate) use exact
// integer arithmetic.

// countdown renders the time left until an ISO time as hh:mm:ss.
export function countdown(until: string | null | undefined, now: number): string {
  if (!until) return "—";
  const left = Math.max(0, Math.floor((Date.parse(until) - now) / 1000));
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(Math.floor(left / 3600))}:${pad(Math.floor((left % 3600) / 60))}:${pad(left % 60)}`;
}

function toScaled(v: string, dp: number): bigint {
  const neg = v.startsWith("-");
  const [int = "0", frac = ""] = v.replace(/^[-+]/, "").split(".");
  const digits = BigInt(int + frac.padEnd(dp, "0").slice(0, dp) || "0");
  return neg ? -digits : digits;
}

function fromScaled(v: bigint, dp: number): string {
  const neg = v < 0n;
  let s = (neg ? -v : v).toString().padStart(dp + 1, "0");
  if (dp > 0) s = `${s.slice(0, -dp)}.${s.slice(-dp)}`.replace(/\.?0+$/, "");
  return (neg ? "-" : "") + s;
}

// divCeil divides a non-negative decimal string by a positive integer,
// rounded up to dp decimals.
export function divCeil(a: string, n: number, dp: number): string {
  const x = toScaled(a, dp);
  const d = BigInt(n);
  return fromScaled((x + d - 1n) / d, dp);
}

// addDecimals adds decimal strings exactly at dp decimals (cut).
export function addDecimals(dp: number, ...values: string[]): string {
  return fromScaled(values.reduce((sum, v) => sum + toScaled(v, dp), 0n), dp);
}

// orderCost estimates what an opening order reserves: its initial margin
// price x quantity / leverage and the taker fee, rounded up to dp.
export function orderCost(price: string, qty: string, leverage: number, takerRate: string, dp: number): string {
  const notional = mul(price, qty);
  const margin = divCeil(notional, Math.max(1, leverage), dp);
  const fee = divCeil(mul(notional, takerRate), 1, dp);
  return addDecimals(dp, margin, fee);
}

// pnlTone colors a signed amount: green gains, red losses.
export function pnlTone(v: string | null | undefined): string {
  if (!v || /^-?0(\.0+)?$/.test(v)) return "text-gray-300";
  return v.startsWith("-") ? "text-red-400" : "text-emerald-400";
}

// signed prefixes a positive amount with "+".
export function signed(v: string): string {
  return v.startsWith("-") || /^0(\.0+)?$/.test(v) ? v : `+${v}`;
}

// ratePercent renders a funding rate such as "0.0001" as "0.0100%".
export function ratePercent(rate: string | null | undefined): string {
  if (!rate) return "—";
  const neg = rate.startsWith("-");
  const scaled = toScaled(rate.replace(/^-/, ""), 6); // rate x 10^6 = percent x 10^4
  const s = scaled.toString().padStart(5, "0");
  return `${neg ? "-" : ""}${s.slice(0, -4)}.${s.slice(-4)}%`;
}
