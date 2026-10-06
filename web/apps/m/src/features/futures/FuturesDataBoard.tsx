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

// The futures data of one contract on the phone (design 2026-10-06 §3.3):
// a period for every chart, then the open interest, the three long/short
// ratios, the takers' volume, the basis, the settled funding rates and the
// liquidations as they come, one card under another. The terminal shows
// it in its 数据 tab, the overview page in a sheet. A contract the
// reference market does not trade (the platform coin's) has none: nothing
// is asked for it.

export type FuturesDataBoardProps = {
  contract: ContractSpec;
  /** Off while the board is hidden (another tab): nothing is read again. */
  active?: boolean;
  /** The link to every contract's data (not in the overview's own sheet). */
  overviewLink?: boolean;
  className?: string;
};

const HEIGHT = 150;

export function FuturesDataBoard({ contract, active = true, overviewLink = true, className }: FuturesDataBoardProps) {
  const { t } = useTranslation();
  const [period, setPeriod] = useState<FuturesPeriod>("5m");
  if (!hasFuturesData(contract)) {
    return <EmptyState compact title={t("mFutures.none")} description={t("mFutures.noneHint")} className={className} />;
  }
  return (
    <div className={cn("flex flex-col gap-3", className)}>
      <Segmented
        size="md"
        block
        aria-label={t("mFutures.period")}
        value={period}
        onValueChange={(v) => setPeriod(v as FuturesPeriod)}
        items={FUTURES_PERIODS.map((p) => ({ value: p, label: t(`mFutures.periods.${p}`) }))}
      />
      {FUTURES_METRICS.map((m) => (
        <MetricSlot key={m} contract={contract} metric={m} period={period} active={active} />
      ))}
      <Liquidations contract={contract} active={active} />
      {overviewLink && (
        <Link
          to={`${routes.futuresData}?symbol=${contract.symbol}`}
          className="flex min-h-tap items-center justify-center gap-0.5 text-sm text-fg-3 active:text-brand"
        >
          {t("mFutures.allData")}
          <ChevronRight size={16} />
        </Link>
      )}
    </div>
  );
}

export default FuturesDataBoard;

function MetricSlot({ contract, metric, period, active }: { contract: ContractSpec; metric: Metric; period: FuturesPeriod; active: boolean }) {
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
      height={HEIGHT}
    />
  );
}

const ROWS = 10;
const ROW_HEIGHT = 28;

function Liquidations({ contract, active }: { contract: ContractSpec; active: boolean }) {
  const { t } = useTranslation();
  const labels = useFuturesLabels();
  const q = useLiquidations(contract.symbol, { enabled: active });
  const items = q.data ?? [];
  const body = 28 + ROWS * ROW_HEIGHT;
  return (
    <section aria-label={t("mFutures.liquidations.title")} className="flex flex-col rounded-2 border border-line-1 bg-bg-1 py-3">
      <header className="flex items-center gap-1.5 px-3">
        <h3 className="text-sm font-medium text-fg-1">{t("mFutures.liquidations.title")}</h3>
        <InfoHint text={t("mFutures.liquidations.hint")} label={t("mFutures.liquidations.hint")} />
        <span className="ml-auto flex items-center gap-1.5 text-xs text-fg-3">
          <span aria-hidden className="size-1.5 animate-pulse rounded-full bg-up" />
          {t("mFutures.liquidations.live")}
        </span>
      </header>
      <div className="mt-2" style={{ height: body }}>
        {q.isPending ? (
          <div className="flex flex-col gap-3 px-3 pt-8">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-4 w-full" />
            ))}
          </div>
        ) : q.isError && items.length === 0 ? (
          <div className="flex h-full items-center justify-center text-sm text-fg-3">{noFuturesData(q.error) ? t("mFutures.none") : t("state.errorTitle")}</div>
        ) : items.length === 0 ? (
          <div className="flex h-full items-center justify-center text-sm text-fg-3">{t("mFutures.liquidations.empty")}</div>
        ) : (
          <LiquidationTape
            items={items}
            priceDecimals={dec.decimalsOf(contract.tick_size)}
            qtyDecimals={dec.decimalsOf(contract.lot_size)}
            labels={labels.liquidations(qtyUnit(contract, labels.contracts))}
            max={ROWS}
            rowHeight={ROW_HEIGHT}
            className="h-full"
          />
        )}
      </div>
    </section>
  );
}
