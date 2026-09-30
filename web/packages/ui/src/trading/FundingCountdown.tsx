import { formatPercent } from "@exchange/core";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";

export type FundingCountdownProps = {
  /** The next funding time (RFC 3339). */
  nextFundingTime: string | null | undefined;
  /** The current funding rate as a fraction ("0.0001" = 0.0100%). */
  rate: string | null | undefined;
  /** Stack label over value (the ticker bar) or inline. */
  layout?: "stacked" | "inline";
  className?: string;
};

/** countdown formats the time left until `target` as hh:mm:ss (00:00:00 once passed). */
export function countdown(target: number, now: number): string {
  const left = Math.max(0, Math.floor((target - now) / 1000));
  const h = Math.floor(left / 3600);
  const m = Math.floor((left % 3600) / 60);
  const s = left % 60;
  return [h, m, s].map((n) => String(n).padStart(2, "0")).join(":");
}

/**
 * FundingCountdown shows the funding rate and the time to the next
 * funding. It is the only component that re-renders every second: its own
 * timer, aligned to the second, updates just this element (design §6.2).
 */
export function FundingCountdown({ nextFundingTime, rate, layout = "stacked", className }: FundingCountdownProps) {
  const { t } = useTranslation();
  const [now, setNow] = useState(() => Date.now());
  const target = nextFundingTime ? Date.parse(nextFundingTime) : NaN;

  useEffect(() => {
    if (Number.isNaN(target)) return;
    let timer: ReturnType<typeof setTimeout>;
    const tick = () => {
      const n = Date.now();
      setNow(n);
      timer = setTimeout(tick, 1000 - (n % 1000) + 5);
    };
    timer = setTimeout(tick, 1000 - (Date.now() % 1000) + 5);
    return () => clearTimeout(timer);
  }, [target]);

  const value = (
    <span className="tabular-nums">
      <span className="text-brand">{formatPercent(rate, 4, false)}</span>
      <span className="mx-1 text-fg-3">/</span>
      <span className="text-fg-1">{Number.isNaN(target) ? "--:--:--" : countdown(target, now)}</span>
    </span>
  );
  const label = `${t("ui.funding.rate")} / ${t("ui.funding.countdown")}`;
  return layout === "inline" ? (
    <span className={cn("inline-flex items-center gap-2 text-xs", className)}>
      <span className="text-fg-3">{label}</span>
      {value}
    </span>
  ) : (
    <div className={cn("flex flex-col gap-0.5 text-xs", className)}>
      <span className="text-fg-3">{label}</span>
      {value}
    </div>
  );
}
