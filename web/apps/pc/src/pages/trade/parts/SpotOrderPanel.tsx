import {
  accountApi, ApiError, applyOrderToCaches, assetDecimals, errorText, formatAmount, formatPrice, newIdempotencyKey, placeOrder, qk, routes, selectSignedIn, tradable,
  unwrap, useAssets, useSession, useSettings, useTerminalPrefs, useTicker, dec, type NewOrder, type Pair,
} from "@exchange/core";
import type { MarginActionKind } from "@exchange/core/margin/form";
import { useIdleImport } from "@exchange/core/idle";
import { useMaxBorrowable } from "@exchange/core/margin/hooks";
import {
  afterMarginOrder, freezeAsset, tradeAccountFor, useMarginSupport, useMarginTrade, type SideEffect, type TradeAccount,
} from "@exchange/core/margin/trade";
import { Checkbox, cn, Dialog, KeyValue, OrderForm, toast, type OrderFormValues, type OrderSide, type OrderType, type PairRules } from "@exchange/ui";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Suspense, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router";
import { MarginDialog } from "../../assets/parts/lazyMargin";
import { ORDER_FORM_ID } from "./EmptyList";
import { MarginBar, MarginInfo } from "./MarginBar";

type Balance = { account_type: string; asset: string; available: string };

/** useSpotBalances reads the balances the private sync keeps current. */
export function useSpotBalances() {
  const signedIn = useSession(selectSignedIn);
  return useQuery({ queryKey: qk.balances, queryFn: () => unwrap(accountApi.GET("/v1/account/balances")), enabled: signedIn, staleTime: 60_000 });
}

export function spotAvailable(list: Balance[] | undefined, asset: string): string {
  return list?.find((b) => b.account_type === "SPOT" && b.asset === asset)?.available ?? "0";
}

/** toNewOrder turns the form's values into the API's order, on a margin account when one is given. */
export function toNewOrder(symbol: string, v: OrderFormValues, account: TradeAccount = "SPOT", effect: SideEffect = "NONE"): NewOrder {
  const margin = account === "SPOT" ? {} : { account, side_effect: effect };
  if (v.type === "limit") return { symbol, side: v.side, type: "LIMIT", price: v.price, quantity: v.quantity, time_in_force: "GTC", ...margin };
  // A market buy by total spends it; one by quantity buys it (B157).
  if (v.side === "BUY") return { symbol, side: "BUY", type: "MARKET", ...(v.quoteAmount ? { quote_amount: v.quoteAmount } : { quantity: v.quantity }), ...margin };
  return { symbol, side: "SELL", type: "MARKET", quantity: v.quantity, ...margin };
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
  const qc = useQueryClient();
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
  const chosen = useTerminalPrefs((s) => s.tradeAccount);
  const effect = useTerminalPrefs((s) => s.sideEffect);
  const setPrefs = useTerminalPrefs((s) => s.set);
  const { open: marginOpen, support, lends } = useMarginSupport(pair);
  const account = signedIn ? tradeAccountFor(chosen, marginOpen, support) : "SPOT";
  const margin = useMarginTrade(pair, account, effect);
  // The margin dialogs come with the assets pages' forms, not with the
  // terminal (review CS ①): preloaded once it is idle on a margin account.
  useIdleImport(MarginDialog.preload, account !== "SPOT");
  const spends = freezeAsset(pair, side);
  // Only an asset the platform lends is asked for (another answers 422).
  const borrowable = useMaxBorrowable(
    account === "SPOT" ? "MARGIN_CROSS" : account, account === "MARGIN_ISOLATED" ? pair.symbol : "", spends, account !== "SPOT" && lends.has(spends),
  );
  // The margin dialog open, and its coin: what the side spends.
  const [act, setActState] = useState<{ kind: MarginActionKind; asset: string } | null>(null);
  const setAct = (kind: MarginActionKind) => setActState({ kind, asset: spends });
  const baseDecimals = assetDecimals(assets.data?.assets, pair.base_asset);
  const quoteDecimals = assetDecimals(assets.data?.assets, pair.quote_asset);

  const rules = useMemo<PairRules>(
    () => ({
      base: pair.base_asset, quote: pair.quote_asset, tickSize: pair.tick_size, lotSize: pair.lot_size, minQuantity: pair.min_quantity,
      maxQuantity: pair.max_quantity, minNotional: pair.min_notional, makerFeeRate: pair.maker_fee_rate, takerFeeRate: pair.taker_fee_rate,
      priceBand: pair.price_band,
    }),
    [pair],
  );
  const available =
    account !== "SPOT"
      ? margin.available
      : signedIn && balances.data
        ? { base: spotAvailable(balances.data.balances, pair.base_asset), quote: spotAvailable(balances.data.balances, pair.quote_asset) }
        : null;

  const send = async (order: NewOrder) => {
    setSubmitting(true);
    try {
      const placed = await placeOrder(order, newIdempotencyKey());
      // The pushes normally bring the order and the frozen balance; right
      // after a page load they may precede the private subscription.
      applyOrderToCaches(qc, placed);
      void qc.invalidateQueries({ queryKey: qk.balances });
      afterMarginOrder(qc, order.account);
      setResetKey((k) => k + 1);
      if (placed.status === "REJECTED") {
        toast.error(t("pcTrade.rejected"), { description: placed.reject_reason ? errorText(new ApiError(0, placed.reject_reason, "")) : undefined });
      } else {
        toast.success(t("pcTrade.placed"), { action: onPlaced ? { label: t("pcTrade.viewOrders"), onClick: onPlaced } : undefined });
        onPlaced?.();
      }
    } catch (e) {
      afterMarginOrder(qc, order.account, e);
      const short = e instanceof ApiError && e.code === "LEDGER_INSUFFICIENT_BALANCE";
      const fix =
        order.account && order.account !== "SPOT"
          ? { label: t("pcTrade.margin.transfer"), onClick: () => setAct("transfer") }
          : { label: t("nav.deposit"), onClick: () => navigate(routes.deposit) };
      toast.error(errorText(e), short ? { action: fix } : undefined);
    } finally {
      setSubmitting(false);
    }
  };

  const submit = (v: OrderFormValues) => {
    const order = toNewOrder(pair.symbol, v, account, effect);
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

  // The futures panel's padding in every account (B120).
  return (
    <div id={ORDER_FORM_ID} className={cn("flex flex-col gap-3 p-3", className)}>
      {signedIn && marginOpen && (support.cross || support.isolated) && (
        <MarginBar
          account={account}
          onAccount={(a) => setPrefs({ tradeAccount: a })}
          effect={effect}
          onEffect={(e) => setPrefs({ sideEffect: e })}
          trade={margin}
          onAct={setAct}
        />
      )}
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
        onDeposit={account === "SPOT" ? () => navigate(routes.deposit) : () => setAct("transfer")}
        depositLabel={account === "SPOT" ? undefined : t("pcTrade.margin.transfer")}
        availableLabel={account !== "SPOT" && effect === "AUTO_BORROW" ? t("pcTrade.margin.withBorrow") : undefined}
        info={
          <MarginInfo
            account={account}
            trade={margin}
            priceDecimals={pair.price_decimals}
            borrowable={{ asset: spends, amount: borrowable.data?.amount, decimals: side === "SELL" ? baseDecimals : quoteDecimals }}
          />
        }
        fill={fill}
        resetKey={resetKey}
        baseDecimals={baseDecimals}
        quoteDecimals={quoteDecimals}
      />
      {!tradable(pair.status) && <p className="text-xs text-warn">{t("pcTrade.notTrading")}</p>}
      {act && (
        <Suspense fallback={null}>
          <MarginDialog
            kind={act.kind}
            init={{
              account: account === "MARGIN_ISOLATED" ? "MARGIN_ISOLATED" : "MARGIN_CROSS",
              symbol: account === "MARGIN_ISOLATED" ? pair.symbol : "",
              asset: act.asset,
              direction: "IN",
            }}
            onClose={() => setActState(null)}
          />
        </Suspense>
      )}
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
                ...(pending.account && pending.account !== "SPOT"
                  ? [{
                      label: t("pcTrade.margin.accountLabel"),
                      value: `${t(pending.account === "MARGIN_CROSS" ? "pcTrade.margin.cross" : "pcTrade.margin.isolated")} · ${t(`pcTrade.margin.effects.${pending.side_effect ?? "NONE"}`)}`,
                    }]
                  : []),
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
