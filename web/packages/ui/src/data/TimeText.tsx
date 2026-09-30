import { formatRelative, formatTime, type TimeInput, type TimeStyle } from "@exchange/core";
import { cn } from "../lib/cn";
import { useNow } from "../lib/clock";
import { useFormatContext } from "../lib/settings";

export type TimeTextProps = {
  value: TimeInput;
  /** The absolute format (default "datetime"). */
  format?: TimeStyle;
  /** "3 分钟前" instead of a clock time; the full time shows on hover. */
  relative?: boolean;
  className?: string;
};

/** The shared clock of relative times: one tick per 30 s for the whole page. */
export const RELATIVE_TICK_MS = 30_000;

function isoOf(value: TimeInput): string | undefined {
  if (value === null || value === undefined || value === "") return undefined;
  const d = value instanceof Date ? value : new Date(value);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}

/**
 * TimeText formats a time in the user's language and time zone (the same
 * clock in tables, charts and notices). Relative times follow one shared
 * 30-second ticker, not a timer per instance.
 */
export function TimeText({ value, format = "datetime", relative, className }: TimeTextProps) {
  const { locale, timeZone } = useFormatContext();
  const absolute = formatTime(value, relative ? "datetimeSeconds" : format, locale, timeZone);
  return (
    <time dateTime={isoOf(value)} title={relative ? absolute : undefined} className={cn("tabular-nums", className)}>
      {relative ? <RelativeTime value={value} locale={locale} /> : absolute}
    </time>
  );
}

// RelativeTime is split out so only relative times subscribe to the clock.
function RelativeTime({ value, locale }: { value: TimeInput; locale: string }) {
  const now = useNow(RELATIVE_TICK_MS);
  return <>{formatRelative(value, Math.max(now, Date.now() - RELATIVE_TICK_MS), locale)}</>;
}
