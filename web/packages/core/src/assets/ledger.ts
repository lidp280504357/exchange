// The fund flow (GET /v1/account/ledger, design §6.2 history): the entry
// types a user's lines can carry, what each relates to (for links to the
// deposit, withdrawal, transfer or trading pages), and time ranges. The
// API filters by asset and type; the time range is applied here, over the
// cursor pages (newest first), which also tells when paging can stop.

/** The ledger's entry types on user accounts (internal/ledger/domain). */
export const LEDGER_ENTRY_TYPES = [
  "DEPOSIT_CREDIT",
  "WITHDRAW_FREEZE",
  "WITHDRAW_SETTLE",
  "WITHDRAW_UNFREEZE",
  "INTERNAL_TRANSFER",
  "ACCOUNT_TRANSFER",
  "ORDER_FREEZE",
  "ORDER_UNFREEZE",
  "TRADE_SETTLE",
  "TRADE_FEE",
  "REALIZED_PNL",
  "FUNDING_PAYMENT",
  "LIQUIDATION_SETTLE",
  "ADL_SETTLE",
  "INSURANCE_CONTRIBUTION",
  "MANUAL_ADJUSTMENT",
  "MARGIN_TRANSFER_IN",
  "MARGIN_TRANSFER_OUT",
  "MARGIN_BORROW",
  "MARGIN_INTEREST",
  "MARGIN_REPAY",
  "MARGIN_TRADE_SETTLE",
  "MARGIN_LIQUIDATE",
] as const;

export type LedgerRelation = "deposit" | "withdraw" | "transfer" | "spot" | "futures" | "margin" | "adjustment";

/**
 * ledgerRelation tells what an entry belongs to. Lines do not carry the
 * order or withdrawal ID, so the pages link to the list they appear in.
 */
export function ledgerRelation(entryType: string, accountType = "SPOT"): LedgerRelation | null {
  if (entryType.startsWith("MARGIN_") || accountType.startsWith("MARGIN_")) return "margin";
  switch (entryType) {
    case "DEPOSIT_CREDIT":
      return "deposit";
    case "WITHDRAW_FREEZE":
    case "WITHDRAW_SETTLE":
    case "WITHDRAW_UNFREEZE":
    case "INTERNAL_TRANSFER":
      return "withdraw";
    case "ACCOUNT_TRANSFER":
      return "transfer";
    case "ORDER_FREEZE":
    case "ORDER_UNFREEZE":
    case "TRADE_SETTLE":
    case "TRADE_FEE":
      return accountType === "FUTURES" ? "futures" : "spot";
    case "REALIZED_PNL":
    case "FUNDING_PAYMENT":
    case "LIQUIDATION_SETTLE":
    case "ADL_SETTLE":
    case "INSURANCE_CONTRIBUTION":
      return "futures";
    case "MANUAL_ADJUSTMENT":
      return "adjustment";
    default:
      return null;
  }
}

/** A time range as epoch milliseconds: from inclusive, to exclusive; null is open. */
export type TimeRange = { from: number | null; to: number | null };

/** How many days of entries the server keeps (M1, user 2026-10-10): older ones are gone. */
export const LEDGER_KEPT_DAYS = 15;

/** The rolling windows offered: none reaches past what the server keeps (LEDGER_KEPT_DAYS). */
export type RangePreset = "all" | "7d" | "15d";

const DAY = 86_400_000;

/** presetRange is a rolling window ending now. */
export function presetRange(preset: RangePreset, now: number = Date.now()): TimeRange {
  switch (preset) {
    case "7d":
      return { from: now - 7 * DAY, to: null };
    case "15d":
      return { from: now - LEDGER_KEPT_DAYS * DAY, to: null };
    default:
      return { from: null, to: null };
  }
}

const zoneFormats = new Map<string, Intl.DateTimeFormat>();

// zoneOffset is how far a time zone's clock is ahead of UTC at instant t (ms).
function zoneOffset(t: number, timeZone?: string): number {
  const key = timeZone ?? "";
  let f = zoneFormats.get(key);
  if (!f) {
    f = new Intl.DateTimeFormat("en-US", {
      timeZone, hourCycle: "h23", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit",
    });
    zoneFormats.set(key, f);
  }
  const parts = f.formatToParts(new Date(t));
  const get = (type: Intl.DateTimeFormatPartTypes) => Number(parts.find((p) => p.type === type)?.value ?? "0");
  const asUtc = Date.UTC(get("year"), get("month") - 1, get("day"), get("hour") % 24, get("minute"), get("second"));
  return asUtc - Math.floor(t / 1000) * 1000;
}

/**
 * dayStart is the first instant of a calendar day ("2026-09-30") in a time
 * zone (the user's; the browser's when undefined), or null for a bad date.
 */
export function dayStart(date: string, timeZone?: string): number | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date);
  if (!m) return null;
  const utc = Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  if (Number.isNaN(utc)) return null;
  // Midnight there is UTC midnight minus the zone's offset; a second pass
  // settles days whose offset changes (daylight saving).
  let t = utc - zoneOffset(utc, timeZone);
  t = utc - zoneOffset(t, timeZone);
  return t;
}

/** dayRange covers whole calendar days, from the first day's start to the day after the last. */
export function dayRange(fromDate: string, toDate: string, timeZone?: string): TimeRange {
  const from = fromDate ? dayStart(fromDate, timeZone) : null;
  const last = toDate ? dayStart(toDate, timeZone) : null;
  // The next day's start: 26 hours on, then back to that day's midnight.
  const to = last === null ? null : dayStart(isoDay(last + 26 * 3_600_000, timeZone), timeZone);
  return { from, to };
}

// isoDay is the calendar day ("YYYY-MM-DD") of instant t in a time zone.
function isoDay(t: number, timeZone?: string): string {
  const local = t + zoneOffset(t, timeZone);
  return new Date(local).toISOString().slice(0, 10);
}

/** inRange reports whether a time falls in the range. */
export function inRange(at: string, r: TimeRange): boolean {
  const t = Date.parse(at);
  if (Number.isNaN(t)) return false;
  return (r.from === null || t >= r.from) && (r.to === null || t < r.to);
}

/** pastRange reports whether a time is older than the range: newest-first paging can stop there. */
export function pastRange(at: string, r: TimeRange): boolean {
  if (r.from === null) return false;
  const t = Date.parse(at);
  return !Number.isNaN(t) && t < r.from;
}
