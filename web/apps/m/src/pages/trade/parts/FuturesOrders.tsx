import {
  cancelContractOrder, dec, enumLabel, errorText, formatAmount, formatPrice, isActive, routes, selectSignedIn, useContractOpenOrders,
  useContractOrderHistory, useSession, type Contract, type ContractOrder,
} from "@exchange/core";
import { Button, EmptyState, ErrorState, Skeleton, Tabs, TimeText, cn, toast } from "@exchange/ui";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { EmptyList } from "./EmptyList";

/** FuturesOrders: the contract's open orders (cancel) and history as cards (design §7.2); an empty list offers the order sheet (onTrade). */
export function FuturesOrders({ contract, onTrade }: { contract: Contract; onTrade?: () => void }) {
  const { t } = useTranslation();
  const signedIn = useSession(selectSignedIn);
  const [tab, setTab] = useState("open");
  const open = useContractOpenOrders(contract.symbol);
  if (!signedIn) {
    return (
      <EmptyState
        compact
        title={t("state.signInToTrade")}
        action={
          <Button asChild size="sm" className="hit-area">
            <Link to={`${routes.login}?next=${encodeURIComponent(routes.futures(contract.symbol))}`}>{t("nav.login")}</Link>
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
        ]}
      />
      {tab === "open" ? <OpenList contract={contract} query={open} onTrade={onTrade} /> : <HistoryList contract={contract} onTrade={onTrade} />}
    </div>
  );
}

function Card({ o, contract, action }: { o: ContractOrder; contract: Contract; action?: ReactNode }) {
  const { t } = useTranslation();
  const pd = dec.decimalsOf(contract.tick_size);
  const qd = dec.decimalsOf(contract.lot_size);
  return (
    <div className="rounded-3 bg-bg-1 p-3 text-xs">
      <div className="flex items-center justify-between">
        <span className={cn("text-sm font-medium", o.side === "BUY" ? "text-up" : "text-down")}>
          {enumLabel(o.side)} · {enumLabel(o.type)}
          {o.reduce_only && ` · ${t("mTrade.reduceOnly")}`}
        </span>
        <TimeText value={o.created_at} format="datetime" className="text-fg-3" />
      </div>
      <div className="mt-2 grid grid-cols-3 gap-2">
        <div>
          <div className="text-fg-3">{t("common.price")}</div>
          <div className="tabular-nums text-fg-1">{o.type === "MARKET" ? t("codes.MARKET") : formatPrice(o.price, pd)}</div>
        </div>
        <div>
          <div className="text-fg-3">{t("common.amount")}</div>
          <div className="tabular-nums text-fg-1">{formatAmount(o.quantity, qd)}</div>
        </div>
        <div className="text-right">
          <div className="text-fg-3">{t("mTrade.filled")}</div>
          <div className="tabular-nums text-fg-1">{formatAmount(o.filled_quantity, qd)}</div>
        </div>
      </div>
      <div className="mt-2 flex items-center justify-between">
        <span className="text-fg-2">
          {enumLabel(o.margin_mode)} {o.leverage}x · {enumLabel(o.status)}
        </span>
        {action}
      </div>
    </div>
  );
}

function OpenList({ contract, query, onTrade }: { contract: Contract; query: ReturnType<typeof useContractOpenOrders>; onTrade?: () => void }) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState<string | null>(null);
  if (query.isPending) return <Skeleton className="m-4 h-20 rounded-3" />;
  if (query.error) return <ErrorState compact message={errorText(query.error)} onRetry={() => void query.refetch()} />;
  const items = query.data?.items ?? [];
  if (items.length === 0) return <EmptyList title={t("mTrade.noOpenOrders")} hint={t("mTrade.noOpenOrdersHint")} onTrade={onTrade} />;
  const cancel = async (o: ContractOrder) => {
    setBusy(o.order_id);
    try {
      await cancelContractOrder(o.order_id);
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(null);
    }
  };
  return (
    <div className="flex flex-col gap-2 p-4">
      {items.map((o) => (
        <Card
          key={o.order_id}
          o={o}
          contract={contract}
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

function HistoryList({ contract, onTrade }: { contract: Contract; onTrade?: () => void }) {
  const { t } = useTranslation();
  const q = useContractOrderHistory(contract.symbol);
  if (q.isPending) return <Skeleton className="m-4 h-20 rounded-3" />;
  if (q.error) return <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  const items = (q.data?.pages ?? []).flatMap((p) => p.items).filter((o) => !isActive(o.status));
  if (items.length === 0) return <EmptyList title={t("mTrade.noHistory")} hint={t("mTrade.noHistoryHint")} onTrade={onTrade} />;
  return (
    <div className="flex flex-col gap-2 p-4">
      {items.map((o) => (
        <Card key={o.order_id} o={o} contract={contract} />
      ))}
      {q.hasNextPage && (
        <Button variant="ghost" loading={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
          {t("mTrade.more")}
        </Button>
      )}
    </div>
  );
}
