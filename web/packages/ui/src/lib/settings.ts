import { timeZoneOf, useSettings, type Locale } from "@exchange/core";

export type FormatContext = { locale: Locale; timeZone: string | undefined };

/**
 * useFormatContext returns the language and time zone to format numbers
 * and times in (the user's settings); two selectors, so a component only
 * re-renders when one of them changes.
 */
export function useFormatContext(): FormatContext {
  const locale = useSettings((s) => s.locale);
  const timeZone = useSettings((s) => timeZoneOf(s));
  return { locale, timeZone };
}
