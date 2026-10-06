import { dec, errorText, formatAmount, formatPercent, routes, useSettings } from "@exchange/core";
import { usePortfolio } from "@exchange/core/assets/hooks";
import { Button, CountUp, ErrorState, HIDDEN_AMOUNT, Skeleton, cn, ease, listItem, toneOf } from "@exchange/ui";
import { ArrowDownToLine, ArrowDownRight, ArrowLeftRight, ArrowUpFromLine, ArrowUpRight, ChevronRight, Eye, EyeOff } from "lucide-react";
import { motion } from "motion/react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { splitShares } from "./logic";

const TONE = { up: "text-up", down: "text-down", neutral: "text-fg-2" } as const;

/** RollIn is a CountUp that rolls up from 0 when it first shows, then to every new value. */
function RollIn({ value, decimals }: { value: string; decimals: number }) {
  const [shown, setShown] = useState("0");
  useEffect(() => setShown(value), [value]);
  return <CountUp value={shown} decimals={decimals} />;
}

/** Hidden crossfades a value with the mask (blurring out over 200 ms) when amounts are hidden. */
function Hidden({ hidden, children, className }: { hidden: boolean; children: ReactNode; className?: string }) {
  return (
    <span className={cn("relative inline-grid", className)}>
      <span aria-hidden={hidden} className={cn("col-start-1 row-start-1 transition-[opacity,filter] duration-200", hidden && "opacity-0 blur-sm")}>
        {children}
      </span>
      <span aria-hidden={!hidden} className={cn("col-start-1 row-start-1 transition-opacity duration-200", hidden ? "opacity-100" : "opacity-0")}>
        {HIDDEN_AMOUNT}
      </span>
    </span>
  );
}

/**
 * AssetsCard (design §7.3 ③): the total of both accounts at reference
 * prices (rolling to every new value) and in BTC, the eye that hides
 * amounts everywhere, the 24-hour change of the holdings (an estimate:
 * core dayChange), the spot and futures shares as a bar that grows in,
 * and deposit, withdraw and transfer.
 */
export function AssetsCard({ index }: { index: number }) {
  const { t } = useTranslation();
  const { balances, portfolio, day, inBtc } = usePortfolio();
  const hidden = useSettings((s) => s.hideAmounts);
  const set = useSettings((s) => s.set);
  const total = dec.round(portfolio.total, 2, "down");
  const shares = splitShares(portfolio.spot, portfolio.futures, portfolio.margin);
  const dir = toneOf(day.value);
  const pct = (v: number) => formatPercent(String(v), 1, false);

  let body;
  if (balances.isPending)
    body = (
      <div className="pb-4">
        <Skeleton className="h-9 w-48" />
        <Skeleton className="mt-2 h-4 w-28" />
        <Skeleton className="mt-4 h-1.5 w-full" />
      </div>
    );
  else if (balances.isError) body = <ErrorState compact className="py-3" message={errorText(balances.error)} onRetry={() => void balances.refetch()} />;
  else
    body = (
      <div className="pb-4">
        <div className="flex items-baseline gap-1.5">
          <Hidden hidden={hidden} className="text-2xl font-semibold tracking-tight text-fg-1">
            <RollIn value={total} decimals={2} />
          </Hidden>
          <span className="text-sm text-fg-3">USDT</span>
        </div>
        <div className="mt-0.5 flex h-6 items-center justify-between gap-3 text-sm tabular-nums">
          <span
            className="truncate text-fg-3"
            title={inBtc ? (hidden ? HIDDEN_AMOUNT : t("mAccount.me.assets.approxBtc", { value: formatAmount(inBtc, 8) })) : undefined}
          >
            {inBtc && <Hidden hidden={hidden}>{t("mAccount.me.assets.approxBtc", { value: formatAmount(inBtc, 8) })}</Hidden>}
          </span>
          <span className="flex shrink-0 items-center gap-1" title={t("mAccount.me.assets.dayHint")}>
            <span className="text-fg-3">{t("mAccount.me.assets.day")}</span>
            <Hidden hidden={hidden} className={cn("font-medium", TONE[dir])}>
              <span className="inline-flex items-center">
                {dir === "up" && <ArrowUpRight size={14} aria-hidden />}
                {dir === "down" && <ArrowDownRight size={14} aria-hidden />}
                {dir === "up" && "+"}
                {formatAmount(dec.round(day.value, 2, "half"), 2)}
                {day.ratio !== null && <span className="ml-1">({formatPercent(day.ratio)})</span>}
              </span>
            </Hidden>
          </span>
        </div>
        <div className="mt-3">
          <div
            role="img"
            aria-label={`${t("mAccount.me.assets.split")}: ${t("mAccount.me.assets.spot")} ${pct(shares.spot)}, ${t("mAccount.me.assets.futures")} ${pct(shares.futures)}${
              shares.margin > 0 ? `, ${t("mAccount.me.assets.margin")} ${pct(shares.margin)}` : ""
            }`}
            className="flex h-1.5 gap-0.5 overflow-hidden rounded-full bg-bg-3"
          >
            {shares.spot > 0 && (
              <motion.span
                className="h-full origin-left rounded-full bg-brand"
                style={{ width: `${shares.spot * 100}%` }}
                initial={{ scaleX: 0 }}
                animate={{ scaleX: 1 }}
                transition={{ duration: 0.4, ease, delay: 0.15 }}
              />
            )}
            {shares.futures > 0 && (
              <motion.span
                className="h-full origin-left rounded-full bg-info"
                style={{ width: `${shares.futures * 100}%` }}
                initial={{ scaleX: 0 }}
                animate={{ scaleX: 1 }}
                transition={{ duration: 0.4, ease, delay: 0.3 }}
              />
            )}
            {shares.margin > 0 && (
              <motion.span
                className="h-full origin-left rounded-full bg-warn"
                style={{ width: `${shares.margin * 100}%` }}
                initial={{ scaleX: 0 }}
                animate={{ scaleX: 1 }}
                transition={{ duration: 0.4, ease, delay: 0.45 }}
              />
            )}
          </div>
          <div className="mt-1.5 flex justify-between text-xs text-fg-3 tabular-nums">
            <span className="flex items-center gap-1.5">
              <span aria-hidden className="size-2 rounded-full bg-brand" />
              {t("mAccount.me.assets.spot")} {pct(shares.spot)}
            </span>
            <span className="flex items-center gap-1.5">
              <span aria-hidden className="size-2 rounded-full bg-info" />
              {t("mAccount.me.assets.futures")} {pct(shares.futures)}
            </span>
            {shares.margin > 0 && (
              <span className="flex items-center gap-1.5">
                <span aria-hidden className="size-2 rounded-full bg-warn" />
                {t("mAccount.me.assets.margin")} {pct(shares.margin)}
              </span>
            )}
          </div>
        </div>
      </div>
    );

  return (
    <motion.section
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={index}
      data-testid="me-assets"
      aria-label={t("mAccount.me.assets.title")}
      className="rounded-3 border border-line-1 bg-bg-1 px-4"
    >
      <div className="flex items-center justify-between">
        <div className="flex min-w-0 items-center text-sm text-fg-3">
          <span className="truncate">{t("mAccount.me.assets.title")} (USDT)</span>
          <button
            type="button"
            aria-pressed={hidden}
            aria-label={hidden ? t("mAccount.me.assets.show") : t("mAccount.me.assets.hide")}
            onClick={() => set({ hideAmounts: !hidden })}
            className="grid size-tap shrink-0 place-items-center rounded-full text-fg-3 transition-colors active:bg-bg-2 active:text-fg-1"
          >
            {hidden ? <EyeOff size={16} /> : <Eye size={16} />}
          </button>
        </div>
        <Link to={routes.assets} className="-mr-2 flex h-tap shrink-0 items-center gap-0.5 px-2 text-sm text-fg-3 transition-colors active:text-fg-1">
          {t("nav.assets")}
          <ChevronRight size={16} aria-hidden />
        </Link>
      </div>
      {body}
      <div className="grid grid-cols-3 gap-2 border-t border-line-1 py-3">
        <Button asChild size="md" icon={<ArrowDownToLine size={16} />}>
          <Link to={routes.deposit}>{t("nav.deposit")}</Link>
        </Button>
        <Button asChild size="md" variant="secondary" icon={<ArrowUpFromLine size={16} />}>
          <Link to={routes.withdraw}>{t("nav.withdraw")}</Link>
        </Button>
        <Button asChild size="md" variant="secondary" icon={<ArrowLeftRight size={16} />}>
          <Link to={routes.transfer}>{t("nav.transfer")}</Link>
        </Button>
      </div>
    </motion.section>
  );
}
