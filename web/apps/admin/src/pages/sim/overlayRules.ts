import { dec } from "@exchange/core";

// The rules of a price event on any pair as the console checks them before
// market-sim does (design 2026-10-07, general price control, J3; J0
// contract §3.1): 1 to 10 pairs, a target price for one pair or a share of
// each pair's reference price, ±90% at most, three times in seconds within
// 10 minutes.

export const OVERLAY_MAX_PAIRS = 10;
export const OVERLAY_MAX_SECONDS = 600;
export const OVERLAY_MIN_DOWN = 3;
export const OVERLAY_MAX_PCT = 90;

/** OverlayDraft is the form as typed. */
export type OverlayDraft = {
  symbols: string[];
  mode: "pct" | "price";
  pct: string;
  price: string;
  up: string;
  hold: string;
  down: string;
  /** spare: 不连带合约与杠杆 (risk false); unticked by default (user 2026-10-07 03:0x). */
  spare: boolean;
};

export const newOverlayDraft = (): OverlayDraft => ({ symbols: [], mode: "pct", pct: "1", price: "", up: "15", hold: "0", down: "5", spare: false });

/** OverlayBody is the event as the console's API takes it, less the reason and the start. */
export type OverlayBody = {
  type: "OVERLAY";
  symbols: string[];
  target_pct?: number;
  target_price?: string;
  ramp_up_seconds: number;
  hold_seconds?: number;
  ramp_down_seconds: number;
  risk: boolean;
};

/** OverlayProblem names what is wrong with a draft: a key of admin.sim.overlay.bad and its values. */
export type OverlayProblem = { problem: "pairs" | "onePrice" | "price" | "tooFar" | "pct" | "seconds" | "total"; vars?: Record<string, number> };

const whole = (v: string) => /^\d+$/.test(v.trim());

/**
 * overlayBody checks a draft and makes its body, or says what is wrong
 * first; a target price is held to ±90% of the pair's price when lasts has
 * it, as a percent is (A81).
 */
export function overlayBody(d: OverlayDraft, lasts?: Record<string, string>): { body: OverlayBody } | OverlayProblem {
  if (d.symbols.length === 0 || d.symbols.length > OVERLAY_MAX_PAIRS) return { problem: "pairs", vars: { max: OVERLAY_MAX_PAIRS } };
  const body: OverlayBody = { type: "OVERLAY", symbols: [...d.symbols], ramp_up_seconds: 0, ramp_down_seconds: 0, risk: !d.spare };
  if (d.mode === "price") {
    if (d.symbols.length !== 1) return { problem: "onePrice" };
    const p = d.price.trim();
    if (!dec.isDecimal(p) || !dec.gt(p, "0")) return { problem: "price" };
    const move = moveOf(p, lasts?.[d.symbols[0] ?? ""]);
    if (move !== null && Math.abs(move) > OVERLAY_MAX_PCT / 100) return { problem: "tooFar", vars: { max: OVERLAY_MAX_PCT } };
    body.target_price = p;
  } else {
    const p = d.pct.trim();
    if (!dec.isDecimal(p) || dec.isZero(p) || dec.gt(dec.abs(p), String(OVERLAY_MAX_PCT))) return { problem: "pct", vars: { max: OVERLAY_MAX_PCT } };
    body.target_pct = Number(p);
  }
  if (![d.up, d.hold, d.down].every(whole) || Number(d.up) < 1 || Number(d.down) < OVERLAY_MIN_DOWN) {
    return { problem: "seconds", vars: { down: OVERLAY_MIN_DOWN } };
  }
  const [up, hold, down] = [Number(d.up), Number(d.hold), Number(d.down)];
  if (up + hold + down > OVERLAY_MAX_SECONDS) return { problem: "total", vars: { max: OVERLAY_MAX_SECONDS } };
  body.ramp_up_seconds = up;
  body.ramp_down_seconds = down;
  if (hold > 0) body.hold_seconds = hold;
  return { body };
}

/**
 * targetOf is where an event takes a pair's price from last: a share of
 * it, or the price given; null without a last price or a valid target.
 */
export function targetOf(d: OverlayDraft, last: string | undefined): string | null {
  if (d.mode === "price") return dec.isDecimal(d.price.trim()) && dec.gt(d.price.trim(), "0") ? d.price.trim() : null;
  const p = d.pct.trim();
  if (!last || !dec.isDecimal(last) || !dec.isDecimal(p) || dec.isZero(p)) return null;
  return dec.mul(last, dec.add("1", dec.div(p, "100", dec.decimalsOf(p) + 2)));
}

/** moveOf is a target's move from last as a share (0.16 is +16%); null without both. */
export function moveOf(target: string | null, last: string | undefined): number | null {
  if (!target || !last || !dec.isDecimal(last) || !dec.gt(last, "0")) return null;
  return Number(dec.div(target, last, 8)) - 1;
}
