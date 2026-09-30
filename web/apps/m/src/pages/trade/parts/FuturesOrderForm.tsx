import {
  ApiError, closeableQuantity, dec, dk, errorText, formatAmount, formatPrice, maxOpenQuantity, newIdempotencyKey, openCost, placeContractOrder,
  routes, selectSignedIn, updateContractSettings, useContractSettings, useFuturesAccount, useMarkPrice, usePositions, useSession, useSettings,
  useTicker, type Contract, type NewContractOrder,
} from "@exchange/core";
import { Button, Checkbox, Dialog, KeyValue, LeverageDialog, NumberInput, Segmented, Slider, Tabs, toast, cn } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowRightLeft } from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation, useNavigate } from "react-router";

type Action = { side: "BUY" | "SELL"; positionSide: "BOTH" | "LONG" | "SHORT"; reduceOnly: boolean; label: string };

/**
 * FuturesOrderForm (design §7.2, in the order sheet): the margin mode and
 * leverage, then open or close with a limit or market order. Opening shows each side's cost
 * (margin plus fee) and the most the margin opens; closing takes at most
 * the position. One-way mode closes with reduce-only orders, hedge mode
 * by position side.
 */
export function FuturesOrderForm({
  contract, fill, onPlaced, className,
}: {
  contract: Contract;
  fill: { price?: string; quantity?: string } | null;
  /** Called after an order is accepted (the sheet closes). */
  onPlaced?: () => void;
  className?: string;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const signedIn = useSession(selectSignedIn);
  const confirmOrders = useSettings((s) => s.confirmOrders);
  const setSettings = useSettings((s) => s.set);
  const symbol = contract.symbol;
  const priceDecimals = dec.decimalsOf(contract.tick_size);
  const qtyDecimals = dec.decimalsOf(contract.lot_size);
  const settings = useContractSettings(symbol);
  const account = useFuturesAccount();
  const positions = usePositions(symbol);
  const mark = useMarkPrice(symbol).data;
  const tk = useTicker(symbol);

  const [tab, setTab] = useState<"open" | "close">("open");
  const [type, setType] = useState<"limit" | "market">("limit");
  const [price, setPrice] = useState("");
  const [quantity, setQuantity] = useState("");
  const [pct, setPct] = useState(0);
  const [reduceOnly, setReduceOnly] = useState(false);
  const [leverageOpen, setLeverageOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [pending, setPending] = useState<NewContractOrder | null>(null);
  const [skipNext, setSkipNext] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const leverage = settings.data?.leverage ?? Math.min(20, contract.max_leverage);
  const marginMode = settings.data?.margin_mode ?? "CROSS";
  const hedge = settings.data?.position_mode === "HEDGE";
  const available = account.data?.available ?? "0";
  const refPrice = type === "limit" ? price : (mark?.mark_price ?? tk?.last ?? "");

  // The book's click fills the price; a first visit takes the last price.
  useEffect(() => {
    if (fill?.price) setPrice(fill.price);
    if (fill?.quantity) setQuantity(dec.normalize(dec.quantize(fill.quantity, contract.lot_size, "down")));
  }, [fill, contract.lot_size]);
  useEffect(() => {
    setPrice((p) => p || (tk?.last ? dec.normalize(dec.quantize(tk.last, contract.tick_size, "down")) : ""));
  }, [tk?.last, contract.tick_size]);
  useEffect(() => {
    setPrice("");
    setQuantity("");
    setPct(0);
  }, [symbol]);

  const list = positions.data?.positions ?? [];
  const oneWay = list.find((p) => p.position_side === "BOTH");
  const longPos = hedge ? list.find((p) => p.position_side === "LONG") : oneWay && dec.sign(oneWay.quantity) > 0 ? oneWay : undefined;
  const shortPos = hedge ? list.find((p) => p.position_side === "SHORT") : oneWay && dec.sign(oneWay.quantity) < 0 ? oneWay : undefined;

  const maxOpen = useMemo(
    () => (dec.isDecimal(refPrice || "x") ? maxOpenQuantity(available, refPrice, leverage, contract.taker_fee_rate, contract.lot_size) : "0"),
    [available, refPrice, leverage, contract.taker_fee_rate, contract.lot_size],
  );
  const maxClose = (side: "BUY" | "SELL") => closeableQuantity((side === "SELL" ? longPos : shortPos)?.quantity);
  const cost = quantity && refPrice ? openCost(refPrice, quantity, leverage, contract.taker_fee_rate) : "0";

  const actions: [Action, Action] =
    tab === "open"
      ? [
          { side: "BUY", positionSide: hedge ? "LONG" : "BOTH", reduceOnly: !hedge && reduceOnly, label: t("mTrade.openLong") },
          { side: "SELL", positionSide: hedge ? "SHORT" : "BOTH", reduceOnly: !hedge && reduceOnly, label: t("mTrade.openShort") },
        ]
      : [
          { side: "BUY", positionSide: hedge ? "SHORT" : "BOTH", reduceOnly: !hedge, label: t("mTrade.closeShort") },
          { side: "SELL", positionSide: hedge ? "LONG" : "BOTH", reduceOnly: !hedge, label: t("mTrade.closeLong") },
        ];

  const setPercent = (p: number) => {
    setPct(p);
    const base = tab === "open" ? maxOpen : dec.max(maxClose("BUY"), maxClose("SELL"));
    if (!dec.isDecimal(base) || dec.sign(base) <= 0 || p <= 0) return setQuantity("");
    const q = dec.quantize(dec.div(dec.mul(base, String(Math.round(p))), "100", qtyDecimals, "down"), contract.lot_size, "down");
    setQuantity(dec.sign(q) > 0 ? dec.normalize(q) : "");
  };

  const problem = (a: Action): string | null => {
    if (!dec.isDecimal(quantity || "x") || dec.sign(quantity) <= 0) return t("mTrade.needQuantity");
    if (type === "limit" && (!dec.isDecimal(price || "x") || dec.sign(price) <= 0)) return t("mTrade.needPrice");
    if (dec.lt(quantity, contract.min_quantity)) return t("mTrade.minQuantity", { value: contract.min_quantity });
    if (dec.gt(quantity, contract.max_quantity)) return t("mTrade.maxQuantity", { value: contract.max_quantity });
    if (refPrice && dec.isDecimal(refPrice) && dec.lt(dec.mul(refPrice, quantity), contract.min_notional))
      return t("mTrade.minNotional", { value: contract.min_notional });
    if (tab === "close" && dec.gt(quantity, maxClose(a.side))) return t("mTrade.overClose");
    return null;
  };

  const orderOf = (a: Action): NewContractOrder => ({
    symbol, side: a.side, position_side: a.positionSide, type: type === "limit" ? "LIMIT" : "MARKET",
    price: type === "limit" ? price : undefined, quantity, reduce_only: a.reduceOnly || undefined,
  });

  const send = async (order: NewContractOrder) => {
    setSubmitting(true);
    try {
      await placeContractOrder(order, newIdempotencyKey());
      toast.success(t("mTrade.placed"));
      setQuantity("");
      setPct(0);
      void qc.invalidateQueries({ queryKey: ["derivatives"] });
      onPlaced?.();
    } catch (e) {
      const short = e instanceof ApiError && (e.code === "DERIV_INSUFFICIENT_MARGIN" || e.code === "LEDGER_INSUFFICIENT_BALANCE");
      toast.error(errorText(e), short ? { action: { label: t("nav.transfer"), onClick: () => navigate(routes.transfer) } } : undefined);
    } finally {
      setSubmitting(false);
    }
  };

  const submit = (a: Action) => {
    const why = problem(a);
    if (why) return toast.error(why);
    const order = orderOf(a);
    if (confirmOrders) {
      setSkipNext(false);
      setPending(order);
    } else void send(order);
  };

  const changeSettings = async (patch: { margin_mode?: "CROSS" | "ISOLATED"; leverage?: number }) => {
    setSaving(true);
    try {
      const next = await updateContractSettings(symbol, patch);
      qc.setQueryData(dk.settings(symbol), next);
      setLeverageOpen(false);
      toast.success(t("mTrade.settingsSaved"));
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setSaving(false);
    }
  };

  if (!signedIn) {
    return (
      <div className={cn("flex flex-col items-center justify-center gap-3 bg-bg-1 p-6 text-center", className)}>
        <p className="text-sm text-fg-2">{t("state.signInToTrade")}</p>
        <Button asChild block>
          <Link to={`${routes.login}?next=${encodeURIComponent(location.pathname)}`}>{t("nav.login")}</Link>
        </Button>
        <Button asChild block variant="secondary">
          <Link to={routes.register}>{t("nav.register")}</Link>
        </Button>
      </div>
    );
  }

  return (
    <div className={cn("flex flex-col gap-3", className)}>
      <div className="flex items-center gap-2">
        <Segmented
          size="sm"
          value={marginMode}
          onValueChange={(m) => void changeSettings({ margin_mode: m as "CROSS" | "ISOLATED" })}
          items={[
            { value: "CROSS", label: t("codes.CROSS") },
            { value: "ISOLATED", label: t("codes.ISOLATED") },
          ]}
          disabled={saving}
          aria-label={t("mTrade.marginMode")}
        />
        <Button size="sm" variant="secondary" className="hit-area" onClick={() => setLeverageOpen(true)} aria-label={t("mTrade.leverage")}>
          {leverage}x
        </Button>
        {hedge && <span className="ml-auto text-xs text-fg-3">{t("codes.HEDGE")}</span>}
      </div>
      <Tabs
        value={tab}
        onValueChange={(v) => {
          setTab(v as "open" | "close");
          setQuantity("");
          setPct(0);
        }}
        variant="pill"
        size="sm"
        block
        items={[
          { value: "open", label: t("mTrade.open") },
          { value: "close", label: t("mTrade.close") },
        ]}
      />
      <Tabs
        value={type}
        onValueChange={(v) => setType(v as "limit" | "market")}
        size="sm"
        items={[
          { value: "limit", label: t("codes.LIMIT") },
          { value: "market", label: t("codes.MARKET") },
        ]}
      />
      {type === "limit" ? (
        <NumberInput
          size="lg"
          aria-label={t("common.price")}
          prefix={<span className="text-xs text-fg-3">{t("common.price")}</span>}
          unit={contract.quote_asset}
          value={price}
          onValueChange={setPrice}
          step={contract.tick_size}
          decimals={priceDecimals}
          align="right"
          snap
        />
      ) : (
        <div className="flex h-10 items-center justify-between rounded-2 bg-bg-2 px-3 text-sm text-fg-3">
          <span>{t("common.price")}</span>
          <span>{t("mTrade.marketPrice")}</span>
        </div>
      )}
      <NumberInput
        size="lg"
        aria-label={t("common.amount")}
        prefix={<span className="text-xs text-fg-3">{t("common.amount")}</span>}
        unit={contract.base_asset}
        value={quantity}
        onValueChange={(q) => {
          setQuantity(q);
          setPct(0);
        }}
        step={contract.lot_size}
        decimals={qtyDecimals}
        align="right"
        snap
      />
      <Slider
        value={pct}
        onValueChange={setPercent}
        min={0}
        max={100}
        step={1}
        marks={[0, 25, 50, 75, 100]}
        markLabels
        formatMark={(m) => `${m}%`}
        formatValue={(v) => `${Math.round(v)}%`}
        aria-label={t("mTrade.percent")}
        className="px-1"
      />
      {tab === "open" && !hedge && (
        <Checkbox checked={reduceOnly} onCheckedChange={setReduceOnly} label={t("mTrade.reduceOnly")} className="text-xs" />
      )}
      <div className="flex flex-col gap-1 text-xs">
        <Row label={t("common.available")}>
          <span className="flex items-center gap-1">
            {formatAmount(available, 2)} {contract.quote_asset}
            <Link to={routes.transfer} aria-label={t("nav.transfer")} className="text-brand">
              <ArrowRightLeft size={12} />
            </Link>
          </span>
        </Row>
        {tab === "open" ? (
          <>
            <Row label={t("mTrade.maxOpen")}>
              {formatAmount(maxOpen, qtyDecimals)} {contract.base_asset}
            </Row>
            <Row label={t("mTrade.cost")}>
              {formatAmount(cost, 2)} {contract.quote_asset}
            </Row>
          </>
        ) : (
          <>
            <Row label={t("mTrade.longPosition")}>{formatAmount(maxClose("SELL"), qtyDecimals)} {contract.base_asset}</Row>
            <Row label={t("mTrade.shortPosition")}>{formatAmount(maxClose("BUY"), qtyDecimals)} {contract.base_asset}</Row>
          </>
        )}
      </div>
      <div className="grid grid-cols-2 gap-2">
        {actions.map((a) => (
          <Button
            key={a.label}
            size="lg"
            variant={a.side === "BUY" ? "buy" : "sell"}
            loading={submitting}
            disabled={contract.status !== "TRADING" && contract.status !== "CANCEL_ONLY"}
            onClick={() => submit(a)}
          >
            {a.label}
          </Button>
        ))}
      </div>
      <LeverageDialog
        open={leverageOpen}
        onOpenChange={setLeverageOpen}
        value={leverage}
        max={contract.max_leverage}
        onConfirm={(l) => void changeSettings({ leverage: l })}
        submitting={saving}
        symbol={symbol}
        info={(l) => {
          const tier = contract.risk_tiers.find((r) => r.max_leverage >= l);
          return tier ? t("mTrade.tierInfo", { value: formatAmount(tier.max_notional, 0) }) : null;
        }}
      />
      <Dialog
        open={pending !== null}
        onOpenChange={(o) => !o && setPending(null)}
        title={t("mTrade.confirmTitle")}
        size="sm"
        onConfirm={() => {
          if (!pending) return;
          if (skipNext) setSettings({ confirmOrders: false });
          const order = pending;
          setPending(null);
          void send(order);
        }}
        confirmVariant={pending?.side === "SELL" ? "sell" : "buy"}
      >
        {pending && (
          <div className="flex flex-col gap-3">
            <KeyValue
              items={[
                { label: t("mTrade.contract"), value: `${contract.base_asset}${contract.quote_asset} ${t("m.perpetual")}` },
                { label: t("mTrade.sideType"), value: `${t(`codes.${pending.side}`)} · ${t(`codes.${pending.type}`)}${pending.reduce_only ? ` · ${t("mTrade.reduceOnly")}` : ""}` },
                { label: t("mTrade.marginAndLeverage"), value: `${t(`codes.${marginMode}`)} · ${leverage}x` },
                ...(pending.price ? [{ label: t("common.price"), value: `${formatPrice(pending.price, priceDecimals)} ${contract.quote_asset}` }] : []),
                { label: t("common.amount"), value: `${formatAmount(pending.quantity, qtyDecimals)} ${contract.base_asset}` },
              ]}
            />
            <Checkbox checked={skipNext} onCheckedChange={setSkipNext} label={t("mTrade.dontAsk")} />
          </div>
        )}
      </Dialog>
    </div>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between">
      <span className="text-fg-3">{label}</span>
      <span className="tabular-nums text-fg-1">{children}</span>
    </div>
  );
}
