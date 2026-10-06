import {
  accountApi, ApiError, applyOrderToCaches, assetDecimals, dec, errorText, formatAmount, formatPrice, newIdempotencyKey, placeOrder, qk, routes,
  selectSignedIn, tradable, unwrap, useAssets, useSession, useSettings, useTerminalPrefs, useTicker, type NewOrder, type Pair,
} from "@exchange/core";
import type { MarginActionKind } from "@exchange/core/margin/form";
import { preloadable, useIdleImport } from "@exchange/core/idle";
import { useMaxBorrowable } from "@exchange/core/margin/hooks";
import {
  afterMarginOrder, freezeAsset, tradeAccountFor, useMarginSupport, useMarginTrade, type SideEffect, type TradeAccount,
} from "@exchange/core/margin/trade";
import {
  Badge, Button, KeyValue, MarginLevel, OrderForm, Segmented, Select, Sheet, Skeleton, toast, type OrderFormValues, type OrderSide, type OrderType,
  type PairRules,
} from "@exchange/ui";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Suspense, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { TextButton } from "../../assets/parts/bits";

type Balance = { account_type: string; asset: string; available: string };

// The margin sheets come with the assets pages' forms, not with the
// terminal (review CS ①): the sheet loads once the order sheet is idle on
// a margin account and opens at once after (B108, B114).
const MarginSheet = preloadable(() => import("../../assets/parts/MarginSheet"), (m) => m.MarginSheet);

function spotAvailable(list: Balance[] | undefined, asset: string): string {
  return list?.find((b) => b.account_type === "SPOT" && b.asset === asset)?.available ?? "0";
}

function toNewOrder(symbol: string, v: OrderFormValues, account: TradeAccount, effect: SideEffect): NewOrder {
  const margin = account === "SPOT" ? {} : { account, side_effect: effect };
  if (v.type === "limit") return { symbol, side: v.side, type: "LIMIT", price: v.price, quantity: v.quantity, time_in_force: "GTC", ...margin };
  if (v.side === "BUY") return { symbol, side: "BUY", type: "MARKET", quote_amount: v.quoteAmount, ...margin };
  return { symbol, side: "SELL", type: "MARKET", quantity: v.quantity, ...margin };
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
  const chosen = useTerminalPrefs((s) => s.tradeAccount);
  const effect = useTerminalPrefs((s) => s.sideEffect);
  const setPrefs = useTerminalPrefs((s) => s.set);
  const { open: marginOpen, support, lends } = useMarginSupport(pair);
  const account = signedIn ? tradeAccountFor(chosen, marginOpen, support) : "SPOT";
  const margin = useMarginTrade(pair, account, effect);
  useIdleImport(MarginSheet.preload, account !== "SPOT");
  const spends = freezeAsset(pair, side);
  // Only an asset the platform lends is asked for (another answers 422).
  const borrowable = useMaxBorrowable(
    account === "SPOT" ? "MARGIN_CROSS" : account, account === "MARGIN_ISOLATED" ? pair.symbol : "", spends, account !== "SPOT" && lends.has(spends),
  );
  // The margin sheet open, and its coin: what the side spends.
  const [act, setActState] = useState<{ kind: MarginActionKind; asset: string } | null>(null);
  const setAct = (kind: MarginActionKind) => setActState({ kind, asset: spends });
  const baseDecimals = assetDecimals(assets.data?.assets, pair.base_asset);
  const quoteDecimals = assetDecimals(assets.data?.assets, pair.quote_asset);
  const rules = useMemo<PairRules>(
    () => ({
      base: pair.base_asset, quote: pair.quote_asset, tickSize: pair.tick_size, lotSize: pair.lot_size, minQuantity: pair.min_quantity,
      maxQuantity: pair.max_quantity, minNotional: pair.min_notional, makerFeeRate: pair.maker_fee_rate, takerFeeRate: pair.taker_fee_rate,
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
      applyOrderToCaches(qc, placed);
      void qc.invalidateQueries({ queryKey: qk.balances });
      afterMarginOrder(qc, order.account);
      setResetKey((k) => k + 1);
      setPending(null);
      onOpenChange(false);
      toast.success(t("mTrade.placed"));
      onPlaced();
    } catch (e) {
      afterMarginOrder(qc, order.account, e);
      const short = e instanceof ApiError && e.code === "LEDGER_INSUFFICIENT_BALANCE";
      const fix =
        order.account && order.account !== "SPOT"
          ? { label: t("mTrade.margin.transfer"), onClick: () => setAct("transfer") }
          : { label: t("nav.deposit"), onClick: () => navigate(routes.deposit) };
      toast.error(errorText(e), short ? { action: fix } : undefined);
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
              ...(pending.account && pending.account !== "SPOT"
                ? [{
                    label: t("mTrade.margin.accountLabel"),
                    value: `${t(pending.account === "MARGIN_CROSS" ? "mTrade.margin.cross" : "mTrade.margin.isolated")} · ${t(`mTrade.margin.effects.${pending.side_effect ?? "NONE"}`)}`,
                  }]
                : []),
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
        <>
          {signedIn && marginOpen && (support.cross || support.isolated) && (
            <div className="mb-3 flex flex-col gap-2" data-testid="margin-bar">
              <Segmented
                block
                size="md"
                value={account}
                onValueChange={(v) => setPrefs({ tradeAccount: v as TradeAccount })}
                aria-label={t("mTrade.margin.account")}
                items={[
                  { value: "SPOT", label: t("mTrade.margin.spot") },
                  ...(support.cross ? [{ value: "MARGIN_CROSS", label: t("mTrade.margin.cross") }] : []),
                  ...(support.isolated ? [{ value: "MARGIN_ISOLATED", label: t("mTrade.margin.isolated") }] : []),
                ]}
              />
              {account !== "SPOT" && margin.terms && (
                <>
                  <div className="flex items-center gap-2">
                    <Badge tone="brand" size="sm">
                      {margin.owner?.leverage ?? margin.terms.leverage}x
                    </Badge>
                    {margin.pending ? (
                      <Skeleton className="h-5 flex-1" />
                    ) : (
                      <MarginLevel
                        level={margin.owner?.margin_level ?? null}
                        warn={margin.owner?.warn_level ?? margin.terms.warn_level}
                        liquidation={margin.owner?.liquidation_level ?? margin.terms.liquidation_level}
                        size="sm"
                        compact
                        className="flex-1"
                      />
                    )}
                  </div>
                  <div className="flex items-center justify-between text-xs">
                    <span className="text-fg-3">{t("mTrade.margin.borrowable")}</span>
                    <span className="tabular-nums text-fg-1" data-testid="margin-borrowable">
                      {borrowable.data ? formatAmount(borrowable.data.amount, side === "SELL" ? baseDecimals : quoteDecimals) : "—"} {spends}
                    </span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="-ml-3 flex items-center">
                      <TextButton onClick={() => setAct("transfer")}>{t("mTrade.margin.transfer")}</TextButton>
                      <TextButton onClick={() => setAct("borrow")}>{t("mTrade.margin.borrow")}</TextButton>
                      <TextButton onClick={() => setAct("repay")}>{t("mTrade.margin.repay")}</TextButton>
                    </span>
                    <Select
                      size="lg"
                      value={effect}
                      onValueChange={(v) => setPrefs({ sideEffect: v as SideEffect })}
                      aria-label={t("mTrade.margin.effect")}
                      options={(["NONE", "AUTO_BORROW", "AUTO_REPAY"] as const).map((e) => ({ value: e, label: t(`mTrade.margin.effects.${e}`) }))}
                    />
                  </div>
                </>
              )}
            </div>
          )}
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
              const order = toNewOrder(pair.symbol, v, account, effect);
              if (confirmOrders) setPending(order);
              else void send(order);
            }}
            submitting={submitting || !tradable(pair.status)}
            signedIn={signedIn}
            onSignIn={() => navigate(`${routes.login}?next=${encodeURIComponent(routes.trade(pair.symbol))}`)}
            onDeposit={account === "SPOT" ? () => navigate(routes.deposit) : () => setAct("transfer")}
            depositLabel={account === "SPOT" ? undefined : t("mTrade.margin.transfer")}
            availableLabel={account !== "SPOT" && effect === "AUTO_BORROW" ? t("mTrade.margin.withBorrow") : undefined}
            fill={fill}
            resetKey={resetKey}
            baseDecimals={baseDecimals}
            quoteDecimals={quoteDecimals}
            className="px-0"
          />
        </>
      )}
      {act && (
        <Suspense fallback={null}>
          <MarginSheet
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
    </Sheet>
  );
}
