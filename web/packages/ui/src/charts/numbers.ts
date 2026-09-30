import { dec } from "@exchange/core";

/**
 * numToDecimal turns a drawn number (a moving average, a hovered value)
 * back into a decimal string at `decimals` places, without float
 * formatting: the value is scaled to an integer first. Only for labels of
 * computed chart values, never for amounts.
 */
export function numToDecimal(n: number, decimals: number): string {
  if (!Number.isFinite(n)) return "";
  const places = Math.max(0, Math.min(12, Math.floor(decimals)));
  const scale = 10 ** places;
  const int = Math.round(n * scale);
  if (!Number.isSafeInteger(int)) return String(Math.round(n));
  return places === 0 ? String(int) : dec.div(String(int), String(scale), places);
}
