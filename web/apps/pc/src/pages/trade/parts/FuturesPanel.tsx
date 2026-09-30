import {
  adjustPositionMargin, cancelAllContractOrders, cancelConditionalOrder, cancelContractOrder, closeableQuantity, dec, enumLabel, errorText,
  formatAmount, formatPercent, formatPrice, isActive, newIdempotencyKey, placeConditionalOrder, placeContractOrder, roe, routes, selectSignedIn,
  useConditionalOrders, useContractFills, useContractOpenOrders, useContractOrderHistory, useContracts, useFundingPayments, useMarkPrice,
  usePositions, useSession, useTicker, type ConditionalOrder, type Contract, type ContractFill, type ContractOrder, type ContractPosition,
  type FundingPayment,
} from "@exchange/core";
import {
  Button, Checkbox, DataTable, Dialog, EmptyState, NumberInput, PositionCard, Segmented, Tabs, TabsPanel, TimeText, TpSlDialog, toast, cn,
  type ColumnDef, type TpSlValues,
} from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

export type FuturesTab = "positions" | "open" | "tpsl" | "history" | "fills" | "funding";

type Specs = Map<string, { price: number; qty: number }>;

function useSpecs(): Specs {
  const contracts = useContracts();
  return useMemo(
    () => new Map((contracts.data?.contracts ?? []).map((c) => [c.symbol, { price: dec.decimalsOf(c.tick_size), qty: dec.decimalsOf(c.lot_size) }])),
    [contracts.data],
  );
}

const spec = (s: Specs, symbol: string) => s.get(symbol) ?? { price: 2, qty: 3 };
const label = (symbol: string) => symbol.replace(/-PERP$/, "").replace("-", "");
const sideClass = (side: string) => (side === "BUY" ? "text-up" : "text-down");

/**
 * FuturesPanel: positions (close, take-profit/stop-loss, margin), open
 * orders, take-profit/stop-loss orders, order history, fills and funding
 * payments under the futures terminal (design §6.2).
 */
export function FuturesPanel({
  contract, tab, onTabChange, height, className,
}: {
  contract: Contract;
  tab: FuturesTab;
  onTabChange: (tab: FuturesTab) => void;
  height: number;
  className?: string;
}) {
  const { t } = useTranslation();
  const signedIn = useSession(selectSignedIn);
  const [onlyThis, setOnlyThis] = useState(false);
  const [confirmAll, setConfirmAll] = useState(false);
  const scope = onlyThis ? contract.symbol : "";
  const positions = usePositions(scope);
  const open = useContractOpenOrders(scope);
  const tpsl = useConditionalOrders(scope);
  const specs = useSpecs();
  const bodyHeight = Math.max(120, height - 40);

  const cancelAll = async () => {
    try {
      const n = await cancelAllContractOrders(scope || undefined);
      toast.success(t("pcTrade.cancelAllSent", { count: n }));
      setConfirmAll(false);
    } catch (e) {
      toast.error(errorText(e));
    }
  };

  return (
    <div className={cn("flex min-h-0 flex-col bg-bg-1", className)}>
      <Tabs
        value={tab}
        onValueChange={(v) => onTabChange(v as FuturesTab)}
        size="sm"
        className="min-h-0 flex-1"
        listClassName="px-3"
        items={[
          { value: "positions", label: t("pcTrade.positions"), count: signedIn ? (positions.data?.positions.length ?? 0) : undefined },
          { value: "open", label: t("pcTrade.openOrders"), count: signedIn ? (open.data?.items.length ?? 0) : undefined },
          { value: "tpsl", label: t("pcTrade.tpsl"), count: signedIn ? (tpsl.data?.items.length ?? 0) : undefined },
          { value: "history", label: t("pcTrade.orderHistory") },
          { value: "fills", label: t("pcTrade.myFills") },
          { value: "funding", label: t("pcTrade.fundingFees") },
        ]}
        extra={
          signedIn && (
            <>
              <Checkbox checked={onlyThis} onCheckedChange={setOnlyThis} label={t("pcTrade.hideOthers")} className="text-xs" />
              {tab === "open" && (
                <Button size="sm" variant="ghost" disabled={(open.data?.items.length ?? 0) === 0} onClick={() => setConfirmAll(true)}>
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
                <Link to={`${routes.login}?next=${encodeURIComponent(routes.futures(contract.symbol))}`}>{t("nav.login")}</Link>
              </Button>
            }
          />
        ) : (
          <>
            <TabsPanel value="positions">
              <Positions query={positions} specs={specs} tpsl={tpsl.data?.items ?? []} height={bodyHeight} />
            </TabsPanel>
            <TabsPanel value="open">
              <OpenOrders query={open} specs={specs} height={bodyHeight} />
            </TabsPanel>
            <TabsPanel value="tpsl">
              <TpSlOrders query={tpsl} specs={specs} height={bodyHeight} />
            </TabsPanel>
            <TabsPanel value="history">
              <History symbol={scope} specs={specs} height={bodyHeight} enabled={tab === "history"} />
            </TabsPanel>
            <TabsPanel value="fills">
              <Fills symbol={scope} specs={specs} height={bodyHeight} enabled={tab === "fills"} />
            </TabsPanel>
            <TabsPanel value="funding">
              <Funding symbol={scope} specs={specs} height={bodyHeight} enabled={tab === "funding"} />
            </TabsPanel>
          </>
        )}
      </Tabs>
      <Dialog
        open={confirmAll}
        onOpenChange={setConfirmAll}
        title={t("pcTrade.cancelAllTitle")}
        description={onlyThis ? t("pcTrade.cancelAllThis", { symbol: label(contract.symbol) }) : t("pcTrade.cancelAllEvery", { count: open.data?.items.length ?? 0 })}
        size="sm"
        onConfirm={() => void cancelAll()}
        confirmVariant="danger"
        confirmText={t("pcTrade.cancelAll")}
      />
    </div>
  );
}

function Positions({
  query, specs, tpsl, height,
}: {
  query: ReturnType<typeof usePositions>;
  specs: Specs;
  tpsl: ConditionalOrder[];
  height: number;
}) {
  const { t } = useTranslation();
  const list = query.data?.positions ?? [];
  if (query.isPending) return <p className="p-6 text-center text-sm text-fg-3">{t("common.loading")}</p>;
  if (list.length === 0) return <EmptyState compact title={t("pcTrade.noPositions")} />;
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(340px,1fr))] gap-3 overflow-y-auto p-3" style={{ maxHeight: height }}>
      {list.map((p) => (
        <PositionItem key={p.position_id} p={p} specs={specs} tpsl={tpsl.filter((c) => c.symbol === p.symbol && c.position_side === p.position_side)} />
      ))}
    </div>
  );
}

function PositionItem({ p, specs, tpsl }: { p: ContractPosition; specs: Specs; tpsl: ConditionalOrder[] }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const d = spec(specs, p.symbol);
  const long = dec.sign(p.quantity) > 0;
  const mark = useMarkPrice(p.symbol).data;
  const last = useTicker(p.symbol)?.last;
  const [closing, setClosing] = useState(false);
  const [tpslOpen, setTpslOpen] = useState(false);
  const [marginOpen, setMarginOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const size = closeableQuantity(p.quantity);

  const close = async () => {
    setBusy(true);
    try {
      await placeContractOrder(
        {
          symbol: p.symbol, side: long ? "SELL" : "BUY", type: "MARKET", quantity: size,
          position_side: p.position_side, reduce_only: p.position_side === "BOTH" ? true : undefined,
        },
        newIdempotencyKey(),
      );
      toast.success(t("pcTrade.closeSent"));
      setClosing(false);
      void qc.invalidateQueries({ queryKey: ["derivatives"] });
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const setTpSl = async (v: TpSlValues) => {
    setBusy(true);
    try {
      const legs = [
        v.takeProfit && { kind: "TAKE_PROFIT" as const, leg: v.takeProfit },
        v.stopLoss && { kind: "STOP_LOSS" as const, leg: v.stopLoss },
      ].filter(Boolean) as { kind: "TAKE_PROFIT" | "STOP_LOSS"; leg: NonNullable<TpSlValues["takeProfit"]> }[];
      for (const l of legs) {
        await placeConditionalOrder({ symbol: p.symbol, position_side: p.position_side, kind: l.kind, trigger_price: l.leg.price, trigger_by: l.leg.triggerType });
      }
      toast.success(t("pcTrade.tpslSet"));
      setTpslOpen(false);
      void qc.invalidateQueries({ queryKey: ["derivatives"] });
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <PositionCard
      position={{
        symbol: label(p.symbol), side: long ? "LONG" : "SHORT", quantity: size, entryPrice: p.entry_price, markPrice: p.mark_price ?? "0",
        liquidationPrice: p.liquidation_price, margin: p.margin, leverage: p.leverage, unrealizedPnl: p.unrealized_pnl ?? "0",
        roe: roe(p.unrealized_pnl, p.margin), marginMode: p.margin_mode,
      }}
      priceDecimals={d.price}
      qtyDecimals={d.qty}
      onClose={() => setClosing(true)}
      onTpSl={() => setTpslOpen(true)}
      onAdjustMargin={p.margin_mode === "ISOLATED" ? () => setMarginOpen(true) : undefined}
    >
      {tpsl.length > 0 && (
        <div className="flex flex-wrap gap-2 text-xs text-fg-2">
          {tpsl.map((c) => (
            <span key={c.conditional_id} className="rounded-1 bg-bg-3 px-2 py-0.5">
              {enumLabel(c.kind)} {formatPrice(c.trigger_price, d.price)}
            </span>
          ))}
        </div>
      )}
      <Dialog
        open={closing}
        onOpenChange={setClosing}
        title={t("pcTrade.closePosition")}
        description={t("pcTrade.closeHint", { size: formatAmount(size, d.qty), symbol: label(p.symbol) })}
        size="sm"
        onConfirm={() => void close()}
        confirmLoading={busy}
        confirmVariant={long ? "sell" : "buy"}
        confirmText={long ? t("pcTrade.closeLong") : t("pcTrade.closeShort")}
      />
      <TpSlDialog
        open={tpslOpen}
        onOpenChange={setTpslOpen}
        side={long ? "LONG" : "SHORT"}
        entryPrice={p.entry_price}
        quantity={size}
        markPrice={mark?.mark_price}
        lastPrice={last}
        priceDecimals={d.price}
        onConfirm={(v) => void setTpSl(v)}
        submitting={busy}
        symbol={label(p.symbol)}
      />
      {marginOpen && <MarginDialog p={p} onDone={() => setMarginOpen(false)} />}
    </PositionCard>
  );
}

function MarginDialog({ p, onDone }: { p: ContractPosition; onDone: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [mode, setMode] = useState<"add" | "remove">("add");
  const [amount, setAmount] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    if (!dec.isDecimal(amount || "x") || dec.sign(amount) <= 0) return;
    setBusy(true);
    try {
      await adjustPositionMargin(p.symbol, mode === "add" ? amount : dec.neg(amount), p.position_side);
      toast.success(t("pcTrade.marginAdjusted"));
      void qc.invalidateQueries({ queryKey: ["derivatives"] });
      onDone();
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onDone()} title={t("pcTrade.adjustMargin")} size="sm" onConfirm={() => void submit()} confirmLoading={busy}>
      <div className="flex flex-col gap-3">
        <Segmented
          block
          value={mode}
          onValueChange={(m) => setMode(m as "add" | "remove")}
          items={[
            { value: "add", label: t("pcTrade.addMargin") },
            { value: "remove", label: t("pcTrade.removeMargin") },
          ]}
        />
        <NumberInput aria-label={t("common.amount")} value={amount} onValueChange={setAmount} decimals={2} unit="USDT" autoFocus />
        <p className="text-xs text-fg-3">{t("pcTrade.currentMargin", { value: formatAmount(p.margin, 2) })}</p>
      </div>
    </Dialog>
  );
}

function OpenOrders({ query, specs, height }: { query: ReturnType<typeof useContractOpenOrders>; specs: Specs; height: number }) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState<string | null>(null);
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
  const columns = useMemo<ColumnDef<ContractOrder>[]>(
    () => [
      { id: "time", header: t("common.time"), cell: ({ row }) => <TimeText value={row.original.created_at} format="datetime" />, meta: { width: 150 } },
      { id: "contract", header: t("pcTrade.contract"), cell: ({ row }) => label(row.original.symbol) },
      { id: "side", header: t("pcTrade.sideType"), cell: ({ row }) => (
        <span className={sideClass(row.original.side)}>
          {enumLabel(row.original.side)} · {enumLabel(row.original.type)}
          {row.original.reduce_only && ` · ${t("pcTrade.reduceOnly")}`}
        </span>
      ) },
      { id: "price", header: t("common.price"), meta: { align: "right" }, cell: ({ row }) =>
        row.original.type === "MARKET" ? t("codes.MARKET") : formatPrice(row.original.price, spec(specs, row.original.symbol).price) },
      { id: "qty", header: t("common.amount"), meta: { align: "right" }, cell: ({ row }) => formatAmount(row.original.quantity, spec(specs, row.original.symbol).qty) },
      { id: "filled", header: t("pcTrade.filled"), meta: { align: "right" }, cell: ({ row }) => formatAmount(row.original.filled_quantity, spec(specs, row.original.symbol).qty) },
      { id: "lev", header: t("pcTrade.leverage"), cell: ({ row }) => `${enumLabel(row.original.margin_mode)} ${row.original.leverage}x` },
      { id: "status", header: t("common.status"), cell: ({ row }) => enumLabel(row.original.status) },
      { id: "action", header: t("common.action"), meta: { align: "right", width: 80 }, cell: ({ row }) => (
        <Button size="sm" variant="ghost" loading={busy === row.original.order_id} disabled={row.original.cancel_requested} onClick={() => void cancel(row.original)}>
          {t("pcTrade.cancel")}
        </Button>
      ) },
    ],
    [t, specs, busy],
  );
  return (
    <DataTable columns={columns} data={query.data?.items ?? []} getRowId={(o) => o.order_id} loading={query.isPending} error={query.error}
      onRetry={() => void query.refetch()} density="compact" height={height} virtual stickyHeader empty={<EmptyState compact title={t("pcTrade.noOpenOrders")} />} />
  );
}

function TpSlOrders({ query, specs, height }: { query: ReturnType<typeof useConditionalOrders>; specs: Specs; height: number }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const cancel = async (c: ConditionalOrder) => {
    try {
      await cancelConditionalOrder(c.conditional_id);
      void qc.invalidateQueries({ queryKey: ["derivatives"] });
    } catch (e) {
      toast.error(errorText(e));
    }
  };
  const columns = useMemo<ColumnDef<ConditionalOrder>[]>(
    () => [
      { id: "time", header: t("common.time"), cell: ({ row }) => <TimeText value={row.original.created_at} format="datetime" />, meta: { width: 150 } },
      { id: "contract", header: t("pcTrade.contract"), cell: ({ row }) => label(row.original.symbol) },
      { id: "kind", header: t("pcTrade.kind"), cell: ({ row }) => enumLabel(row.original.kind) },
      { id: "trigger", header: t("pcTrade.trigger"), meta: { align: "right" }, cell: ({ row }) =>
        `${row.original.trigger_by === "MARK" ? t("pcTrade.markPrice") : t("market.last")} ${formatPrice(row.original.trigger_price, spec(specs, row.original.symbol).price)}` },
      { id: "qty", header: t("common.amount"), meta: { align: "right" }, cell: ({ row }) =>
        row.original.quantity ? formatAmount(row.original.quantity, spec(specs, row.original.symbol).qty) : t("pcTrade.wholePosition") },
      { id: "action", header: t("common.action"), meta: { align: "right", width: 80 }, cell: ({ row }) => (
        <Button size="sm" variant="ghost" onClick={() => void cancel(row.original)}>
          {t("pcTrade.cancel")}
        </Button>
      ) },
    ],
    [t, specs],
  );
  return (
    <DataTable columns={columns} data={query.data?.items ?? []} getRowId={(c) => c.conditional_id} loading={query.isPending} error={query.error}
      onRetry={() => void query.refetch()} density="compact" height={height} virtual stickyHeader empty={<EmptyState compact title={t("pcTrade.noTpsl")} />} />
  );
}

function History({ symbol, specs, height, enabled }: { symbol: string; specs: Specs; height: number; enabled: boolean }) {
  const { t } = useTranslation();
  const q = useContractOrderHistory(symbol, enabled);
  const rows = useMemo(() => (q.data?.pages ?? []).flatMap((p) => p.items).filter((o) => !isActive(o.status)), [q.data]);
  const columns = useMemo<ColumnDef<ContractOrder>[]>(
    () => [
      { id: "time", header: t("common.time"), cell: ({ row }) => <TimeText value={row.original.updated_at} format="datetime" />, meta: { width: 150 } },
      { id: "contract", header: t("pcTrade.contract"), cell: ({ row }) => label(row.original.symbol) },
      { id: "side", header: t("pcTrade.sideType"), cell: ({ row }) => (
        <span className={sideClass(row.original.side)}>{enumLabel(row.original.side)} · {enumLabel(row.original.type)}</span>
      ) },
      { id: "avg", header: t("pcTrade.avgPrice"), meta: { align: "right" }, cell: ({ row }) =>
        row.original.average_price ? formatPrice(row.original.average_price, spec(specs, row.original.symbol).price) : "—" },
      { id: "filled", header: t("pcTrade.filled"), meta: { align: "right" }, cell: ({ row }) =>
        `${formatAmount(row.original.filled_quantity, spec(specs, row.original.symbol).qty)} / ${formatAmount(row.original.quantity, spec(specs, row.original.symbol).qty)}` },
      { id: "pnl", header: t("pcTrade.realizedPnl"), meta: { align: "right" }, cell: ({ row }) => (
        <span className={cn(dec.sign(row.original.realized_pnl) > 0 && "text-up", dec.sign(row.original.realized_pnl) < 0 && "text-down")}>
          {formatAmount(row.original.realized_pnl, 2)}
        </span>
      ) },
      { id: "status", header: t("common.status"), cell: ({ row }) => enumLabel(row.original.status) },
    ],
    [t, specs],
  );
  return (
    <DataTable columns={columns} data={rows} getRowId={(o) => o.order_id} loading={q.isPending} error={q.error} onRetry={() => void q.refetch()}
      density="compact" height={height} virtual stickyHeader onEndReached={() => q.hasNextPage && !q.isFetchingNextPage && void q.fetchNextPage()}
      loadingMore={q.isFetchingNextPage} hasMore={q.hasNextPage} empty={<EmptyState compact title={t("pcTrade.noHistory")} />} />
  );
}

function Fills({ symbol, specs, height, enabled }: { symbol: string; specs: Specs; height: number; enabled: boolean }) {
  const { t } = useTranslation();
  const q = useContractFills(symbol, enabled);
  const rows = useMemo(() => (q.data?.pages ?? []).flatMap((p) => p.items), [q.data]);
  const columns = useMemo<ColumnDef<ContractFill>[]>(
    () => [
      { id: "time", header: t("common.time"), cell: ({ row }) => <TimeText value={row.original.executed_at} format="datetime" />, meta: { width: 150 } },
      { id: "contract", header: t("pcTrade.contract"), cell: ({ row }) => label(row.original.symbol) },
      { id: "side", header: t("pcTrade.side"), cell: ({ row }) => (
        <span className={sideClass(row.original.side)}>
          {enumLabel(row.original.side)}
          {row.original.liquidation && ` · ${t("pcTrade.liquidation")}`}
        </span>
      ) },
      { id: "price", header: t("common.price"), meta: { align: "right" }, cell: ({ row }) => formatPrice(row.original.price, spec(specs, row.original.symbol).price) },
      { id: "qty", header: t("common.amount"), meta: { align: "right" }, cell: ({ row }) => formatAmount(row.original.quantity, spec(specs, row.original.symbol).qty) },
      { id: "fee", header: t("common.fee"), meta: { align: "right" }, cell: ({ row }) => `${formatAmount(row.original.fee, 4)} USDT` },
      { id: "pnl", header: t("pcTrade.realizedPnl"), meta: { align: "right" }, cell: ({ row }) => (
        <span className={cn(dec.sign(row.original.realized_pnl) > 0 && "text-up", dec.sign(row.original.realized_pnl) < 0 && "text-down")}>
          {formatAmount(row.original.realized_pnl, 2)}
        </span>
      ) },
    ],
    [t, specs],
  );
  return (
    <DataTable columns={columns} data={rows} getRowId={(f) => `${f.trade_id}:${f.order_id}`} loading={q.isPending} error={q.error}
      onRetry={() => void q.refetch()} density="compact" height={height} virtual stickyHeader
      onEndReached={() => q.hasNextPage && !q.isFetchingNextPage && void q.fetchNextPage()} loadingMore={q.isFetchingNextPage} hasMore={q.hasNextPage}
      empty={<EmptyState compact title={t("pcTrade.noFills")} />} />
  );
}

function Funding({ symbol, specs, height, enabled }: { symbol: string; specs: Specs; height: number; enabled: boolean }) {
  const { t } = useTranslation();
  const q = useFundingPayments(symbol, enabled);
  const rows = useMemo(() => (q.data?.pages ?? []).flatMap((p) => p.items), [q.data]);
  const columns = useMemo<ColumnDef<FundingPayment>[]>(
    () => [
      { id: "time", header: t("common.time"), cell: ({ row }) => <TimeText value={row.original.funding_time} format="datetime" />, meta: { width: 150 } },
      { id: "contract", header: t("pcTrade.contract"), cell: ({ row }) => label(row.original.symbol) },
      { id: "qty", header: t("pcTrade.positionSize"), meta: { align: "right" }, cell: ({ row }) => formatAmount(row.original.quantity, spec(specs, row.original.symbol).qty) },
      { id: "rate", header: t("pcTrade.fundingRate"), meta: { align: "right" }, cell: ({ row }) => formatPercent(row.original.funding_rate, 4) },
      { id: "mark", header: t("pcTrade.markPrice"), meta: { align: "right" }, cell: ({ row }) => formatPrice(row.original.mark_price, spec(specs, row.original.symbol).price) },
      { id: "amount", header: t("pcTrade.fundingAmount"), meta: { align: "right" }, cell: ({ row }) => (
        <span className={cn(dec.sign(row.original.amount) > 0 ? "text-up" : "text-down")}>{formatAmount(row.original.amount, 4)} USDT</span>
      ) },
    ],
    [t, specs],
  );
  return (
    <DataTable columns={columns} data={rows} getRowId={(f) => `${f.symbol}:${f.funding_time}:${f.position_side}`} loading={q.isPending} error={q.error}
      onRetry={() => void q.refetch()} density="compact" height={height} virtual stickyHeader
      onEndReached={() => q.hasNextPage && !q.isFetchingNextPage && void q.fetchNextPage()} loadingMore={q.isFetchingNextPage} hasMore={q.hasNextPage}
      empty={<EmptyState compact title={t("pcTrade.noFunding")} />} />
  );
}
