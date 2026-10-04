import {
  adjustPositionMargin, closeableQuantity, dec, enumLabel, errorText, formatAmount, formatPrice, liveFigures, newIdempotencyKey, placeConditionalOrder,
  placeContractOrder, routes, useConditionalOrders, useContracts, useMarkPrice, usePositions, useTicker, type ConditionalOrder, type ContractPosition,
} from "@exchange/core";
import { Button, Dialog, ErrorState, NumberInput, PositionCard, Segmented, Sheet, Skeleton, TpSlDialog, toast, type TpSlValues } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { EmptyList } from "./EmptyList";

type Specs = Map<string, { price: number; qty: number }>;
const label = (symbol: string) => symbol.replace(/-PERP$/, "").replace("-", "");

/**
 * FuturesPositions: the caller's positions as cards (design §7.2) with
 * market close, take-profit/stop-loss and, for isolated ones, margin.
 * Without one it offers the order sheet (onTrade) and a transfer.
 */
export function FuturesPositions({ symbol, onTrade }: { symbol: string; onTrade?: () => void }) {
  const { t } = useTranslation();
  const q = usePositions("");
  const tpsl = useConditionalOrders("");
  const contracts = useContracts();
  const specs = useMemo<Specs>(
    () => new Map((contracts.data?.contracts ?? []).map((c) => [c.symbol, { price: dec.decimalsOf(c.tick_size), qty: dec.decimalsOf(c.lot_size) }])),
    [contracts.data],
  );
  if (q.isPending) return <Skeleton className="m-4 h-40 rounded-3" />;
  if (q.error) return <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  // This contract's positions first.
  const list = [...(q.data?.positions ?? [])].sort((a, b) => Number(b.symbol === symbol) - Number(a.symbol === symbol));
  if (list.length === 0) {
    return (
      <EmptyList
        title={t("mTrade.noPositions")}
        hint={t("mTrade.noPositionsHint")}
        action={
          <div className="flex gap-2">
            {onTrade && (
              <Button size="sm" className="hit-area" onClick={onTrade}>
                {t("mTrade.openPosition")}
              </Button>
            )}
            <Button asChild size="sm" variant="secondary" className="hit-area">
              <Link to={routes.transfer}>{t("nav.transfer")}</Link>
            </Button>
          </div>
        }
      />
    );
  }
  return (
    <div className="flex flex-col gap-3 p-4">
      {list.map((p) => (
        <PositionItem
          key={p.position_id}
          p={p}
          spec={specs.get(p.symbol) ?? { price: 2, qty: 3 }}
          tpsl={(tpsl.data?.items ?? []).filter((c) => c.symbol === p.symbol && c.position_side === p.position_side)}
        />
      ))}
    </div>
  );
}

function PositionItem({ p, spec, tpsl }: { p: ContractPosition; spec: { price: number; qty: number }; tpsl: ConditionalOrder[] }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const long = dec.sign(p.quantity) > 0;
  const mark = useMarkPrice(p.symbol).data;
  const live = liveFigures(p, mark?.mark_price);
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
          symbol: p.symbol, side: long ? "SELL" : "BUY", type: "MARKET", quantity: size, position_side: p.position_side,
          reduce_only: p.position_side === "BOTH" ? true : undefined,
        },
        newIdempotencyKey(),
      );
      toast.success(t("mTrade.closeSent"));
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
      if (v.takeProfit) {
        await placeConditionalOrder({ symbol: p.symbol, position_side: p.position_side, kind: "TAKE_PROFIT", trigger_price: v.takeProfit.price, trigger_by: v.takeProfit.triggerType });
      }
      if (v.stopLoss) {
        await placeConditionalOrder({ symbol: p.symbol, position_side: p.position_side, kind: "STOP_LOSS", trigger_price: v.stopLoss.price, trigger_by: v.stopLoss.triggerType });
      }
      toast.success(t("mTrade.tpslSet"));
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
        symbol: label(p.symbol), side: long ? "LONG" : "SHORT", quantity: size, entryPrice: p.entry_price, markPrice: live.markPrice,
        liquidationPrice: p.liquidation_price, margin: p.margin, leverage: p.leverage, unrealizedPnl: live.unrealizedPnl,
        roe: live.roe, marginMode: p.margin_mode,
      }}
      priceDecimals={spec.price}
      qtyDecimals={spec.qty}
      onClose={() => setClosing(true)}
      onTpSl={() => setTpslOpen(true)}
      onAdjustMargin={p.margin_mode === "ISOLATED" ? () => setMarginOpen(true) : undefined}
    >
      {tpsl.length > 0 && (
        <div className="flex flex-wrap gap-2 text-xs text-fg-2">
          {tpsl.map((c) => (
            <span key={c.conditional_id} className="rounded-1 bg-bg-3 px-2 py-0.5">
              {enumLabel(c.kind)} {formatPrice(c.trigger_price, spec.price)}
            </span>
          ))}
        </div>
      )}
      <Dialog
        open={closing}
        onOpenChange={setClosing}
        title={t("mTrade.closePosition")}
        description={t("mTrade.closeHint", { size: formatAmount(size, spec.qty), symbol: label(p.symbol) })}
        size="sm"
        onConfirm={() => void close()}
        confirmLoading={busy}
        confirmVariant={long ? "sell" : "buy"}
        confirmText={long ? t("mTrade.closeLong") : t("mTrade.closeShort")}
      />
      <TpSlDialog
        open={tpslOpen}
        onOpenChange={setTpslOpen}
        side={long ? "LONG" : "SHORT"}
        entryPrice={p.entry_price}
        quantity={size}
        markPrice={mark?.mark_price}
        lastPrice={last}
        priceDecimals={spec.price}
        onConfirm={(v) => void setTpSl(v)}
        submitting={busy}
        symbol={label(p.symbol)}
      />
      {marginOpen && <MarginSheet p={p} onDone={() => setMarginOpen(false)} />}
    </PositionCard>
  );
}

function MarginSheet({ p, onDone }: { p: ContractPosition; onDone: () => void }) {
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
      toast.success(t("mTrade.marginAdjusted"));
      void qc.invalidateQueries({ queryKey: ["derivatives"] });
      onDone();
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onDone()}
      title={t("mTrade.adjustMargin")}
      footer={
        <Button size="lg" block loading={busy} onClick={() => void submit()}>
          {t("common.confirm")}
        </Button>
      }
    >
      <div className="flex flex-col gap-3 pb-2">
        <Segmented
          block
          value={mode}
          onValueChange={(m) => setMode(m as "add" | "remove")}
          items={[
            { value: "add", label: t("mTrade.addMargin") },
            { value: "remove", label: t("mTrade.removeMargin") },
          ]}
        />
        <NumberInput size="lg" aria-label={t("common.amount")} value={amount} onValueChange={setAmount} decimals={2} unit="USDT" />
        <p className="text-xs text-fg-3">
          {t("common.available")} · {formatAmount(p.margin, 2)} USDT
        </p>
      </div>
    </Sheet>
  );
}
