import {
  ApiError, closeableQuantity, dec, dk, errorText, formatAmount, formatPrice, maxNotional, newIdempotencyKey, openLimit, placeContractOrder, routes,
  selectSignedIn, sideExposure, updateContractSettings, usdValue, useContractMath, useContractOpenOrders, useContractSettings, useFuturesAccount,
  useMarkPrice, usePositions, useSession, useSettings, useTicker, type Contract, type NewContractOrder,
} from "@exchange/core";
import { Button, Checkbox, Dialog, KeyValue, LeverageDialog, NumberInput, Segmented, Slider, Tabs, toast, cn } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowRightLeft } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation, useNavigate } from "react-router";
import { ORDER_FORM_ID } from "./EmptyList";

type Action = { side: "BUY" | "SELL"; positionSide: "BOTH" | "LONG" | "SHORT"; reduceOnly: boolean; label: string };

/**
 * FuturesOrderPanel (design §6.2): the margin mode and leverage, then open
 * or close with a limit or market order. Opening shows each side's cost
 * (margin plus fee) and the most the margin opens; closing takes at most
 * the position. One-way mode closes with reduce-only orders, hedge mode
 * by position side. A coin-margined contract (design 2026-10-06 §2.6)
 * takes whole contracts, shows what they are worth in its coin and in
 * USD, and reserves and reports in its coin's FUTURES account.
 */
export function FuturesOrderPanel({
  contract, fill, className,
}: {
  contract: Contract;
  fill: { price?: string; quantity?: string } | null;
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
  const math = useContractMath(contract);
  const account = useFuturesAccount(math.settle);
  const positions = usePositions(symbol);
  const openOrders = useContractOpenOrders(symbol);
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
  // A coin-margined contract's quantity is whole contracts.
  const unit = math.inverse ? t("pcTrade.contractsUnit") : contract.base_asset;
  const transferTo = `${routes.transfer}?asset=${math.settle}`;

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

  // What opening may add on each side: the margin's limit at the price the
  // order reserves at (a buy its price, a sell no lower than the mark) and
  // the leverage's risk limit at the mark price (§11.7), less what the side
  // holds and has on order.
  const riskMark = mark?.mark_price ?? refPrice;
  const orders = openOrders.data?.items ?? [];
  const exposure = (side: "BUY" | "SELL") => sideExposure(side, hedge ? (side === "BUY" ? "LONG" : "SHORT") : "BOTH", list, orders);
  const byMargin = (side: "BUY" | "SELL") => {
    const at = math.reservePrice(side, type, price, mark?.mark_price ?? tk?.last ?? "");
    return at ? math.maxOpen(available, at, leverage) : "0";
  };
  const maxOpenOf = (side: "BUY" | "SELL") =>
    dec.isDecimal(riskMark || "x") ? openLimit(byMargin(side), math.riskRoom(leverage, riskMark, exposure(side))) : byMargin(side);
  const maxOpen = { BUY: maxOpenOf("BUY"), SELL: maxOpenOf("SELL") };
  const maxClose = (side: "BUY" | "SELL") => closeableQuantity((side === "SELL" ? longPos : shortPos)?.quantity);
  const cost = quantity && refPrice ? math.openCost(refPrice, quantity, leverage) : "0";

  const actions: [Action, Action] =
    tab === "open"
      ? [
          { side: "BUY", positionSide: hedge ? "LONG" : "BOTH", reduceOnly: !hedge && reduceOnly, label: t("pcTrade.openLong") },
          { side: "SELL", positionSide: hedge ? "SHORT" : "BOTH", reduceOnly: !hedge && reduceOnly, label: t("pcTrade.openShort") },
        ]
      : [
          { side: "BUY", positionSide: hedge ? "SHORT" : "BOTH", reduceOnly: !hedge, label: t("pcTrade.closeShort") },
          { side: "SELL", positionSide: hedge ? "LONG" : "BOTH", reduceOnly: !hedge, label: t("pcTrade.closeLong") },
        ];

  const setPercent = (p: number) => {
    setPct(p);
    const base = tab === "open" ? dec.max(maxOpen.BUY, maxOpen.SELL) : dec.max(maxClose("BUY"), maxClose("SELL"));
    if (!dec.isDecimal(base) || dec.sign(base) <= 0 || p <= 0) return setQuantity("");
    const q = dec.quantize(dec.div(dec.mul(base, String(Math.round(p))), "100", qtyDecimals, "down"), contract.lot_size, "down");
    setQuantity(dec.sign(q) > 0 ? dec.normalize(q) : "");
  };

  const problem = (a: Action): string | null => {
    if (!dec.isDecimal(quantity || "x") || dec.sign(quantity) <= 0) return t("pcTrade.needQuantity");
    if (type === "limit" && (!dec.isDecimal(price || "x") || dec.sign(price) <= 0)) return t("pcTrade.needPrice");
    if (dec.lt(quantity, contract.min_quantity)) return t("pcTrade.minQuantity", { value: contract.min_quantity });
    if (dec.gt(quantity, contract.max_quantity)) return t("pcTrade.maxQuantity", { value: contract.max_quantity });
    if (refPrice && dec.isDecimal(refPrice) && dec.lt(math.orderNotional(quantity, refPrice), contract.min_notional))
      return t("pcTrade.minNotional", { value: contract.min_notional, asset: math.notionalUnit });
    if (tab === "close" && dec.gt(quantity, maxClose(a.side))) return t("pcTrade.overClose");
    if (tab === "open" && !a.reduceOnly && dec.isDecimal(riskMark || "x")) {
      const r = math.checkRisk(leverage, riskMark, exposure(a.side), quantity);
      if (!r.ok) {
        return t("pcTrade.overRisk", {
          leverage, cap: formatAmount(r.cap, math.inverse ? undefined : 0), notional: formatAmount(r.notional, math.amountDecimals), asset: math.settle,
        });
      }
    }
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
      toast.success(t("pcTrade.placed"));
      setQuantity("");
      setPct(0);
      void qc.invalidateQueries({ queryKey: ["derivatives"] });
    } catch (e) {
      const short = e instanceof ApiError && (e.code === "DERIV_INSUFFICIENT_MARGIN" || e.code === "LEDGER_INSUFFICIENT_BALANCE");
      toast.error(errorText(e), short ? { action: { label: t("nav.transfer"), onClick: () => navigate(transferTo) } } : undefined);
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
      toast.success(t("pcTrade.settingsSaved"));
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
    <div id={ORDER_FORM_ID} className={cn("flex flex-col gap-3 bg-bg-1 p-3", className)}>
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
          aria-label={t("pcTrade.marginMode")}
        />
        <Button size="sm" variant="secondary" onClick={() => setLeverageOpen(true)} aria-label={t("pcTrade.leverage")}>
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
          { value: "open", label: t("pcTrade.open") },
          { value: "close", label: t("pcTrade.close") },
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
          <span>{t("pcTrade.marketPrice")}</span>
        </div>
      )}
      <NumberInput
        aria-label={t("common.amount")}
        prefix={<span className="text-xs text-fg-3">{t("common.amount")}</span>}
        unit={unit}
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
      {math.inverse && (
        <p data-testid="contracts-value" className="-mt-1.5 text-right text-xs tabular-nums text-fg-3">
          ≈ {formatAmount(math.worth(quantity || "0", refPrice), math.amountDecimals)} {math.settle} · {formatAmount(usdValue(quantity || "0", contract.contract_size), 0)} USD
        </p>
      )}
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
        aria-label={t("pcTrade.percent")}
        className="px-1"
      />
      {tab === "open" && !hedge && (
        <Checkbox checked={reduceOnly} onCheckedChange={setReduceOnly} label={t("pcTrade.reduceOnly")} className="text-xs" />
      )}
      <div className="flex flex-col gap-1 text-xs">
        {/* The settlement asset's FUTURES account (design 2026-10-06 §2.6: a coin-margined contract's is its coin's). */}
        <Row label={t("pcTrade.walletBalance")}>
          {formatAmount(account.data?.wallet_balance ?? "0", math.amountDecimals)} {math.settle}
        </Row>
        <Row label={t("pcTrade.marginBalance")}>
          {formatAmount(account.data?.margin_balance ?? "0", math.amountDecimals)} {math.settle}
        </Row>
        <Row label={t("common.available")}>
          <span className="flex items-center gap-1">
            {formatAmount(available, math.amountDecimals)} {math.settle}
            <Link to={transferTo} aria-label={t("nav.transfer")} className="text-brand">
              <ArrowRightLeft size={12} />
            </Link>
          </span>
        </Row>
        {tab === "open" ? (
          <>
            <Row label={t("pcTrade.maxOpenLong")}>
              {formatAmount(maxOpen.BUY, qtyDecimals)} {unit}
            </Row>
            <Row label={t("pcTrade.maxOpenShort")}>
              {formatAmount(maxOpen.SELL, qtyDecimals)} {unit}
            </Row>
            <Row label={t("pcTrade.cost")}>
              {formatAmount(cost, math.amountDecimals)} {math.settle}
            </Row>
          </>
        ) : (
          <>
            <Row label={t("pcTrade.longPosition")}>{formatAmount(maxClose("SELL"), qtyDecimals)} {unit}</Row>
            <Row label={t("pcTrade.shortPosition")}>{formatAmount(maxClose("BUY"), qtyDecimals)} {unit}</Row>
          </>
        )}
      </div>
      <div className="grid grid-cols-2 gap-2">
        {actions.map((a) => (
          <Button
            key={a.label}
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
          const cap = maxNotional(contract.risk_tiers, l);
          if (dec.sign(cap) <= 0) return null;
          // A leverage whose cap a position already exceeds is refused (DERIV_RISK_LIMIT_EXCEEDED).
          const held = dec.isDecimal(riskMark || "x") ? list.reduce((m, p) => dec.max(m, math.worth(p.quantity, riskMark)), "0") : "0";
          return dec.gt(held, cap)
            ? t("pcTrade.tierOver", { held: formatAmount(held, math.amountDecimals), asset: math.settle })
            : t("pcTrade.tierInfo", { value: formatAmount(cap, math.inverse ? undefined : 0), asset: math.settle });
        }}
      />
      <Dialog
        open={pending !== null}
        onOpenChange={(o) => !o && setPending(null)}
        title={t("pcTrade.confirmTitle")}
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
                { label: t("pcTrade.contract"), value: `${contract.base_asset}${contract.quote_asset} ${t("pcTrade.perpetual")}` },
                { label: t("pcTrade.sideType"), value: `${t(`codes.${pending.side}`)} · ${t(`codes.${pending.type}`)}${pending.reduce_only ? ` · ${t("pcTrade.reduceOnly")}` : ""}` },
                { label: t("pcTrade.marginAndLeverage"), value: `${t(`codes.${marginMode}`)} · ${leverage}x` },
                ...(pending.price ? [{ label: t("common.price"), value: `${formatPrice(pending.price, priceDecimals)} ${contract.quote_asset}` }] : []),
                { label: t("common.amount"), value: `${formatAmount(pending.quantity, qtyDecimals)} ${unit}` },
                ...(math.inverse
                  ? [{ label: t("pcTrade.value"), value: `≈ ${formatAmount(usdValue(pending.quantity, contract.contract_size), 0)} USD` }]
                  : []),
              ]}
            />
            <Checkbox checked={skipNext} onCheckedChange={setSkipNext} label={t("pcTrade.dontAsk")} />
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
