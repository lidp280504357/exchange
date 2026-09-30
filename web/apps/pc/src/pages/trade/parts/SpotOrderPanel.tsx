import {
  accountApi, ApiError, assetDecimals, errorText, formatAmount, formatPrice, newIdempotencyKey, placeOrder, qk, routes, selectSignedIn, tradable,
  unwrap, useAssets, useSession, useSettings, useTicker, dec, type NewOrder, type Pair,
} from "@exchange/core";
import { Checkbox, Dialog, KeyValue, OrderForm, toast, type OrderFormValues, type OrderSide, type OrderType, type PairRules } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router";

type Balance = { account_type: string; asset: string; available: string };

/** useSpotBalances reads the balances the private sync keeps current. */
export function useSpotBalances() {
  const signedIn = useSession(selectSignedIn);
  return useQuery({ queryKey: qk.balances, queryFn: () => unwrap(accountApi.GET("/v1/account/balances")), enabled: signedIn, staleTime: 60_000 });
}

export function spotAvailable(list: Balance[] | undefined, asset: string): string {
  return list?.find((b) => b.account_type === "SPOT" && b.asset === asset)?.available ?? "0";
}

/** toNewOrder turns the form's values into the API's order. */
export function toNewOrder(symbol: string, v: OrderFormValues): NewOrder {
  if (v.type === "limit") return { symbol, side: v.side, type: "LIMIT", price: v.price, quantity: v.quantity, time_in_force: "GTC" };
  if (v.side === "BUY") return { symbol, side: "BUY", type: "MARKET", quote_amount: v.quoteAmount };
  return { symbol, side: "SELL", type: "MARKET", quantity: v.quantity };
}

export type SpotOrderPanelProps = {
  pair: Pair;
  side: OrderSide;
  onSideChange: (side: OrderSide) => void;
  fill: { price?: string; quantity?: string } | null;
  /** Called after an order is accepted (the page shows the open orders). */
  onPlaced?: () => void;
  className?: string;
};

/**
 * SpotOrderPanel: the order form with the pair's rules and the spot
 * balances, a confirmation (unless turned off in settings), and the
 * outcome as a toast; errors say what to do (a deposit link when the
 * balance is short).
 */
export function SpotOrderPanel({ pair, side, onSideChange, fill, onPlaced, className }: SpotOrderPanelProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const signedIn = useSession(selectSignedIn);
  const confirmOrders = useSettings((s) => s.confirmOrders);
  const setSettings = useSettings((s) => s.set);
  const [type, setType] = useState<OrderType>("limit");
  const [pending, setPending] = useState<NewOrder | null>(null);
  const [skipNext, setSkipNext] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [resetKey, setResetKey] = useState(0);
  const tk = useTicker(pair.symbol);
  const assets = useAssets();
  const balances = useSpotBalances();
  const baseDecimals = assetDecimals(assets.data?.assets, pair.base_asset);
  const quoteDecimals = assetDecimals(assets.data?.assets, pair.quote_asset);

  const rules = useMemo<PairRules>(
    () => ({
      base: pair.base_asset, quote: pair.quote_asset, tickSize: pair.tick_size, lotSize: pair.lot_size, minQuantity: pair.min_quantity,
      maxQuantity: pair.max_quantity, minNotional: pair.min_notional, makerFeeRate: pair.maker_fee_rate, takerFeeRate: pair.taker_fee_rate,
    }),
    [pair],
  );
  const available = signedIn && balances.data
    ? { base: spotAvailable(balances.data.balances, pair.base_asset), quote: spotAvailable(balances.data.balances, pair.quote_asset) }
    : null;

  const send = async (order: NewOrder) => {
    setSubmitting(true);
    try {
      const placed = await placeOrder(order, newIdempotencyKey());
      setResetKey((k) => k + 1);
      if (placed.status === "REJECTED") {
        toast.error(t("pcTrade.rejected"), { description: placed.reject_reason ? errorText(new ApiError(0, placed.reject_reason, "")) : undefined });
      } else {
        toast.success(t("pcTrade.placed"), { action: onPlaced ? { label: t("pcTrade.viewOrders"), onClick: onPlaced } : undefined });
        onPlaced?.();
      }
    } catch (e) {
      const short = e instanceof ApiError && e.code === "LEDGER_INSUFFICIENT_BALANCE";
      toast.error(errorText(e), short ? { action: { label: t("nav.deposit"), onClick: () => navigate(routes.deposit) } } : undefined);
    } finally {
      setSubmitting(false);
    }
  };

  const submit = (v: OrderFormValues) => {
    const order = toNewOrder(pair.symbol, v);
    if (confirmOrders) {
      setSkipNext(false);
      setPending(order);
    } else void send(order);
  };

  const confirm = () => {
    if (!pending) return;
    if (skipNext) setSettings({ confirmOrders: false });
    const order = pending;
    setPending(null);
    void send(order);
  };

  return (
    <div className={className}>
      <OrderForm
        side={side}
        onSideChange={onSideChange}
        type={type}
        onTypeChange={setType}
        pair={rules}
        available={available}
        lastPrice={tk?.last}
        onSubmit={submit}
        submitting={submitting || !tradable(pair.status)}
        signedIn={signedIn}
        onSignIn={() => navigate(`${routes.login}?next=${encodeURIComponent(location.pathname)}`)}
        onDeposit={() => navigate(routes.deposit)}
        fill={fill}
        resetKey={resetKey}
        baseDecimals={baseDecimals}
        quoteDecimals={quoteDecimals}
      />
      {!tradable(pair.status) && <p className="px-4 pb-3 text-xs text-warn">{t("pcTrade.notTrading")}</p>}
      <Dialog
        open={pending !== null}
        onOpenChange={(o) => !o && setPending(null)}
        title={t("pcTrade.confirmTitle")}
        size="sm"
        onConfirm={confirm}
        confirmText={pending?.side === "SELL" ? t("common.sell") : t("common.buy")}
        confirmVariant={pending?.side === "SELL" ? "sell" : "buy"}
      >
        {pending && (
          <div className="flex flex-col gap-3">
            <KeyValue
              items={[
                { label: t("market.pair"), value: `${pair.base_asset}/${pair.quote_asset}` },
                { label: t("pcTrade.sideType"), value: `${t(`codes.${pending.side}`)} · ${t(`codes.${pending.type}`)}` },
                ...(pending.price ? [{ label: t("common.price"), value: `${formatPrice(pending.price, pair.price_decimals)} ${pair.quote_asset}` }] : []),
                ...(pending.quantity ? [{ label: t("common.amount"), value: `${formatAmount(pending.quantity, pair.qty_decimals)} ${pair.base_asset}` }] : []),
                ...(pending.price && pending.quantity
                  ? [{ label: t("common.total"), value: `${formatAmount(dec.mul(pending.price, pending.quantity), quoteDecimals)} ${pair.quote_asset}` }]
                  : []),
                ...(pending.quote_amount ? [{ label: t("common.total"), value: `${formatAmount(pending.quote_amount, quoteDecimals)} ${pair.quote_asset}` }] : []),
              ]}
            />
            <Checkbox checked={skipNext} onCheckedChange={(c) => setSkipNext(c === true)} label={t("pcTrade.dontAsk")} />
          </div>
        )}
      </Dialog>
    </div>
  );
}
