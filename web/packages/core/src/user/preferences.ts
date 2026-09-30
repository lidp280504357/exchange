import { timeZones } from "../format/time";

// Helpers of the settings page (device-local preferences, settings/store):
// the time zones to offer, each with its current UTC offset.

/** browserTimeZone is the zone the browser runs in ("" when Intl cannot tell). */
export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone ?? "";
  } catch {
    return "";
  }
}

const offsetFormatters = new Map<string, Intl.DateTimeFormat | null>();

function offsetFormatter(zone: string): Intl.DateTimeFormat | null {
  let f = offsetFormatters.get(zone);
  if (f === undefined) {
    try {
      f = new Intl.DateTimeFormat("en-US", { timeZone: zone, timeZoneName: "longOffset" });
    } catch {
      f = null; // not a zone this browser knows
    }
    offsetFormatters.set(zone, f);
  }
  return f;
}

/** zoneOffset is a zone's UTC offset at a moment: "UTC+08:00", "UTC-04:00", "UTC+00:00" ("" if unknown). */
export function zoneOffset(zone: string, at: number = Date.now()): string {
  const name = offsetFormatter(zone)
    ?.formatToParts(at)
    .find((p) => p.type === "timeZoneName")?.value;
  if (name === undefined) return "";
  const m = /^GMT([+-]\d{2}:\d{2})?$/.exec(name);
  if (!m) return "";
  return `UTC${m[1] ?? "+00:00"}`;
}

/** offsetMinutes turns "UTC+05:30" into 330 (0 for anything else). */
export function offsetMinutes(offset: string): number {
  const m = /^UTC([+-])(\d{2}):(\d{2})$/.exec(offset);
  if (!m) return 0;
  const v = Number(m[2]) * 60 + Number(m[3]);
  return m[1] === "-" ? -v : v;
}

export type ZoneOption = { zone: string; offset: string };

/**
 * zoneOptions lists the zones to choose from, UTC first, then west to
 * east by their offset at `at`, and by name within an offset.
 */
export function zoneOptions(zones: readonly string[] = timeZones(), at: number = Date.now()): ZoneOption[] {
  const unique = [...new Set(["UTC", ...zones])];
  const list = unique.map((zone) => ({ zone, offset: zoneOffset(zone, at) })).filter((z) => z.offset !== "");
  const utc = list.filter((z) => z.zone === "UTC");
  const rest = list
    .filter((z) => z.zone !== "UTC")
    .sort((a, b) => offsetMinutes(a.offset) - offsetMinutes(b.offset) || (a.zone < b.zone ? -1 : a.zone > b.zone ? 1 : 0));
  return [...utc, ...rest];
}

/** zoneLabel reads a zone name aloud: "America/New_York" → "America/New York". */
export function zoneLabel(zone: string): string {
  return zone.replace(/_/g, " ");
}
