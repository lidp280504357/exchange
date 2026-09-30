import {
  applyOrderToCaches, cancelOrder, dec, enumLabel, errorText, formatAmount, formatPrice, isActive, routes, selectSignedIn, useFills,
  useOpenOrders, useOrderHistory, useSession, type Fill, type Order, type Pair,
} from "@exchange/core";
import { Button, EmptyState, ErrorState, Progress, Skeleton, Tabs, TimeText, cn, toast } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

/**
 * SpotOrders: the caller's orders of this pair under the terminal as cards
 * (design §7.2): open orders with cancel, history and fills. The pushes
 * keep them current.
 */
export function SpotOrders({ pair }: { pair: Pair }) {
  const { t } = useTranslation();
  const signedIn = useSession(selectSignedIn);
  const [tab, setTab] = useState("open");
  const open = useOpenOrders(pair.symbol);
  if (!signedIn) {
    return (
      <EmptyState
        compact
        title={t("state.signInToTrade")}
        action={
          <Button asChild size="sm" className="hit-area">
            <Link to={`${routes.login}?next=${encodeURIComponent(routes.trade(pair.symbol))}`}>{t("nav.login")}</Link>
          </Button>
        }
      />
    );
  }
  return (
    <div>
      <Tabs
        value={tab}
        onValueChange={setTab}
        size="sm"
        listClassName="px-4"
        items={[
          { value: "open", label: t("mTrade.openOrders"), count: open.data?.items.length ?? 0 },
          { value: "history", label: t("mTrade.history") },
          { value: "fills", label: t("mTrade.fills") },
        ]}
      />
      {tab === "open" && <OpenList pair={pair} query={open} />}
      {tab === "history" && <HistoryList pair={pair} />}
      {tab === "fills" && <FillList pair={pair} />}
    </div>
  );
}

function Loading() {
  return (
    <div className="flex flex-col gap-2 p-4">
      <Skeleton className="h-20 w-full rounded-3" />
      <Skeleton className="h-20 w-full rounded-3" />
    </div>
  );
}

function OrderCard({ o, pair, action }: { o: Order; pair: Pair; action?: ReactNode }) {
  const { t } = useTranslation();
  const ratio = o.quantity && dec.sign(o.quantity) > 0 ? Math.min(100, dec.toNumber(dec.div(o.filled_quantity, o.quantity, 6)) * 100) : 0;
  return (
    <div className="rounded-3 bg-bg-1 p-3">
      <div className="flex items-center justify-between">
        <span className={cn("text-sm font-medium", o.side === "BUY" ? "text-up" : "text-down")}>
          {enumLabel(o.side)} · {enumLabel(o.type)}
        </span>
        <span className="text-xs text-fg-3">
          <TimeText value={o.created_at} format="datetime" />
        </span>
      </div>
      <div className="mt-2 grid grid-cols-3 gap-2 text-xs">
        <div>
          <div className="text-fg-3">{t("common.price")}</div>
          <div className="tabular-nums text-fg-1">{o.price ? formatPrice(o.price, pair.price_decimals) : t("codes.MARKET")}</div>
        </div>
        <div>
          <div className="text-fg-3">{t("common.amount")}</div>
          <div className="tabular-nums text-fg-1">{o.quantity ? formatAmount(o.quantity, pair.qty_decimals) : "—"}</div>
        </div>
        <div className="text-right">
          <div className="text-fg-3">{t("mTrade.filled")}</div>
          <div className="tabular-nums text-fg-1">{formatAmount(o.filled_quantity, pair.qty_decimals)}</div>
        </div>
      </div>
      {isActive(o.status) && o.quantity && <Progress value={ratio} size="xs" className="mt-2" />}
      <div className="mt-2 flex items-center justify-between text-xs">
        <span className="text-fg-2">{enumLabel(o.status)}</span>
        {action}
      </div>
    </div>
  );
}

function OpenList({ pair, query }: { pair: Pair; query: ReturnType<typeof useOpenOrders> }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [busy, setBusy] = useState<string | null>(null);
  if (query.isPending) return <Loading />;
  if (query.error) return <ErrorState compact message={errorText(query.error)} onRetry={() => void query.refetch()} />;
  const items = query.data?.items ?? [];
  if (items.length === 0) return <EmptyState compact title={t("mTrade.noOpenOrders")} />;
  const cancel = async (o: Order) => {
    setBusy(o.order_id);
    try {
      applyOrderToCaches(qc, await cancelOrder(o.order_id));
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(null);
    }
  };
  return (
    <div className="flex flex-col gap-2 p-4">
      {items.map((o) => (
        <OrderCard
          key={o.order_id}
          o={o}
          pair={pair}
          action={
            <Button size="sm" variant="secondary" className="hit-area" loading={busy === o.order_id} disabled={o.cancel_requested} onClick={() => void cancel(o)}>
              {t("mTrade.cancel")}
            </Button>
          }
        />
      ))}
    </div>
  );
}

function HistoryList({ pair }: { pair: Pair }) {
  const { t } = useTranslation();
  const q = useOrderHistory(pair.symbol);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  const items = (q.data?.pages ?? []).flatMap((p) => p.items).filter((o) => !isActive(o.status));
  if (items.length === 0) return <EmptyState compact title={t("mTrade.noHistory")} />;
  return (
    <div className="flex flex-col gap-2 p-4">
      {items.map((o) => (
        <OrderCard key={o.order_id} o={o} pair={pair} />
      ))}
      {q.hasNextPage && (
        <Button variant="ghost" loading={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
          {t("mTrade.more")}
        </Button>
      )}
    </div>
  );
}

function FillList({ pair }: { pair: Pair }) {
  const { t } = useTranslation();
  const q = useFills(pair.symbol);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  const items: Fill[] = (q.data?.pages ?? []).flatMap((p) => p.items);
  if (items.length === 0) return <EmptyState compact title={t("mTrade.noFills")} />;
  return (
    <div className="flex flex-col gap-2 p-4">
      {items.map((f) => (
        <div key={`${f.trade_id}:${f.order_id}`} className="rounded-3 bg-bg-1 p-3 text-xs">
          <div className="flex items-center justify-between">
            <span className={cn("text-sm font-medium", f.side === "BUY" ? "text-up" : "text-down")}>
              {enumLabel(f.side)} · {enumLabel(f.role)}
            </span>
            <TimeText value={f.executed_at} format="datetime" className="text-fg-3" />
          </div>
          <div className="mt-2 grid grid-cols-3 gap-2">
            <div>
              <div className="text-fg-3">{t("common.price")}</div>
              <div className="tabular-nums text-fg-1">{formatPrice(f.price, pair.price_decimals)}</div>
            </div>
            <div>
              <div className="text-fg-3">{t("common.amount")}</div>
              <div className="tabular-nums text-fg-1">{formatAmount(f.quantity, pair.qty_decimals)}</div>
            </div>
            <div className="text-right">
              <div className="text-fg-3">{t("common.fee")}</div>
              <div className="tabular-nums text-fg-1">
                {formatAmount(f.fee, 8)} {f.fee_asset}
              </div>
            </div>
          </div>
        </div>
      ))}
      {q.hasNextPage && (
        <Button variant="ghost" loading={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
          {t("mTrade.more")}
        </Button>
      )}
    </div>
  );
}
