import { formatDecimal } from "@exchange/core";
import { gaugeShare, levelText, levelZone, type LevelZone } from "@exchange/core/margin/math";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";

export type MarginLevelProps = {
  /** total assets / total liabilities; null without debts (shown as 999). */
  level: string | null | undefined;
  /** The account's warning and liquidation levels. */
  warn: string;
  liquidation: string;
  size?: "sm" | "md";
  /** Hide the thresholds under the bar (tight rows). */
  compact?: boolean;
  className?: string;
};

const fills: Record<LevelZone, string> = { none: "bg-success", safe: "bg-success", caution: "bg-warn", danger: "bg-danger" };
const texts: Record<LevelZone, string> = { none: "text-success", safe: "text-success", caution: "text-warn", danger: "text-danger" };

/**
 * MarginLevel is a margin account's risk gauge (margin design §7): the
 * margin level coloured by where it stands (green well above the warning
 * level, amber near it, red under it), a bar from the liquidation level to
 * twice the warning level with a mark at the warning level, and both
 * thresholds. The fill moves with a transform, so it animates without
 * layout.
 */
export function MarginLevel({ level, warn, liquidation, size = "md", compact, className }: MarginLevelProps) {
  const { t } = useTranslation();
  const zone = levelZone(level, warn, liquidation);
  const share = gaugeShare(level, warn, liquidation);
  const warnAt = gaugeShare(warn, warn, liquidation);
  const shown = levelText(level);
  return (
    <div className={cn("flex w-full flex-col gap-1.5", className)} data-zone={zone}>
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-xs text-fg-3">{t("ui.margin.level")}</span>
        <span className={cn("font-semibold tabular-nums", size === "md" ? "text-lg" : "text-sm", texts[zone])} data-testid="margin-level">
          {zone === "none" ? `${shown}` : formatDecimal(shown)}
        </span>
      </div>
      <div
        role="meter"
        aria-label={t("ui.margin.level")}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(share * 100)}
        aria-valuetext={t(`ui.margin.zone.${zone}`, { level: shown })}
        className={cn("relative w-full overflow-hidden rounded-full bg-bg-3", size === "md" ? "h-2" : "h-1.5")}
      >
        <span
          className={cn("absolute inset-0 rounded-full transition-transform duration-[var(--t-slow)] ease-out", fills[zone])}
          style={{ transform: `translateX(-${(1 - share) * 100}%)` }}
        />
        <span className="absolute inset-y-0 w-px bg-fg-1/60" style={{ left: `${warnAt * 100}%` }} aria-hidden />
      </div>
      {!compact && (
        <div className="flex justify-between gap-2 text-xs text-fg-3 tabular-nums">
          <span>{t("ui.margin.liquidationAt", { level: formatDecimal(liquidation) })}</span>
          <span>{t("ui.margin.warnAt", { level: formatDecimal(warn) })}</span>
        </div>
      )}
    </div>
  );
}
