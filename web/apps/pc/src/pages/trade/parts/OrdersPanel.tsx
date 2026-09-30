import {
  ApiError, assetDecimals, cancelAllOrders, cancelOrder, dec, enumLabel, errorText, formatAmount, formatPrice, isActive, routes, selectSignedIn, useAssets,
  useFills, useOpenOrders, useOrderHistory, usePairs, useSession, type Fill, type Order, type Pair,
} from "@exchange/core";
import {
  Button, Checkbox, DataTable, Dialog, EmptyState, Progress, Tabs, TabsPanel, TimeText, toast, cn, type ColumnDef,
} from "@exchange/ui";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { spotAvailable, useSpotBalances } from "./SpotOrderPanel";

export type OrdersTab = "open" | "history" | "fills" | "assets";

type PairMap = Map<string, Pair>;

function pairOf(pairs: PairMap, symbol: string) {
  const p = pairs.get(symbol);
  return { price: p?.price_decimals ?? 8, qty: p?.qty_decimals ?? 8, base: p?.base_asset ?? "", quote: p?.quote_asset ?? "" };
}

function sideClass(side: string) {
  return side === "BUY" ? "text-up" : "text-down";
}

/**
 * OrdersPanel: the caller's open orders (cancel one or all), order
 * history, fills and spot balances under the terminal (design §6.2). The
 * lists stay current through the private pushes; "hide other pairs"
 * narrows them to the terminal's pair.
 */
export function OrdersPanel({
  symbol, tab, onTabChange, height, className,
}: {
  symbol: string;
  tab: OrdersTab;
  onTabChange: (tab: OrdersTab) => void;
  height: number;
  className?: string;
}) {
  const { t } = useTranslation();
  const signedIn = useSession(selectSignedIn);
  const [onlyThis, setOnlyThis] = useState(false);
  const [confirmAll, setConfirmAll] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const scope = onlyThis ? symbol : "";
  const pairs = usePairs();
  const pairMap = useMemo<PairMap>(() => new Map((pairs.data?.pairs ?? []).map((p) => [p.symbol, p])), [pairs.data]);
  const open = useOpenOrders(scope);
  const openCount = open.data?.items.length ?? 0;

  const cancelAll = async () => {
    setCancelling(true);
    try {
      const n = await cancelAllOrders(scope || undefined);
      toast.success(t("pcTrade.cancelAllSent", { count: n }));
      setConfirmAll(false);
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setCancelling(false);
    }
  };

  const bodyHeight = Math.max(120, height - 40);

  return (
    <div className={cn("flex min-h-0 flex-col bg-bg-1", className)}>
      <Tabs
        value={tab}
        onValueChange={(v) => onTabChange(v as OrdersTab)}
        size="sm"
        className="min-h-0 flex-1"
        listClassName="px-3"
        items={[
          { value: "open", label: t("pcTrade.openOrders"), count: signedIn ? openCount : undefined },
          { value: "history", label: t("pcTrade.orderHistory") },
          { value: "fills", label: t("pcTrade.myFills") },
          { value: "assets", label: t("nav.assets") },
        ]}
        extra={
          signedIn && (
            <>
              <Checkbox checked={onlyThis} onCheckedChange={setOnlyThis} label={t("pcTrade.hideOthers")} className="text-xs" />
              {tab === "open" && (
                <Button size="sm" variant="ghost" disabled={openCount === 0} onClick={() => setConfirmAll(true)}>
                  {t("pcTrade.cancelAll")}
                </Button>
              )}
            </>
          )
        }
      >
        {!signedIn ? (
          <EmptyState
            compact
            title={t("state.signInToTrade")}
            action={
              <Button asChild size="sm">
                <Link to={`${routes.login}?next=${encodeURIComponent(routes.trade(symbol))}`}>{t("nav.login")}</Link>
              </Button>
            }
          />
        ) : (
          <>
            <TabsPanel value="open">
              <OpenOrders pairs={pairMap} query={open} height={bodyHeight} />
            </TabsPanel>
            <TabsPanel value="history">
              <History pairs={pairMap} symbol={scope} height={bodyHeight} enabled={tab === "history"} />
            </TabsPanel>
            <TabsPanel value="fills">
              <Fills pairs={pairMap} symbol={scope} height={bodyHeight} enabled={tab === "fills"} />
            </TabsPanel>
            <TabsPanel value="assets">
              <Assets height={bodyHeight} />
            </TabsPanel>
          </>
        )}
      </Tabs>
      <Dialog
        open={confirmAll}
        onOpenChange={setConfirmAll}
        title={t("pcTrade.cancelAllTitle")}
        description={onlyThis ? t("pcTrade.cancelAllThis", { symbol }) : t("pcTrade.cancelAllEvery", { count: openCount })}
        size="sm"
        onConfirm={() => void cancelAll()}
        confirmLoading={cancelling}
        confirmVariant="danger"
        confirmText={t("pcTrade.cancelAll")}
      />
    </div>
  );
}

function OpenOrders({ pairs, query, height }: { pairs: PairMap; query: ReturnType<typeof useOpenOrders>; height: number }) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState<string | null>(null);
  const cancel = async (o: Order) => {
    setBusy(o.order_id);
    try {
      await cancelOrder(o.order_id);
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(null);
    }
  };
  const columns = useMemo<ColumnDef<Order>[]>(
    () => [
      { id: "time", header: t("common.time"), cell: ({ row }) => <TimeText value={row.original.created_at} format="datetime" />, meta: { width: 150 } },
      { id: "pair", header: t("market.pair"), cell: ({ row }) => row.original.symbol.replace("-", "/") },
      { id: "side", header: t("pcTrade.sideType"), cell: ({ row }) => (
        <span className={sideClass(row.original.side)}>{enumLabel(row.original.side)} · {enumLabel(row.original.type)}</span>
      ) },
      { id: "price", header: t("common.price"), meta: { align: "right" }, cell: ({ row }) =>
        row.original.price ? formatPrice(row.original.price, pairOf(pairs, row.original.symbol).price) : t("codes.MARKET") },
      { id: "qty", header: t("common.amount"), meta: { align: "right" }, cell: ({ row }) => {
        const d = pairOf(pairs, row.original.symbol);
        return row.original.quantity ? formatAmount(row.original.quantity, d.qty) : `${formatAmount(row.original.quote_amount, 8)} ${d.quote}`;
      } },
      { id: "filled", header: t("pcTrade.filled"), meta: { align: "right", width: 160 }, cell: ({ row }) => {
        const o = row.original;
        const d = pairOf(pairs, o.symbol);
        const ratio = o.quantity && dec.sign(o.quantity) > 0 ? Math.min(100, dec.toNumber(dec.div(o.filled_quantity, o.quantity, 6)) * 100) : 0;
        return (
          <div className="flex flex-col items-end gap-1">
            <span>{formatAmount(o.filled_quantity, d.qty)}</span>
            {o.quantity && <Progress value={ratio} size="xs" className="w-20" />}
          </div>
        );
      } },
      { id: "status", header: t("common.status"), cell: ({ row }) => enumLabel(row.original.status) },
      { id: "action", header: t("common.action"), meta: { align: "right", width: 80 }, cell: ({ row }) => (
        <Button size="sm" variant="ghost" loading={busy === row.original.order_id} disabled={row.original.cancel_requested} onClick={() => void cancel(row.original)}>
          {t("pcTrade.cancel")}
        </Button>
      ) },
    ],
    [t, pairs, busy],
  );
  return (
    <DataTable
      columns={columns}
      data={query.data?.items ?? []}
      getRowId={(o) => o.order_id}
      loading={query.isPending}
      error={query.error}
      onRetry={() => void query.refetch()}
      density="compact"
      height={height}
      virtual
      stickyHeader
      empty={<EmptyState compact title={t("pcTrade.noOpenOrders")} />}
    />
  );
}

function History({ pairs, symbol, height, enabled }: { pairs: PairMap; symbol: string; height: number; enabled: boolean }) {
  const { t } = useTranslation();
  const q = useOrderHistory(symbol, enabled);
  const rows = useMemo(() => (q.data?.pages ?? []).flatMap((p) => p.items).filter((o) => !isActive(o.status)), [q.data]);
  const columns = useMemo<ColumnDef<Order>[]>(
    () => [
      { id: "time", header: t("common.time"), cell: ({ row }) => <TimeText value={row.original.updated_at} format="datetime" />, meta: { width: 150 } },
      { id: "pair", header: t("market.pair"), cell: ({ row }) => row.original.symbol.replace("-", "/") },
      { id: "side", header: t("pcTrade.sideType"), cell: ({ row }) => (
        <span className={sideClass(row.original.side)}>{enumLabel(row.original.side)} · {enumLabel(row.original.type)}</span>
      ) },
      { id: "price", header: t("common.price"), meta: { align: "right" }, cell: ({ row }) =>
        row.original.price ? formatPrice(row.original.price, pairOf(pairs, row.original.symbol).price) : t("codes.MARKET") },
      { id: "avg", header: t("pcTrade.avgPrice"), meta: { align: "right" }, cell: ({ row }) => {
        const o = row.original;
        return dec.sign(o.filled_quantity) > 0 ? formatPrice(dec.div(o.filled_quote, o.filled_quantity, 12), pairOf(pairs, o.symbol).price) : "—";
      } },
      { id: "filled", header: t("pcTrade.filled"), meta: { align: "right" }, cell: ({ row }) =>
        formatAmount(row.original.filled_quantity, pairOf(pairs, row.original.symbol).qty) },
      { id: "status", header: t("common.status"), cell: ({ row }) => {
        const o = row.original;
        const why = o.reject_reason ? errorText(new ApiError(0, o.reject_reason, "")) : o.cancel_reason ? enumLabel(o.cancel_reason) : "";
        return (
          <span title={why || undefined} className={cn(o.status === "REJECTED" && "text-danger")}>
            {enumLabel(o.status)}
          </span>
        );
      } },
    ],
    [t, pairs],
  );
  return (
    <DataTable
      columns={columns}
      data={rows}
      getRowId={(o) => o.order_id}
      loading={q.isPending}
      error={q.error}
      onRetry={() => void q.refetch()}
      density="compact"
      height={height}
      virtual
      stickyHeader
      onEndReached={() => q.hasNextPage && !q.isFetchingNextPage && void q.fetchNextPage()}
      loadingMore={q.isFetchingNextPage}
      hasMore={q.hasNextPage}
      empty={<EmptyState compact title={t("pcTrade.noHistory")} />}
    />
  );
}

function Fills({ pairs, symbol, height, enabled }: { pairs: PairMap; symbol: string; height: number; enabled: boolean }) {
  const { t } = useTranslation();
  const q = useFills(symbol, enabled);
  const rows = useMemo(() => (q.data?.pages ?? []).flatMap((p) => p.items), [q.data]);
  const columns = useMemo<ColumnDef<Fill>[]>(
    () => [
      { id: "time", header: t("common.time"), cell: ({ row }) => <TimeText value={row.original.executed_at} format="datetime" />, meta: { width: 150 } },
      { id: "pair", header: t("market.pair"), cell: ({ row }) => row.original.symbol.replace("-", "/") },
      { id: "side", header: t("pcTrade.side"), cell: ({ row }) => <span className={sideClass(row.original.side)}>{enumLabel(row.original.side)}</span> },
      { id: "role", header: t("pcTrade.role"), cell: ({ row }) => enumLabel(row.original.role) },
      { id: "price", header: t("common.price"), meta: { align: "right" }, cell: ({ row }) =>
        formatPrice(row.original.price, pairOf(pairs, row.original.symbol).price) },
      { id: "qty", header: t("common.amount"), meta: { align: "right" }, cell: ({ row }) =>
        formatAmount(row.original.quantity, pairOf(pairs, row.original.symbol).qty) },
      { id: "total", header: t("common.total"), meta: { align: "right" }, cell: ({ row }) => formatAmount(row.original.quote_quantity, 8) },
      { id: "fee", header: t("common.fee"), meta: { align: "right" }, cell: ({ row }) => `${formatAmount(row.original.fee, 8)} ${row.original.fee_asset}` },
    ],
    [t, pairs],
  );
  return (
    <DataTable
      columns={columns}
      data={rows}
      getRowId={(f) => `${f.trade_id}:${f.order_id}`}
      loading={q.isPending}
      error={q.error}
      onRetry={() => void q.refetch()}
      density="compact"
      height={height}
      virtual
      stickyHeader
      onEndReached={() => q.hasNextPage && !q.isFetchingNextPage && void q.fetchNextPage()}
      loadingMore={q.isFetchingNextPage}
      hasMore={q.hasNextPage}
      empty={<EmptyState compact title={t("pcTrade.noFills")} />}
    />
  );
}

type Balance = { account_type: string; asset: string; available: string; frozen: string; total: string };

function Assets({ height }: { height: number }) {
  const { t } = useTranslation();
  const q = useSpotBalances();
  const assets = useAssets();
  const rows = useMemo(() => (q.data?.balances ?? []).filter((b: Balance) => b.account_type === "SPOT"), [q.data]);
  const columns = useMemo<ColumnDef<Balance>[]>(
    () => [
      { id: "asset", header: t("pcTrade.asset"), cell: ({ row }) => <span className="font-medium">{row.original.asset}</span> },
      { id: "available", header: t("common.available"), meta: { align: "right" }, cell: ({ row }) =>
        formatAmount(spotAvailable(rows, row.original.asset), assetDecimals(assets.data?.assets, row.original.asset)) },
      { id: "frozen", header: t("codes.FROZEN"), meta: { align: "right" }, cell: ({ row }) =>
        formatAmount(row.original.frozen, assetDecimals(assets.data?.assets, row.original.asset)) },
      { id: "total", header: t("pcTrade.totalBalance"), meta: { align: "right" }, cell: ({ row }) =>
        formatAmount(row.original.total, assetDecimals(assets.data?.assets, row.original.asset)) },
      { id: "action", header: t("common.action"), meta: { align: "right" }, cell: () => (
        <span className="flex justify-end gap-3 text-brand">
          <Link to={routes.deposit}>{t("nav.deposit")}</Link>
          <Link to={routes.transfer}>{t("nav.transfer")}</Link>
        </span>
      ) },
    ],
    [t, rows, assets.data],
  );
  return (
    <DataTable
      columns={columns}
      data={rows}
      getRowId={(b) => `${b.account_type}:${b.asset}`}
      loading={q.isPending}
      error={q.error}
      onRetry={() => void q.refetch()}
      density="compact"
      height={height}
      stickyHeader
      empty={<EmptyState compact title={t("pcTrade.noAssets")} action={<Button asChild size="sm"><Link to={routes.deposit}>{t("nav.deposit")}</Link></Button>} />}
    />
  );
}
