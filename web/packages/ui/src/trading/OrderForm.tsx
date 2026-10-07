import { dec, formatAmount, formatPercent } from "@exchange/core";
import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "../components/Button";
import { NumberInput, ratioPercent } from "../components/NumberInput";
import { Segmented } from "../components/Segmented";
import { Slider } from "../components/Slider";
import { Tabs } from "../components/Tabs";
import { cn } from "../lib/cn";
import {
  estimateFee,
  maxQuantity,
  percentOfAmount,
  percentOfQuantity,
  quantityForTotal,
  snapToStep,
  totalOf,
  usable,
  validateOrder,
  type Balances,
  type OrderErrors,
  type OrderFieldError,
  type OrderSide,
  type OrderType,
  type PairRules,
} from "./orderMath";

export type OrderFormValues = {
  side: OrderSide;
  type: OrderType;
  /** Limit orders. */
  price?: string;
  /** Limit orders and market sells. */
  quantity?: string;
  /** Market buys: how much quote to spend. */
  quoteAmount?: string;
};

export type OrderFormProps = {
  side: OrderSide;
  onSideChange: (side: OrderSide) => void;
  type: OrderType;
  onTypeChange: (type: OrderType) => void;
  pair: PairRules;
  /** The spot balances; null or undefined when signed out. */
  available?: Balances | null;
  lastPrice?: string | null;
  onSubmit: (order: OrderFormValues) => void;
  submitting?: boolean;
  signedIn: boolean;
  onSignIn?: () => void;
  onDeposit?: () => void;
  /** The deposit link's text (default "充值"; a margin account's "划转"). */
  depositLabel?: ReactNode;
  /** The balance line's label (default "可用"; with AUTO_BORROW "可用（含可借）"). */
  availableLabel?: ReactNode;
  /**
   * The account's own lines under the balance (a margin account's
   * borrowable amount and margin level, as Binance shows them, B120).
   */
  info?: ReactNode;
  /**
   * A price (and optionally a quantity) picked in the order book: pass a new
   * object on every pick and the form takes it.
   */
  fill?: { price?: string; quantity?: string } | null;
  /** Change it after a successful order to clear the amounts. */
  resetKey?: unknown;
  /** Asset precisions for balances and fees (default 8). */
  baseDecimals?: number;
  quoteDecimals?: number;
  /** Hide the buy/sell switch (the mobile sheet opens on one side). */
  hideSideSwitch?: boolean;
  /** The submit button's text (default "买入 BTC" / "卖出 BTC"). */
  submitLabel?: ReactNode;
  className?: string;
};

/**
 * OrderForm is the spot order panel (design §6.2): buy/sell, limit/market,
 * price and quantity snapped to the tick and lot sizes, total = price ×
 * quantity kept in sync both ways, a percent slider of the balance, the
 * estimated fee and the pair's limits. It only calculates and presents:
 * the page submits (onSubmit) and owns side, type and the balances.
 */
export function OrderForm({
  side, onSideChange, type, onTypeChange, pair, available, lastPrice, onSubmit, submitting, signedIn, onSignIn, onDeposit, depositLabel,
  availableLabel, info, fill, resetKey, baseDecimals = 8, quoteDecimals = 8, hideSideSwitch, submitLabel, className,
}: OrderFormProps) {
  const { t } = useTranslation();
  const [price, setPrice] = useState("");
  const [quantity, setQuantity] = useState("");
  const [total, setTotal] = useState("");
  const [attempted, setAttempted] = useState(false);
  const [dragPct, setDragPct] = useState<number | null>(null);
  const seeded = useRef(false);

  const priceDecimals = dec.decimalsOf(pair.tickSize);
  const qtyDecimals = dec.decimalsOf(pair.lotSize);
  const totalDecimals = Math.min(quoteDecimals, priceDecimals + qtyDecimals);
  const buy = side === "BUY";
  const limit = type === "limit";
  const byTotal = !limit && buy;
  const tone = buy ? "up" : "down";

  // A new pair starts empty and takes its last price once.
  const pairKey = `${pair.base}/${pair.quote}`;
  useEffect(() => {
    seeded.current = false;
    setPrice("");
    setQuantity("");
    setTotal("");
    setAttempted(false);
  }, [pairKey]);

  useEffect(() => {
    if (seeded.current || !usable(lastPrice)) return;
    seeded.current = true;
    setPrice((p) => p || snapToStep(lastPrice, pair.tickSize));
  }, [lastPrice, pair.tickSize, pairKey]);

  // After a successful order the amounts clear (the price stays).
  const firstReset = useRef(true);
  useEffect(() => {
    if (firstReset.current) {
      firstReset.current = false;
      return;
    }
    setQuantity("");
    setTotal("");
    setAttempted(false);
  }, [resetKey]);

  // Order book picks: price, and with Shift the cumulative quantity.
  useEffect(() => {
    if (!fill) return;
    const p = fill.price && usable(fill.price) ? snapToStep(fill.price, pair.tickSize) : null;
    const q = fill.quantity && usable(fill.quantity) ? snapToStep(fill.quantity, pair.lotSize) : null;
    if (p) setPrice(p);
    if (q) setQuantity(q);
    const np = p ?? price;
    const nq = q ?? quantity;
    if (p || q) setTotal(totalOf(np, nq));
    // Only a new pick applies: price and quantity are read, not followed.
  }, [fill]);

  const changePrice = (p: string) => {
    setPrice(p);
    if (usable(quantity)) setTotal(totalOf(p, quantity));
    else if (usable(total)) setQuantity(quantityForTotal(total, p, pair.lotSize));
  };
  const changeQuantity = (q: string) => {
    setQuantity(q);
    setTotal(limit ? totalOf(price, q) : "");
  };
  const changeTotal = (v: string) => {
    setTotal(v);
    if (limit) setQuantity(quantityForTotal(v, price, pair.lotSize));
  };

  const max = byTotal ? (available?.quote ?? "") : maxQuantity(side, limit ? price : (lastPrice ?? ""), available, pair.lotSize);
  const pctFromValues = byTotal
    ? ratioPercent(total, available?.quote)
    : buy
      ? ratioPercent(limit ? totalOf(price, quantity) : "", available?.quote)
      : ratioPercent(quantity, available?.base);
  const pct = dragPct ?? pctFromValues;

  const slide = (p: number) => {
    setDragPct(p);
    if (!available) return;
    if (byTotal) {
      setTotal(percentOfAmount(available.quote, p, totalDecimals));
      return;
    }
    const q = percentOfQuantity(maxQuantity(side, limit ? price : (lastPrice ?? ""), available, pair.lotSize), p, pair.lotSize);
    setQuantity(q);
    setTotal(limit ? totalOf(price, q) : "");
  };

  const errors: OrderErrors = validateOrder({ side, type, rules: pair, price, quantity, total, available: signedIn ? available : null, lastPrice });
  const show = (field: keyof OrderErrors, value: string) => {
    const e = errors[field];
    if (!e) return undefined;
    // Required-field errors wait for a submit; the others show as one types.
    const required = e.key.endsWith("Required");
    return attempted || (!required && value !== "") ? message(e) : undefined;
  };
  const message = (e: OrderFieldError) =>
    t(`ui.order.${e.key}`, {
      value: e.values?.value ? formatAmount(e.values.value) : "",
      unit: e.values?.unit ?? "",
    });

  const fee = estimateFee({ side, type, rules: pair, price, quantity, quoteAmount: total, lastPrice, baseDecimals, quoteDecimals });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!signedIn) {
      onSignIn?.();
      return;
    }
    setAttempted(true);
    if (Object.keys(errors).length > 0 || submitting) return;
    if (byTotal) onSubmit({ side, type, quoteAmount: total });
    else if (!limit) onSubmit({ side, type, quantity });
    else onSubmit({ side, type, price, quantity });
  };

  const availAsset = buy ? pair.quote : pair.base;
  const availValue = buy ? available?.quote : available?.base;
  // A market order has one amount, the slider under it as Binance has it
  // (B157): a buy spends a total of the quote, a sell sells a quantity of
  // the base. A limit order has both, the total under the slider.
  const totalInput = (
    <NumberInput
      aria-label={t("common.total")}
      prefix={<span className="text-xs">{t("common.total")}</span>}
      unit={pair.quote}
      align="right"
      value={total}
      onValueChange={changeTotal}
      decimals={totalDecimals}
      error={show("total", total)}
    />
  );

  return (
    <form noValidate onSubmit={submit} className={cn("flex flex-col gap-3", className)}>
      {!hideSideSwitch && (
        <Segmented
          block
          square
          size="md"
          value={side}
          onValueChange={(v) => onSideChange(v as OrderSide)}
          aria-label={`${t("common.buy")} / ${t("common.sell")}`}
          items={[
            { value: "BUY", label: t("common.buy"), thumbClassName: "bg-up", activeClassName: "text-black" },
            { value: "SELL", label: t("common.sell"), thumbClassName: "bg-down", activeClassName: "text-black" },
          ]}
        />
      )}
      <Tabs
        size="sm"
        value={type}
        onValueChange={(v) => onTypeChange(v as OrderType)}
        items={[
          { value: "limit", label: t("codes.LIMIT") },
          { value: "market", label: t("codes.MARKET") },
        ]}
      />
      {limit ? (
        <NumberInput
          aria-label={t("common.price")}
          prefix={<span className="text-xs">{t("common.price")}</span>}
          unit={pair.quote}
          align="right"
          value={price}
          onValueChange={changePrice}
          decimals={priceDecimals}
          step={pair.tickSize}
          snap
          error={show("price", price)}
        />
      ) : (
        <NumberInput
          aria-label={t("common.price")}
          prefix={<span className="text-xs">{t("common.price")}</span>}
          unit={pair.quote}
          align="right"
          value=""
          onValueChange={() => {}}
          placeholder={t("ui.order.marketPrice")}
          disabled
        />
      )}
      {byTotal ? (
        totalInput
      ) : (
        <NumberInput
          aria-label={t("common.amount")}
          prefix={<span className="text-xs">{t("common.amount")}</span>}
          unit={pair.base}
          align="right"
          value={quantity}
          onValueChange={changeQuantity}
          decimals={qtyDecimals}
          step={pair.lotSize}
          snap
          onBlur={() => limit && setTotal(totalOf(price, snapToStep(quantity, pair.lotSize)))}
          error={show("quantity", quantity)}
        />
      )}
      <Slider
        className="px-1"
        value={pct}
        onValueChange={slide}
        onValueCommit={() => setDragPct(null)}
        marks={[0, 25, 50, 75, 100]}
        markLabels
        formatMark={(m) => `${m}%`}
        formatValue={(v) => `${Math.round(v)}%`}
        tone={tone}
        disabled={!signedIn || !usable(max)}
        aria-label={`${t("common.available")} %`}
      />
      {limit && totalInput}
      <div className="flex flex-col gap-1.5 text-xs">
        <div className="flex items-center justify-between gap-2">
          <span className="text-fg-3">{availableLabel ?? t("common.available")}</span>
          <span className="flex items-center gap-2 tabular-nums text-fg-1">
            {signedIn && availValue !== undefined ? formatAmount(availValue, buy ? quoteDecimals : baseDecimals) : "—"} {availAsset}
            {signedIn && onDeposit && (
              <button type="button" onClick={onDeposit} className="text-brand hover:brightness-110">
                {depositLabel ?? t("ui.order.deposit")}
              </button>
            )}
          </span>
        </div>
        {info}
        {!byTotal && (
          <div className="flex items-center justify-between gap-2">
            <span className="text-fg-3">{buy ? t("ui.order.maxBuy") : t("ui.order.maxSell")}</span>
            <span className="tabular-nums text-fg-1">
              {signedIn && usable(max) ? formatAmount(max, qtyDecimals) : "—"} {pair.base}
            </span>
          </div>
        )}
        <div className="flex items-center justify-between gap-2">
          <span className="text-fg-3">
            {t("ui.order.estFee")}
            {fee && <span className="ml-1">({formatPercent(fee.rate, 2, false)})</span>}
          </span>
          <span className="tabular-nums text-fg-1">{fee ? `≈ ${formatAmount(fee.amount)} ${fee.asset}` : "—"}</span>
        </div>
      </div>
      {signedIn ? (
        <Button type="submit" size="lg" block variant={buy ? "buy" : "sell"} loading={submitting}>
          {submitLabel ?? (buy ? t("ui.order.buyBase", { base: pair.base }) : t("ui.order.sellBase", { base: pair.base }))}
        </Button>
      ) : (
        <Button type="submit" size="lg" block variant="primary">
          {t("state.signInToTrade")}
        </Button>
      )}
    </form>
  );
}
