import { dec, formatDecimal } from "@exchange/core";
import { cn, Progress } from "@exchange/ui";
import { ShieldCheck, UserRound } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { useConsoleSettings } from "../../live";

/**
 * ModeBanner says how fund operations are approved now: by a second
 * administrator, or (single-person mode) by the requester alone within
 * the limits, with the share of the 24-hour limit already used.
 */
export function ModeBanner({ className }: { className?: string }) {
  const { t } = useTranslation();
  const s = useConsoleSettings().data;
  if (!s) return null;
  const usdt = (v: string) => formatDecimal(v, { decimals: 0 });
  const used = dec.isDecimal(s.daily_used_usdt) && dec.isDecimal(s.daily_max_usdt) && dec.gt(s.daily_max_usdt, "0")
    ? Math.min(100, (dec.toNumber(s.daily_used_usdt) / dec.toNumber(s.daily_max_usdt)) * 100)
    : 0;
  return (
    <div
      className={cn(
        "card flex flex-wrap items-center gap-x-4 gap-y-2 border-l-[3px] px-4 py-3 text-sm",
        s.two_person_approval ? "border-l-success" : "border-l-info",
        className,
      )}
    >
      {s.two_person_approval ? <ShieldCheck size={18} className="text-success" /> : <UserRound size={18} className="text-info" />}
      <div className="min-w-0 flex-1">
        <div className="font-medium">{t(s.two_person_approval ? "admin.funds.twoPersonMode" : "admin.funds.singleMode")}</div>
        <div className="text-fg-3">
          {s.two_person_approval
            ? t("admin.funds.twoPersonModeHint")
            : t("admin.funds.singleModeHint", { single: usdt(s.single_max_usdt), daily: usdt(s.daily_max_usdt) })}
        </div>
      </div>
      {!s.two_person_approval && (
        <Progress
          className="w-56"
          value={used}
          tone={used >= 90 ? "warn" : "info"}
          label={t("admin.funds.used24h")}
          valueText={`${usdt(s.daily_used_usdt)} / ${usdt(s.daily_max_usdt)} USDT`}
        />
      )}
      <Link to="/settings" className="text-info hover:underline">
        {t("admin.funds.toSettings")}
      </Link>
    </div>
  );
}
