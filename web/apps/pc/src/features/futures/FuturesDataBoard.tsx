import { dec, routes } from "@exchange/core";
import {
  FUTURES_METRICS,
  FUTURES_PERIODS,
  hasFuturesData,
  noFuturesData,
  useFuturesData,
  useLiquidations,
  type ContractSpec,
  type FuturesMetric as Metric,
  type FuturesPeriod,
} from "@exchange/core/futures/index";
import { EmptyState, Segmented, Skeleton, cn } from "@exchange/ui";
import { FuturesMetric, InfoHint, LiquidationTape } from "@exchange/ui/futures/index";
import { ChevronRight } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { qtyUnit, useFuturesLabels } from "./labels";

// The futures data of one contract (design 2026-10-06 §3.3): a period for
// every chart, the open interest, the three long/short ratios, the takers'
// volume, the basis, the settled funding rates and the liquidations as
// they come. The terminal shows it in its 数据 tab, the overview page
// under its table. A contract the reference market does not trade (the
// platform coin's) has none: nothing is asked for it.

export type FuturesDataBoardProps = {
  contract: ContractSpec;
  /** terminal: inside the terminal's centre column (with a link to the overview); page: the overview page. */
  layout?: "terminal" | "page";
  /** Off while the board is hidden: nothing is read again. */
  active?: boolean;
  className?: string;
};

export function FuturesDataBoard({ contract, layout = "terminal", active = true, className }: FuturesDataBoardProps) {
  const { t } = useTranslation();
  const [period, setPeriod] = useState<FuturesPeriod>("5m");
  const page = layout === "page";
  if (!hasFuturesData(contract)) {
    return <EmptyState compact title={t("pcFutures.none")} description={t("pcFutures.noneHint")} className={className} />;
  }
  return (
    <div className={cn("flex flex-col gap-2", className)}>
      <div className="flex flex-wrap items-center gap-3">
        <Segmented
          size="xs"
          aria-label={t("pcFutures.period")}
          value={period}
          onValueChange={(v) => setPeriod(v as FuturesPeriod)}
          items={FUTURES_PERIODS.map((p) => ({ value: p, label: t(`pcFutures.periods.${p}`) }))}
        />
        {!page && (
          <Link to={`${routes.futuresData}?symbol=${contract.symbol}`} className="ml-auto flex items-center gap-0.5 text-xs text-fg-3 transition-colors hover:text-brand">
            {t("pcFutures.allData")}
            <ChevronRight size={14} />
          </Link>
        )}
      </div>
      <div className={cn("grid gap-2", page ? "grid-cols-[repeat(auto-fill,minmax(360px,1fr))]" : "grid-cols-[repeat(auto-fill,minmax(300px,1fr))]")}>
        {FUTURES_METRICS.map((m) => (
          <MetricSlot key={m} contract={contract} metric={m} period={period} active={active} height={page ? 160 : 140} />
        ))}
        <Liquidations contract={contract} active={active} height={page ? 160 : 140} />
      </div>
    </div>
  );
}

export default FuturesDataBoard;

function MetricSlot({ contract, metric, period, active, height }: { contract: ContractSpec; metric: Metric; period: FuturesPeriod; active: boolean; height: number }) {
  const labels = useFuturesLabels();
  const q = useFuturesData(contract.symbol, metric, period, { enabled: active, fundingHours: contract.funding_interval_hours });
  const state = q.isPending ? "loading" : q.isError && !q.data ? (noFuturesData(q.error) ? "empty" : "error") : "ready";
  return (
    <FuturesMetric
      metric={metric}
      period={period}
      points={q.data?.points}
      state={state}
      stale={q.isPlaceholderData}
      onRetry={() => void q.refetch()}
      labels={labels.metric(metric)}
      common={labels.common}
      qtyUnit={qtyUnit(contract, labels.contracts)}
      priceDecimals={dec.decimalsOf(contract.tick_size)}
      qtyDecimals={dec.decimalsOf(contract.lot_size)}
      height={height}
    />
  );
}

// A statistic card's value line and legend over its chart: the
// liquidations' list takes the same room under its header.
const CARD_TOP = 58;
/** The tape's header row, px. */
const TAPE_HEAD = 28;

function Liquidations({ contract, active, height }: { contract: ContractSpec; active: boolean; height: number }) {
  const { t } = useTranslation();
  const labels = useFuturesLabels();
  const q = useLiquidations(contract.symbol, { enabled: active });
  const items = q.data ?? [];
  const rowHeight = 22;
  const body = CARD_TOP + height;
  return (
    <section aria-label={t("pcFutures.liquidations.title")} className="flex min-w-0 flex-col rounded-2 border border-line-1 bg-bg-1 py-3">
      <header className="flex items-center gap-1.5 px-3">
        <h3 className="text-sm font-medium text-fg-1">{t("pcFutures.liquidations.title")}</h3>
        <InfoHint text={t("pcFutures.liquidations.hint")} label={t("pcFutures.liquidations.hint")} />
        <span className="ml-auto flex items-center gap-1.5 text-xs text-fg-3">
          <span aria-hidden className="size-1.5 animate-pulse rounded-full bg-up" />
          {t("pcFutures.liquidations.live")}
        </span>
      </header>
      <div className="mt-2" style={{ height: body }}>
        {q.isPending ? (
          <div className="flex flex-col gap-2 px-3 pt-8">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-3.5 w-full" />
            ))}
          </div>
        ) : q.isError && items.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-sm text-fg-3">{noFuturesData(q.error) ? t("pcFutures.none") : t("state.errorTitle")}</div>
        ) : items.length === 0 ? (
          <div className="flex h-full items-center justify-center text-sm text-fg-3">{t("pcFutures.liquidations.empty")}</div>
        ) : (
          <LiquidationTape
            items={items}
            priceDecimals={dec.decimalsOf(contract.tick_size)}
            qtyDecimals={dec.decimalsOf(contract.lot_size)}
            labels={labels.liquidations(qtyUnit(contract, labels.contracts))}
            max={Math.max(1, Math.floor((body - TAPE_HEAD) / rowHeight))}
            rowHeight={rowHeight}
            className="h-full"
          />
        )}
      </div>
    </section>
  );
}
