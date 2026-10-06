import {
  adjustPositionMargin, closeableQuantity, closeAtMarket, dec, enumLabel, errorText, formatAmount, formatDecimal, formatPrice, placeConditionalOrder,
  routes, usdValue, useConditionalOrders, useContractMath, useContracts, useMarkPrice, usePositions, useTicker,
  type ConditionalOrder, type Contract, type ContractPosition, type ContractTerms,
} from "@exchange/core";
import { Button, Dialog, ErrorState, NumberInput, PositionCard, Segmented, Sheet, Skeleton, TpSlDialog, toast, type TpSlValues } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { EmptyList } from "./EmptyList";

type Spec = { price: number; qty: number; contract?: Contract };
type Specs = Map<string, Spec>;
const label = (symbol: string) => symbol.replace(/-PERP$/, "").replace("-", "");

// A linear USDT contract's terms: the arithmetic of a position whose
// contract the list no longer has (its own figures stand).
const LINEAR: ContractTerms = {
  quote_asset: "USDT", settle_asset: "USDT", contract_size: "0", tick_size: "0", lot_size: "0", price_band: "0", taker_fee_rate: "0", risk_tiers: [],
};

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
    () => new Map((contracts.data?.contracts ?? []).map((c) => [c.symbol, { price: dec.decimalsOf(c.tick_size), qty: dec.decimalsOf(c.lot_size), contract: c }])),
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

function PositionItem({ p, spec, tpsl }: { p: ContractPosition; spec: Spec; tpsl: ConditionalOrder[] }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const math = useContractMath(spec.contract ?? LINEAR);
  const long = dec.sign(p.quantity) > 0;
  const mark = useMarkPrice(p.symbol).data;
  const live = math.live(p, mark?.mark_price);
  const unit = math.inverse ? t("mTrade.contractsUnit") : undefined;
  const last = useTicker(p.symbol)?.last;
  const [closing, setClosing] = useState(false);
  const [tpslOpen, setTpslOpen] = useState(false);
  const [marginOpen, setMarginOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const size = closeableQuantity(p.quantity);

  // A market close sends what is left until the position is closed, three
  // orders at most; what a thin book leaves open is said, with a button to
  // close the rest (review FE, B129).
  const close = async (quantity = p.quantity) => {
    setBusy(true);
    try {
      const r = await closeAtMarket({ symbol: p.symbol, quantity, position_side: p.position_side });
      setClosing(false);
      const rest = long ? r.left : dec.neg(r.left);
      const again = { label: t("mTrade.continueClose"), onClick: () => void close(rest) };
      const amount = (v: string) => (unit ? `${formatAmount(v, spec.qty)} ${unit}` : formatAmount(v, spec.qty));
      if (dec.sign(r.left) === 0) toast.success(t("mTrade.closeDone"));
      else if (dec.sign(r.closed) > 0) {
        toast.info(t("mTrade.closePartly", { closed: amount(r.closed), left: amount(r.left) }), { description: t("mTrade.closePartlyHint"), action: again });
      } else toast.error(t("mTrade.closeNone"), { action: again });
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
      base={unit}
      quote={math.settle}
      quoteDecimals={math.amountDecimals}
      onClose={() => setClosing(true)}
      onTpSl={() => setTpslOpen(true)}
      onAdjustMargin={p.margin_mode === "ISOLATED" ? () => setMarginOpen(true) : undefined}
    >
      {math.inverse && spec.contract && (
        // What the contracts are worth in the coin and in USD, and the result in USD at the mark (design 2026-10-06 §2.6).
        <div data-testid="position-value" className="flex flex-wrap justify-between gap-2 text-xs tabular-nums text-fg-2">
          <span>
            {t("mTrade.positionValue", {
              coin: formatAmount(math.worth(p.quantity, live.markPrice), math.amountDecimals), asset: math.settle,
              usd: formatAmount(usdValue(p.quantity, spec.contract.contract_size), 0),
            })}
          </span>
          {dec.sign(live.markPrice) > 0 && (
            <span>{t("mTrade.pnlUsd", { value: formatDecimal(dec.mul(live.unrealizedPnl, live.markPrice), { decimals: 2, rounding: "half", sign: true }) })}</span>
          )}
        </div>
      )}
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
        description={t("mTrade.closeHint", { size: unit ? `${formatAmount(size, spec.qty)} ${unit}` : formatAmount(size, spec.qty), symbol: label(p.symbol) })}
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
        // The dialog estimates a linear contract's result; a coin-margined one's is in its coin, so none shows.
        entryPrice={math.inverse ? undefined : p.entry_price}
        quantity={math.inverse ? undefined : size}
        markPrice={mark?.mark_price}
        lastPrice={last}
        priceDecimals={spec.price}
        onConfirm={(v) => void setTpSl(v)}
        submitting={busy}
        symbol={label(p.symbol)}
      />
      {marginOpen && <MarginSheet p={p} decimals={math.amountDecimals} onDone={() => setMarginOpen(false)} />}
    </PositionCard>
  );
}

function MarginSheet({ p, decimals, onDone }: { p: ContractPosition; decimals: number; onDone: () => void }) {
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
        <NumberInput size="lg" aria-label={t("common.amount")} value={amount} onValueChange={setAmount} decimals={decimals} unit={p.settle_asset} />
        <p className="text-xs text-fg-3">
          {t("common.available")} · {formatAmount(p.margin, decimals)} {p.settle_asset}
        </p>
      </div>
    </Sheet>
  );
}
