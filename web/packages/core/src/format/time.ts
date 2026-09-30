// Times on screen (design §4.3): one formatter for tables, charts and
// notices, in the user's time zone (the browser's by default), so a trade
// shows the same clock everywhere. Intl formatters are costly to build and
// are cached per locale, zone and style.

export type TimeStyle = "datetime" | "datetimeSeconds" | "date" | "time" | "timeSeconds" | "monthDay";

const styles: Record<TimeStyle, Intl.DateTimeFormatOptions> = {
  datetime: { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23" },
  datetimeSeconds: {
    year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
  },
  date: { year: "numeric", month: "2-digit", day: "2-digit" },
  time: { hour: "2-digit", minute: "2-digit", hourCycle: "h23" },
  timeSeconds: { hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" },
  monthDay: { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23" },
};

const cache = new Map<string, Intl.DateTimeFormat>();

function formatter(style: TimeStyle, locale: string, timeZone?: string): Intl.DateTimeFormat {
  const key = `${style}|${locale}|${timeZone ?? ""}`;
  let f = cache.get(key);
  if (!f) {
    f = new Intl.DateTimeFormat(locale, { ...styles[style], timeZone });
    cache.set(key, f);
  }
  return f;
}

export type TimeInput = string | number | Date | null | undefined;

function toDate(t: TimeInput): Date | null {
  if (t === null || t === undefined || t === "") return null;
  const d = t instanceof Date ? t : new Date(t);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** formatTime renders a time (RFC 3339 string, epoch ms or Date). */
export function formatTime(t: TimeInput, style: TimeStyle = "datetime", locale = "zh-CN", timeZone?: string): string {
  const d = toDate(t);
  if (!d) return "—";
  // Chinese and English both read year-month-day with dashes here: the
  // locale's slashes are swapped for one stable form.
  const parts = formatter(style, locale, timeZone).formatToParts(d);
  const get = (type: Intl.DateTimeFormatPartTypes) => parts.find((p) => p.type === type)?.value ?? "";
  const date = [get("year"), get("month"), get("day")].filter(Boolean).join("-");
  const clock = [get("hour"), get("minute"), get("second")].filter(Boolean).join(":");
  return [date, clock].filter(Boolean).join(" ");
}

const relative = new Map<string, Intl.RelativeTimeFormat>();

/** formatRelative renders how long ago t was: "3 分钟前", "3 minutes ago". */
export function formatRelative(t: TimeInput, now: number = Date.now(), locale = "zh-CN"): string {
  const d = toDate(t);
  if (!d) return "—";
  let f = relative.get(locale);
  if (!f) {
    f = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
    relative.set(locale, f);
  }
  const secs = Math.round((d.getTime() - now) / 1000);
  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ["year", 31_536_000], ["month", 2_592_000], ["week", 604_800], ["day", 86_400], ["hour", 3_600], ["minute", 60],
  ];
  for (const [unit, size] of units) {
    if (Math.abs(secs) >= size) return f.format(Math.trunc(secs / size), unit);
  }
  return f.format(secs, "second");
}

/** timeZones lists the zones the settings offer. */
export function timeZones(): string[] {
  const all = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.("timeZone");
  return all ?? ["UTC", "Asia/Shanghai", "Asia/Singapore", "Asia/Tokyo", "Europe/London", "America/New_York"];
}
