import { routes, useTotpStatus } from "@exchange/core";
import { useInView } from "@exchange/core/markets/index";
import { useProfile } from "@exchange/core/user/profile";
import { useBoundIdentities } from "@exchange/core/user/security";
import { Skeleton, cn, listItem } from "@exchange/ui";
import { ChevronRight, ShieldCheck } from "lucide-react";
import { motion } from "motion/react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { totpState } from "../parts/logic";
import { guardProgress } from "./logic";

const RADIUS = 23;
const CIRCUMFERENCE = 2 * Math.PI * RADIUS;

/**
 * GuardCard (design §7.3 ⑤): how many of the four protections are on as
 * a ring that draws itself (800 ms) the first time it scrolls into view,
 * and the first one still to do; all four on shows a green shield. The
 * whole card opens the security centre.
 */
export function GuardCard({ index }: { index: number }) {
  const { t } = useTranslation();
  const ids = useBoundIdentities();
  const profile = useProfile();
  const totp = useTotpStatus();
  const [ref, seen] = useInView<HTMLAnchorElement>({ rootMargin: "0px" });

  if (!ids.data || !profile.data || !totp.data) {
    // While loading, and on errors (the security centre has the retries).
    return ids.isPending || profile.isPending || totp.isPending ? (
      <div aria-busy className="flex items-center gap-4 rounded-3 border border-line-1 bg-bg-1 p-4">
        <Skeleton round className="size-14" />
        <div className="flex flex-1 flex-col gap-2">
          <Skeleton className="h-4 w-24" />
          <Skeleton className="h-4 w-full" />
        </div>
      </div>
    ) : null;
  }
  const g = guardProgress({
    totp: totpState(totp.data) === "on",
    email: Boolean(ids.data.EMAIL),
    phone: Boolean(ids.data.PHONE),
    antiPhishing: Boolean(profile.data.anti_phishing_code),
  });
  const all = g.next === null;
  const tone = all ? "success" : g.done >= 2 ? "warn" : "danger";

  return (
    <motion.div variants={listItem} initial="initial" animate="animate" custom={index}>
      <Link
        ref={ref}
        to={routes.security}
        data-testid="me-guard"
        className="flex items-center gap-4 rounded-3 border border-line-1 bg-bg-1 p-4 transition-colors active:bg-bg-2"
      >
        <span className="relative grid size-14 shrink-0 place-items-center">
          <svg viewBox="0 0 56 56" aria-hidden className="absolute inset-0 -rotate-90">
            <circle cx="28" cy="28" r={RADIUS} fill="none" strokeWidth="5" className="stroke-bg-3" />
            <circle
              cx="28"
              cy="28"
              r={RADIUS}
              fill="none"
              strokeWidth="5"
              strokeLinecap="round"
              strokeDasharray={CIRCUMFERENCE}
              strokeDashoffset={seen ? CIRCUMFERENCE * (1 - g.done / g.max) : CIRCUMFERENCE}
              className={cn(
                "transition-[stroke-dashoffset] duration-[800ms] ease-out",
                tone === "success" ? "stroke-success" : tone === "warn" ? "stroke-warn" : "stroke-danger",
              )}
            />
          </svg>
          {all ? (
            <ShieldCheck size={22} className="text-success" aria-hidden />
          ) : (
            <span className="text-sm font-semibold text-fg-1 tabular-nums">
              {g.done}/{g.max}
            </span>
          )}
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-baseline gap-x-2">
            <span className="text-base font-medium text-fg-1">{t("mAccount.security.title")}</span>
            <span className={cn("text-xs", all ? "text-success" : "text-fg-3")}>
              {all ? t("mAccount.me.securityTag", { level: t("mAccount.security.levels.high") }) : t("mAccount.me.guard.done", { n: g.done, max: g.max })}
            </span>
          </span>
          <span className="mt-1 line-clamp-2 block text-sm leading-snug text-fg-2">
            {g.next === null ? t("mAccount.me.guard.all") : t(`mAccount.me.guard.next.${g.next}`)}
          </span>
        </span>
        <ChevronRight size={18} className="shrink-0 text-fg-3" aria-hidden />
      </Link>
    </motion.div>
  );
}
