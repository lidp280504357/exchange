// Amounts are decimal strings end to end (ADR-0008); the client never
// parses them into floating point.

const DECIMAL = /^(0|[1-9]\d*)(\.\d+)?$/;

// checkAmount validates user input against an asset's precision.
export function checkAmount(input: string, decimals: number): "ok" | "format" | "zero" | "precision" {
  const s = input.trim();
  if (!DECIMAL.test(s)) return "format";
  const [, frac = ""] = s.split(".");
  if (frac.length > decimals) return "precision";
  if (/^0(\.0+)?$/.test(s)) return "zero";
  return "ok";
}

// compare compares two non-negative decimal strings.
export function compare(a: string, b: string): number {
  const [ai = "0", af = ""] = a.split(".");
  const [bi = "0", bf = ""] = b.split(".");
  const ia = ai.replace(/^0+(?=\d)/, "");
  const ib = bi.replace(/^0+(?=\d)/, "");
  if (ia.length !== ib.length) return ia.length < ib.length ? -1 : 1;
  if (ia !== ib) return ia < ib ? -1 : 1;
  const n = Math.max(af.length, bf.length);
  const fa = af.padEnd(n, "0");
  const fb = bf.padEnd(n, "0");
  return fa === fb ? 0 : fa < fb ? -1 : 1;
}

// format groups the integer part and pads or cuts nothing: the server
// already returns exact values.
export function format(value: string): string {
  const [int = "0", frac] = value.split(".");
  const neg = int.startsWith("-");
  const digits = neg ? int.slice(1) : int;
  const grouped = digits.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return (neg ? "-" : "") + grouped + (frac ? "." + frac : "");
}
