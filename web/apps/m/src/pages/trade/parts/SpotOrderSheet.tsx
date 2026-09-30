import {
  accountApi, ApiError, applyOrderToCaches, assetDecimals, dec, errorText, formatAmount, formatPrice, newIdempotencyKey, placeOrder, qk, routes,
  selectSignedIn, tradable, unwrap, useAssets, useSession, useSettings, useTicker, type NewOrder, type Pair,
} from "@exchange/core";
import { Button, KeyValue, OrderForm, Sheet, toast, type OrderFormValues, type OrderSide, type OrderType, type PairRules } from "@exchange/ui";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";

type Balance = { account_type: string; asset: string; available: string };

function spotAvailable(list: Balance[] | undefined, asset: string): string {
  return list?.find((b) => b.account_type === "SPOT" && b.asset === asset)?.available ?? "0";
}

function toNewOrder(symbol: string, v: OrderFormValues): NewOrder {
  if (v.type === "limit") return { symbol, side: v.side, type: "LIMIT", price: v.price, quantity: v.quantity, time_in_force: "GTC" };
  if (v.side === "BUY") return { symbol, side: "BUY", type: "MARKET", quote_amount: v.quoteAmount };
  return { symbol, side: "SELL", type: "MARKET", quantity: v.quantity };
}

/**
 * SpotOrderSheet (design §7.2): the order form in a bottom sheet opened on
 * one side, with the book's picked price, the balances and, unless turned
 * off in settings, a confirmation step inside the same sheet.
 */
export function SpotOrderSheet({
  pair, side, open, onOpenChange, fill, onPlaced,
}: {
  pair: Pair;
  side: OrderSide;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  fill: { price?: string; quantity?: string } | null;
  onPlaced: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const signedIn = useSession(selectSignedIn);
  const confirmOrders = useSettings((s) => s.confirmOrders);
  const [type, setType] = useState<OrderType>("limit");
  const [pending, setPending] = useState<NewOrder | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [resetKey, setResetKey] = useState(0);
  const tk = useTicker(pair.symbol);
  const assets = useAssets();
  const balances = useQuery({ queryKey: qk.balances, queryFn: () => unwrap(accountApi.GET("/v1/account/balances")), enabled: signedIn, staleTime: 60_000 });
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
      applyOrderToCaches(qc, placed);
      void qc.invalidateQueries({ queryKey: qk.balances });
      setResetKey((k) => k + 1);
      setPending(null);
      onOpenChange(false);
      toast.success(t("mTrade.placed"));
      onPlaced();
    } catch (e) {
      const short = e instanceof ApiError && e.code === "LEDGER_INSUFFICIENT_BALANCE";
      toast.error(errorText(e), short ? { action: { label: t("nav.deposit"), onClick: () => navigate(routes.deposit) } } : undefined);
    } finally {
      setSubmitting(false);
    }
  };

  const title = `${side === "BUY" ? t("common.buy") : t("common.sell")} ${pair.base_asset}`;
  return (
    <Sheet open={open} onOpenChange={(o) => (o ? onOpenChange(true) : (setPending(null), onOpenChange(false)))} title={title}>
      {pending ? (
        <div className="flex flex-col gap-4 pb-2">
          <KeyValue
            items={[
              { label: t("market.pair"), value: `${pair.base_asset}/${pair.quote_asset}` },
              { label: t("mTrade.sideType"), value: `${t(`codes.${pending.side}`)} · ${t(`codes.${pending.type}`)}` },
              ...(pending.price ? [{ label: t("common.price"), value: `${formatPrice(pending.price, pair.price_decimals)} ${pair.quote_asset}` }] : []),
              ...(pending.quantity ? [{ label: t("common.amount"), value: `${formatAmount(pending.quantity, pair.qty_decimals)} ${pair.base_asset}` }] : []),
              ...(pending.price && pending.quantity
                ? [{ label: t("common.total"), value: `${formatAmount(dec.mul(pending.price, pending.quantity), quoteDecimals)} ${pair.quote_asset}` }]
                : []),
              ...(pending.quote_amount ? [{ label: t("common.total"), value: `${formatAmount(pending.quote_amount, quoteDecimals)} ${pair.quote_asset}` }] : []),
            ]}
          />
          <div className="grid grid-cols-2 gap-2">
            <Button size="lg" variant="secondary" onClick={() => setPending(null)}>
              {t("common.back")}
            </Button>
            <Button size="lg" variant={pending.side === "SELL" ? "sell" : "buy"} loading={submitting} onClick={() => void send(pending)}>
              {t("common.confirm")}
            </Button>
          </div>
        </div>
      ) : (
        <OrderForm
          side={side}
          onSideChange={() => {}}
          hideSideSwitch
          type={type}
          onTypeChange={setType}
          pair={rules}
          available={available}
          lastPrice={tk?.last}
          onSubmit={(v) => {
            const order = toNewOrder(pair.symbol, v);
            if (confirmOrders) setPending(order);
            else void send(order);
          }}
          submitting={submitting || !tradable(pair.status)}
          signedIn={signedIn}
          onSignIn={() => navigate(`${routes.login}?next=${encodeURIComponent(routes.trade(pair.symbol))}`)}
          onDeposit={() => navigate(routes.deposit)}
          fill={fill}
          resetKey={resetKey}
          baseDecimals={baseDecimals}
          quoteDecimals={quoteDecimals}
          className="px-0"
        />
      )}
    </Sheet>
  );
}
