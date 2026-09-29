import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useParams } from "react-router";
import { derivativesApi, marketApi, unwrap } from "../api/client";
import type { components as DerivTypes } from "../api/gen/derivatives";
import type { components as MarketTypes } from "../api/gen/market";
import { Badge, Button, Card, ErrorText, Field, Notice } from "../components/ui";
import { codeText, errorText } from "../i18n";
import { checkAmount, format } from "../lib/decimal";
import { countdown, orderCost, pnlTone, ratePercent, signed } from "../lib/futures";
import { decimalsOf, marketSocket, percent } from "../lib/market";
import { useFuturesAccount } from "./assets";
import { changeTone, statusTone, useContracts, useMarkPrice, useNow } from "./markets";
import { CandleChart, OrderBook, RecentTrades } from "./trade";

type Contract = MarketTypes["schemas"]["Contract"];
type Ticker = MarketTypes["schemas"]["Ticker"];
type Position = DerivTypes["schemas"]["ContractPosition"];
type Settings = DerivTypes["schemas"]["ContractSettings"];

// The settlement asset of every contract, and its precision.
const QUOTE_DECIMALS = 6;

export function FuturesPage() {
  const { t } = useTranslation();
  const { symbol = "" } = useParams();
  const contracts = useContracts();
  const contract = contracts.data?.contracts.find((c) => c.symbol === symbol.toUpperCase());
  const [picked, setPicked] = useState({ price: "", at: 0 });
  if (contracts.isLoading) return <p className="p-8 text-center text-sm text-gray-500">{t("common.loading")}</p>;
  if (!contract) return <ErrorText text={contracts.error ? errorText(contracts.error) : t("errors.COMMON_NOT_FOUND")} />;
  return (
    <div className="space-y-3">
      <ContractBar contract={contract} />
      <div className="grid gap-3 lg:grid-cols-[1fr_300px]">
        <CandleChart symbol={contract.symbol} />
        <OrderBook symbol={contract.symbol} base={contract.base_asset} quote={contract.quote_asset} onPick={(price) => setPicked({ price, at: Date.now() })} />
      </div>
      <div className="grid gap-3 lg:grid-cols-[1fr_300px]">
        <OrderForm contract={contract} picked={picked} />
        <div className="space-y-3">
          <AccountCard />
          <RecentTrades symbol={contract.symbol} />
        </div>
      </div>
      <PositionsPanel contract={contract} />
      <OrdersPanel contract={contract} />
    </div>
  );
}

function ContractBar({ contract }: { contract: Contract }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const ticker = useQuery({
    queryKey: ["ticker", contract.symbol],
    queryFn: () => unwrap(marketApi.GET("/v1/market/{symbol}/ticker", { params: { path: { symbol: contract.symbol } } })),
  });
  useEffect(
    () => marketSocket.subscribe(`ticker:${contract.symbol}`, (m) => qc.setQueryData(["ticker", contract.symbol], m.data as Ticker)),
    [contract.symbol, qc],
  );
  const mark = useMarkPrice(contract.symbol);
  const now = useNow();
  const tk = ticker.data;
  const m = mark.data;
  const stat = (label: string, value: string, tone = "text-gray-200") => (
    <div>
      <div className="text-xs text-gray-500">{label}</div>
      <div className={`font-mono text-sm ${tone}`}>{value}</div>
    </div>
  );
  return (
    <Card>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
        <div>
          <Link to="/markets" className="text-lg font-semibold">
            {contract.base_asset}{contract.quote_asset}<span className="text-sm text-gray-500"> {t("futures.perpetual")}</span>
          </Link>{" "}
          <Badge tone={statusTone(contract.status)}>{codeText(contract.status)}</Badge>{" "}
          {m?.degraded && <Badge tone="red">{t("futures.reduceOnly")}</Badge>}
        </div>
        <div className={`font-mono text-xl ${changeTone(tk?.change)}`}>{tk?.last ? format(tk.last) : t("trade.noAnchor")}</div>
        {stat(t("futures.mark"), m?.mark_price ? format(m.mark_price) : "—")}
        {stat(t("futures.index"), m?.index_price ? format(m.index_price) : "—")}
        {stat(`${t("futures.funding")} / ${t("futures.countdown")}`, `${ratePercent(m?.funding_rate)} ${countdown(m?.next_funding_time, now)}`)}
        {stat(t("markets.change"), percent(tk?.change), changeTone(tk?.change))}
        {stat(`${t("markets.volume")} (${contract.base_asset})`, tk ? format(tk.volume) : "—")}
      </div>
    </Card>
  );
}

function AccountCard() {
  const { t } = useTranslation();
  const account = useFuturesAccount();
  const a = account.data;
  const row = (label: string, value: string | undefined, tone = "text-gray-200") => (
    <div className="flex justify-between">
      <span className="text-gray-400">{label}</span>
      <span className={`font-mono ${tone}`}>{value ? format(value) : "—"}</span>
    </div>
  );
  return (
    <Card title={t("futures.account")} actions={<Link to="/transfer" className="text-xs text-[#f0b90b] hover:underline">{t("nav.transfer")}</Link>}>
      <ErrorText text={account.error ? errorText(account.error) : ""} />
      <div className="space-y-1 text-sm" data-testid="futures-account">
        {row(t("futures.walletBalance"), a?.wallet_balance)}
        {row(t("trade.available"), a?.available)}
        {row(t("futures.marginBalance"), a?.margin_balance)}
        {row(t("futures.unrealized"), a?.unrealized_pnl, pnlTone(a?.unrealized_pnl))}
        {row(t("futures.positionMargin"), a?.position_margin)}
        {row(t("futures.orderMargin"), a?.order_margin)}
      </div>
    </Card>
  );
}

function useSettings(symbol: string) {
  return useQuery({
    queryKey: ["settings", symbol],
    queryFn: () => unwrap(derivativesApi.GET("/v1/derivatives/settings/{symbol}", { params: { path: { symbol } } })),
  });
}

function SettingsBar({ contract, settings }: { contract: Contract; settings: Settings }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [leverage, setLeverage] = useState(String(settings.leverage));
  useEffect(() => setLeverage(String(settings.leverage)), [settings.leverage]);
  const update = useMutation({
    mutationFn: (body: { position_mode?: "ONE_WAY" | "HEDGE"; margin_mode?: "CROSS" | "ISOLATED"; leverage?: number }) =>
      unwrap(derivativesApi.PUT("/v1/derivatives/settings/{symbol}", { params: { path: { symbol: contract.symbol } }, body })),
    onSuccess: (s) => {
      qc.setQueryData(["settings", contract.symbol], s);
      void qc.invalidateQueries({ queryKey: ["positions"] });
      void qc.invalidateQueries({ queryKey: ["futures-account"] });
    },
  });
  const pill = (active: boolean) => `rounded px-2 py-0.5 text-xs ${active ? "bg-white/15 text-white" : "text-gray-400 hover:text-white"}`;
  const lev = Number(leverage);
  const badLeverage = !Number.isInteger(lev) || lev < 1 || lev > contract.max_leverage;
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        {(["CROSS", "ISOLATED"] as const).map((m) => (
          <button key={m} type="button" className={pill(settings.margin_mode === m)} onClick={() => update.mutate({ margin_mode: m })}>{codeText(m)}</button>
        ))}
        <span className="text-gray-600">|</span>
        {(["ONE_WAY", "HEDGE"] as const).map((m) => (
          <button key={m} type="button" className={pill(settings.position_mode === m)} onClick={() => update.mutate({ position_mode: m })}>{codeText(m)}</button>
        ))}
        <span className="text-gray-600">|</span>
        <input
          aria-label={t("futures.leverage")}
          className="w-14 rounded border border-white/10 bg-[#0b0e11] px-2 py-0.5 text-right font-mono"
          value={leverage}
          onChange={(e) => setLeverage(e.target.value.replace(/\D/g, ""))}
        />
        <span className="text-gray-400">x / {contract.max_leverage}x</span>
        <button type="button" className="text-[#f0b90b] disabled:opacity-40" disabled={badLeverage || lev === settings.leverage} onClick={() => update.mutate({ leverage: lev })}>
          {t("futures.setLeverage")}
        </button>
      </div>
      <ErrorText text={update.error ? errorText(update.error) : ""} />
    </div>
  );
}

type Action = "OPEN_LONG" | "OPEN_SHORT" | "CLOSE_LONG" | "CLOSE_SHORT";

function OrderForm({ contract, picked }: { contract: Contract; picked: { price: string; at: number } }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const settings = useSettings(contract.symbol);
  const account = useFuturesAccount();
  const mark = useMarkPrice(contract.symbol);
  const [action, setAction] = useState<Action>("OPEN_LONG");
  const [type, setType] = useState<"LIMIT" | "MARKET">("LIMIT");
  const [price, setPrice] = useState("");
  const [qty, setQty] = useState("");
  const [reduceOnly, setReduceOnly] = useState(false);
  const [done, setDone] = useState("");
  useEffect(() => {
    if (picked.price) {
      setPrice(picked.price);
      setType("LIMIT");
    }
  }, [picked]);
  useEffect(() => {
    if (!done) return;
    const tm = setTimeout(() => setDone(""), 4000);
    return () => clearTimeout(tm);
  }, [done]);
  const s = settings.data;
  const hedge = s?.position_mode === "HEDGE";
  const buy = action === "OPEN_LONG" || action === "CLOSE_SHORT";
  const closing = hedge ? action.startsWith("CLOSE") : reduceOnly;
  const checks: [string, number][] = type === "LIMIT" ? [[price, decimalsOf(contract.tick_size)], [qty, decimalsOf(contract.lot_size)]] : [[qty, decimalsOf(contract.lot_size)]];
  const problem = checks.map(([v, n]) => (v ? checkAmount(v, n) : "empty")).find((r) => r !== "ok");
  const firstBad = checks.find(([v, n]) => v && checkAmount(v, n) !== "ok");
  const hint = firstBad ? t(`errors.${checkAmount(firstBad[0], firstBad[1])}`, { n: firstBad[1] }) : "";
  const at = type === "LIMIT" ? price : (mark.data?.mark_price ?? "");
  const cost = !problem && !closing && s && at ? orderCost(at, qty, s.leverage, contract.taker_fee_rate, QUOTE_DECIMALS) : "";
  const place = useMutation({
    mutationFn: () => {
      const body: Record<string, unknown> = { symbol: contract.symbol, side: buy ? "BUY" : "SELL", type, quantity: qty };
      if (type === "LIMIT") body.price = price;
      if (hedge) body.position_side = action.endsWith("LONG") ? "LONG" : "SHORT";
      else if (reduceOnly) body.reduce_only = true;
      return unwrap(
        derivativesApi.POST("/v1/derivatives/orders", {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          body: body as any,
          headers: { "Idempotency-Key": crypto.randomUUID() },
        }),
      );
    },
    onSuccess: () => {
      setDone(t("trade.placed"));
      setQty("");
      void qc.invalidateQueries({ queryKey: ["orders"] });
      void qc.invalidateQueries({ queryKey: ["futures-account"] });
    },
  });
  const tab = (active: boolean, tone: string) => `flex-1 rounded-md py-1.5 text-sm font-medium ${active ? tone : "text-gray-400 hover:text-white"}`;
  const trading = contract.status === "TRADING";
  const actions: [Action, string, string][] = hedge
    ? [
        ["OPEN_LONG", t("futures.openLong"), "bg-emerald-500/20 text-emerald-300"],
        ["OPEN_SHORT", t("futures.openShort"), "bg-red-500/20 text-red-300"],
        ["CLOSE_LONG", t("futures.closeLong"), "bg-red-500/20 text-red-300"],
        ["CLOSE_SHORT", t("futures.closeShort"), "bg-emerald-500/20 text-emerald-300"],
      ]
    : [
        ["OPEN_LONG", t("futures.buyLong"), "bg-emerald-500/20 text-emerald-300"],
        ["OPEN_SHORT", t("futures.sellShort"), "bg-red-500/20 text-red-300"],
      ];
  return (
    <Card>
      {s && <SettingsBar contract={contract} settings={s} />}
      <form
        className="mt-3 space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          setDone("");
          if (!problem) place.mutate();
        }}
      >
        <div className="flex gap-1 rounded-lg bg-black/30 p-1">
          {actions.map(([k, label, tone]) => (
            <button key={k} type="button" className={tab(action === k, tone)} onClick={() => setAction(k)}>{label}</button>
          ))}
        </div>
        <div className="flex items-center gap-4 text-sm">
          {(["LIMIT", "MARKET"] as const).map((k) => (
            <button key={k} type="button" onClick={() => setType(k)} className={type === k ? "text-[#f0b90b]" : "text-gray-400 hover:text-white"}>
              {k === "LIMIT" ? t("trade.limit") : t("trade.market")}
            </button>
          ))}
          {!hedge && (
            <label className="ml-auto flex items-center gap-1 text-xs text-gray-400">
              <input type="checkbox" checked={reduceOnly} onChange={(e) => setReduceOnly(e.target.checked)} /> {t("futures.reduceOnlyOrder")}
            </label>
          )}
        </div>
        {type === "LIMIT" && (
          <Field label={`${t("trade.price")} (${contract.quote_asset})`} inputMode="decimal" name="price" value={price} onChange={(e) => setPrice(e.target.value)} />
        )}
        <Field
          label={`${t("trade.quantity")} (${contract.base_asset})`}
          inputMode="decimal"
          name="quantity"
          value={qty}
          onChange={(e) => setQty(e.target.value)}
          hint={type === "MARKET" ? t("futures.marketHint") : undefined}
        />
        <div className="flex justify-between text-xs text-gray-400">
          <span>{t("trade.available")}: <span className="font-mono text-gray-200">{account.data ? format(account.data.available) : "—"} {contract.quote_asset}</span></span>
          {cost && <span>{t("futures.cost")}: <span className="font-mono text-gray-200">{format(cost)} {contract.quote_asset}</span></span>}
        </div>
        <ErrorText text={hint || (place.error ? errorText(place.error) : !trading ? t("trade.notTrading") : "")} />
        <Notice text={done} />
        <Button
          type="submit"
          disabled={!trading || Boolean(problem) || place.isPending || !s}
          className={`w-full ${buy ? "!bg-emerald-500 hover:!bg-emerald-400" : "!bg-red-500 hover:!bg-red-400"} !text-white`}
        >
          {actions.find(([k]) => k === action)?.[1] ?? ""}
        </Button>
        <p className="text-xs text-gray-500">
          {t("markets.rules")}: {t("markets.tick")} {contract.tick_size} · {t("markets.lot")} {contract.lot_size} · {t("markets.minNotional")} {contract.min_notional} {contract.quote_asset} · {t("markets.fees")} {percent(contract.maker_fee_rate).replace("+", "")} / {percent(contract.taker_fee_rate).replace("+", "")}
        </p>
      </form>
    </Card>
  );
}

function PositionsPanel({ contract }: { contract: Contract }) {
  const { t } = useTranslation();
  const positions = useQuery({
    queryKey: ["positions", contract.symbol],
    queryFn: () => unwrap(derivativesApi.GET("/v1/derivatives/positions", { params: { query: { symbol: contract.symbol } } })),
    refetchInterval: 5000,
  });
  const list = positions.data?.positions ?? [];
  return (
    <Card title={`${t("futures.positions")} (${list.length})`}>
      <ErrorText text={positions.error ? errorText(positions.error) : ""} />
      {list.length === 0 ? (
        <p className="py-4 text-center text-sm text-gray-500">{t("futures.noPositions")}</p>
      ) : (
        <div className="space-y-3" data-testid="positions">
          {list.map((p) => <PositionRow key={p.position_id} contract={contract} p={p} />)}
        </div>
      )}
    </Card>
  );
}

function PositionRow({ contract, p }: { contract: Contract; p: Position }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [panel, setPanel] = useState<"" | "tpsl" | "margin">("");
  const long = !p.quantity.startsWith("-");
  const qty = p.quantity.replace(/^-/, "");
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["positions"] });
    void qc.invalidateQueries({ queryKey: ["orders"] });
    void qc.invalidateQueries({ queryKey: ["futures-account"] });
  };
  const close = useMutation({
    mutationFn: () => {
      const body: Record<string, unknown> = { symbol: contract.symbol, side: long ? "SELL" : "BUY", type: "MARKET", quantity: qty };
      if (p.position_side === "BOTH") body.reduce_only = true;
      else body.position_side = p.position_side;
      return unwrap(
        derivativesApi.POST("/v1/derivatives/orders", {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          body: body as any,
          headers: { "Idempotency-Key": crypto.randomUUID() },
        }),
      );
    },
    onSuccess: refresh,
  });
  const cell = (label: string, value: React.ReactNode, tone = "text-gray-200") => (
    <div>
      <div className="text-xs text-gray-500">{label}</div>
      <div className={`font-mono text-sm ${tone}`}>{value}</div>
    </div>
  );
  return (
    <div className="rounded-lg border border-white/5 p-3">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <Badge tone={long ? "green" : "red"}>{codeText(long ? "LONG" : "SHORT")}</Badge>
        <span className="text-sm">{codeText(p.margin_mode)} {p.leverage}x</span>
        <div className="ml-auto flex gap-3 text-xs">
          <button className="text-[#f0b90b] hover:underline" onClick={() => setPanel(panel === "tpsl" ? "" : "tpsl")}>{t("futures.tpsl")}</button>
          {p.margin_mode === "ISOLATED" && (
            <button className="text-[#f0b90b] hover:underline" onClick={() => setPanel(panel === "margin" ? "" : "margin")}>{t("futures.adjustMargin")}</button>
          )}
          <button className="text-red-300 hover:underline disabled:opacity-40" disabled={close.isPending} onClick={() => close.mutate()}>{t("futures.marketClose")}</button>
        </div>
      </div>
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-8">
        {cell(t("trade.quantity"), format(p.quantity))}
        {cell(t("futures.entryPrice"), format(p.entry_price))}
        {cell(t("futures.mark"), p.mark_price ? format(p.mark_price) : "—")}
        {cell(t("futures.liquidationPrice"), p.liquidation_price ? format(p.liquidation_price) : "—")}
        {cell(t("futures.margin"), format(p.margin))}
        {cell(t("futures.maintenance"), p.maintenance_margin ? format(p.maintenance_margin) : "—")}
        {cell(t("futures.unrealized"), p.unrealized_pnl ? signed(format(p.unrealized_pnl)) : "—", pnlTone(p.unrealized_pnl))}
        {cell(t("futures.realized"), signed(format(p.realized_pnl)), pnlTone(p.realized_pnl))}
      </div>
      <ErrorText text={close.error ? errorText(close.error) : ""} />
      {panel === "tpsl" && <TpSlForm contract={contract} p={p} onDone={() => setPanel("")} />}
      {panel === "margin" && <MarginForm contract={contract} p={p} onDone={() => setPanel("")} />}
    </div>
  );
}

function TpSlForm({ contract, p, onDone }: { contract: Contract; p: Position; onDone: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [kind, setKind] = useState<"TAKE_PROFIT" | "STOP_LOSS">("TAKE_PROFIT");
  const [trigger, setTrigger] = useState("");
  const [by, setBy] = useState<"MARK" | "LAST">("MARK");
  const [orderType, setOrderType] = useState<"MARKET" | "LIMIT">("MARKET");
  const [price, setPrice] = useState("");
  const [qty, setQty] = useState("");
  const create = useMutation({
    mutationFn: () => {
      const body: Record<string, unknown> = { symbol: contract.symbol, position_side: p.position_side, kind, trigger_price: trigger, trigger_by: by, order_type: orderType };
      if (orderType === "LIMIT") body.price = price;
      if (qty) body.quantity = qty;
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return unwrap(derivativesApi.POST("/v1/derivatives/conditional-orders", { body: body as any }));
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["conditionals"] });
      onDone();
    },
  });
  const pill = (active: boolean) => `rounded px-2 py-0.5 text-xs ${active ? "bg-white/15 text-white" : "text-gray-400 hover:text-white"}`;
  return (
    <form className="mt-3 space-y-2 border-t border-white/5 pt-3" onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
      <div className="flex flex-wrap gap-2">
        {(["TAKE_PROFIT", "STOP_LOSS"] as const).map((k) => <button key={k} type="button" className={pill(kind === k)} onClick={() => setKind(k)}>{codeText(k)}</button>)}
        <span className="text-gray-600">|</span>
        {(["MARK", "LAST"] as const).map((k) => <button key={k} type="button" className={pill(by === k)} onClick={() => setBy(k)}>{t(k === "MARK" ? "futures.byMark" : "futures.byLast")}</button>)}
        <span className="text-gray-600">|</span>
        {(["MARKET", "LIMIT"] as const).map((k) => <button key={k} type="button" className={pill(orderType === k)} onClick={() => setOrderType(k)}>{codeText(k)}</button>)}
      </div>
      <div className="grid gap-2 sm:grid-cols-3">
        <Field label={t("futures.triggerPrice")} inputMode="decimal" value={trigger} onChange={(e) => setTrigger(e.target.value)} />
        {orderType === "LIMIT" && <Field label={t("trade.price")} inputMode="decimal" value={price} onChange={(e) => setPrice(e.target.value)} />}
        <Field label={t("trade.quantity")} inputMode="decimal" value={qty} placeholder={t("futures.wholePosition")} onChange={(e) => setQty(e.target.value)} />
      </div>
      <ErrorText text={create.error ? errorText(create.error) : ""} />
      <Button type="submit" variant="ghost" disabled={!trigger || create.isPending}>{t("common.confirm")}</Button>
    </form>
  );
}

function MarginForm({ contract, p, onDone }: { contract: Contract; p: Position; onDone: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [amount, setAmount] = useState("");
  const [add, setAdd] = useState(true);
  const adjust = useMutation({
    mutationFn: () =>
      unwrap(
        derivativesApi.POST("/v1/derivatives/positions/{symbol}/margin", {
          params: { path: { symbol: contract.symbol } },
          body: { position_side: p.position_side, amount: add ? amount : `-${amount}` },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["positions"] });
      void qc.invalidateQueries({ queryKey: ["futures-account"] });
      onDone();
    },
  });
  const bad = amount ? checkAmount(amount, QUOTE_DECIMALS) : "empty";
  return (
    <form className="mt-3 flex flex-wrap items-end gap-2 border-t border-white/5 pt-3" onSubmit={(e) => { e.preventDefault(); if (bad === "ok") adjust.mutate(); }}>
      <div className="flex gap-1 text-xs">
        <button type="button" className={add ? "text-white" : "text-gray-500"} onClick={() => setAdd(true)}>{t("futures.addMargin")}</button>
        <span className="text-gray-600">/</span>
        <button type="button" className={!add ? "text-white" : "text-gray-500"} onClick={() => setAdd(false)}>{t("futures.reduceMargin")}</button>
      </div>
      <div className="w-40"><Field label={`${t("transfer.amount")} (${contract.quote_asset})`} inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} /></div>
      <Button type="submit" variant="ghost" disabled={bad !== "ok" || adjust.isPending}>{t("common.confirm")}</Button>
      <ErrorText text={adjust.error ? errorText(adjust.error) : ""} />
    </form>
  );
}

function OrdersPanel({ contract }: { contract: Contract }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const symbol = contract.symbol;
  const [tab, setTab] = useState<"open" | "conditional" | "history" | "fills" | "funding">("open");
  const open = useQuery({
    queryKey: ["orders", "perp", symbol, "ACTIVE"],
    queryFn: () => unwrap(derivativesApi.GET("/v1/derivatives/orders", { params: { query: { symbol, status: "ACTIVE", limit: 50 } } })),
  });
  const history = useQuery({
    queryKey: ["orders", "perp", symbol, "ALL"],
    queryFn: () => unwrap(derivativesApi.GET("/v1/derivatives/orders", { params: { query: { symbol, limit: 50 } } })),
    enabled: tab === "history",
  });
  const conditionals = useQuery({
    queryKey: ["conditionals", symbol],
    queryFn: () => unwrap(derivativesApi.GET("/v1/derivatives/conditional-orders", { params: { query: { symbol, status: "ACTIVE", limit: 50 } } })),
  });
  const fills = useQuery({
    queryKey: ["fills", "perp", symbol],
    queryFn: () => unwrap(derivativesApi.GET("/v1/derivatives/fills", { params: { query: { symbol, limit: 50 } } })),
    enabled: tab === "fills",
  });
  const funding = useQuery({
    queryKey: ["funding", symbol],
    queryFn: () => unwrap(derivativesApi.GET("/v1/derivatives/funding", { params: { query: { symbol, limit: 50 } } })),
    enabled: tab === "funding",
  });
  const cancel = useMutation({
    mutationFn: (id: string) => unwrap(derivativesApi.DELETE("/v1/derivatives/orders/{order_id}", { params: { path: { order_id: id } } })),
    onSettled: () => void qc.invalidateQueries({ queryKey: ["orders"] }),
  });
  const cancelConditional = useMutation({
    mutationFn: (id: string) =>
      unwrap(derivativesApi.DELETE("/v1/derivatives/conditional-orders/{conditional_id}", { params: { path: { conditional_id: id } } })),
    onSettled: () => void qc.invalidateQueries({ queryKey: ["conditionals"] }),
  });
  const tabs = [
    ["open", `${t("trade.openOrders")} (${open.data?.items.length ?? 0})`],
    ["conditional", `${t("futures.tpsl")} (${conditionals.data?.items.length ?? 0})`],
    ["history", t("trade.history")],
    ["fills", t("trade.fills")],
    ["funding", t("futures.fundingHistory")],
  ] as const;
  const err = open.error ?? history.error ?? conditionals.error ?? fills.error ?? funding.error ?? cancel.error ?? cancelConditional.error;
  const empty = <p className="py-4 text-center text-sm text-gray-500">{t("common.none")}</p>;
  const orders = (tab === "open" ? open.data?.items : history.data?.items) ?? [];
  return (
    <Card
      title={
        <div className="flex flex-wrap gap-4">
          {tabs.map(([k, label]) => (
            <button key={k} onClick={() => setTab(k)} className={tab === k ? "text-white" : "text-gray-500 hover:text-gray-300"}>{label}</button>
          ))}
        </div>
      }
    >
      <ErrorText text={err ? errorText(err) : ""} />
      <div className="overflow-x-auto">
        {(tab === "open" || tab === "history") && (orders.length === 0 ? empty : (
          <table className="w-full min-w-[680px] text-sm" data-testid={`perp-orders-${tab}`}>
            <thead className="text-left text-xs text-gray-500">
              <tr>
                <th className="py-1">{t("trade.time")}</th><th>{t("trade.side")}</th><th>{t("trade.type")}</th>
                <th className="text-right">{t("trade.price")}</th><th className="text-right">{t("trade.filled")}</th>
                <th className="text-right">{t("futures.realized")}</th><th className="text-right">{t("trade.status")}</th><th />
              </tr>
            </thead>
            <tbody>
              {orders.map((o) => (
                <tr key={o.order_id} className="border-t border-white/5">
                  <td className="py-2 text-xs text-gray-400">{new Date(o.created_at).toLocaleString()}</td>
                  <td className={o.side === "BUY" ? "text-emerald-400" : "text-red-400"}>
                    {codeText(o.side)}{o.position_side !== "BOTH" ? ` ${codeText(o.position_side)}` : ""}{o.reduce_only ? ` · ${t("futures.reduceOnlyOrder")}` : ""}
                  </td>
                  <td>{codeText(o.type)}</td>
                  <td className="text-right font-mono">{format(o.price)}</td>
                  <td className="text-right font-mono">{format(o.filled_quantity)} / {format(o.quantity)}</td>
                  <td className={`text-right font-mono ${pnlTone(o.realized_pnl)}`}>{signed(format(o.realized_pnl))}</td>
                  <td className="text-right">{codeText(o.status)}</td>
                  <td className="text-right">
                    {tab === "open" && (
                      <button className="text-xs text-[#f0b90b] hover:underline" onClick={() => cancel.mutate(o.order_id)}>{t("trade.cancel")}</button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ))}
        {tab === "conditional" && ((conditionals.data?.items.length ?? 0) === 0 ? empty : (
          <table className="w-full min-w-[640px] text-sm">
            <thead className="text-left text-xs text-gray-500">
              <tr><th className="py-1">{t("trade.type")}</th><th>{t("trade.side")}</th><th className="text-right">{t("futures.triggerPrice")}</th><th className="text-right">{t("trade.quantity")}</th><th /></tr>
            </thead>
            <tbody>
              {(conditionals.data?.items ?? []).map((c) => (
                <tr key={c.conditional_id} className="border-t border-white/5">
                  <td className="py-2">{codeText(c.kind)} · {codeText(c.order_type)}</td>
                  <td className={c.side === "BUY" ? "text-emerald-400" : "text-red-400"}>{codeText(c.side)}</td>
                  <td className="text-right font-mono">{format(c.trigger_price)} <span className="text-xs text-gray-500">{t(c.trigger_by === "MARK" ? "futures.byMark" : "futures.byLast")}</span></td>
                  <td className="text-right font-mono">{c.quantity ? format(c.quantity) : t("futures.wholePosition")}</td>
                  <td className="text-right"><button className="text-xs text-[#f0b90b] hover:underline" onClick={() => cancelConditional.mutate(c.conditional_id)}>{t("trade.cancel")}</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        ))}
        {tab === "fills" && ((fills.data?.items.length ?? 0) === 0 ? empty : (
          <table className="w-full min-w-[680px] text-sm" data-testid="perp-fills">
            <thead className="text-left text-xs text-gray-500">
              <tr>
                <th className="py-1">{t("trade.time")}</th><th>{t("trade.side")}</th><th>{t("trade.role")}</th>
                <th className="text-right">{t("trade.price")}</th><th className="text-right">{t("trade.quantity")}</th>
                <th className="text-right">{t("trade.fee")}</th><th className="text-right">{t("futures.realized")}</th>
              </tr>
            </thead>
            <tbody>
              {(fills.data?.items ?? []).map((f) => (
                <tr key={`${f.trade_id}${f.side}`} className="border-t border-white/5">
                  <td className="py-2 text-xs text-gray-400">{new Date(f.executed_at).toLocaleString()}</td>
                  <td className={f.side === "BUY" ? "text-emerald-400" : "text-red-400"}>{codeText(f.side)}{f.liquidation ? ` · ${t("futures.liquidation")}` : ""}</td>
                  <td>{codeText(f.role)}</td>
                  <td className="text-right font-mono">{format(f.price)}</td>
                  <td className="text-right font-mono">{format(f.quantity)}</td>
                  <td className="text-right font-mono">{format(f.fee)}</td>
                  <td className={`text-right font-mono ${pnlTone(f.realized_pnl)}`}>{signed(format(f.realized_pnl))}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ))}
        {tab === "funding" && ((funding.data?.items.length ?? 0) === 0 ? empty : (
          <table className="w-full min-w-[560px] text-sm">
            <thead className="text-left text-xs text-gray-500">
              <tr><th className="py-1">{t("trade.time")}</th><th className="text-right">{t("trade.quantity")}</th><th className="text-right">{t("futures.funding")}</th><th className="text-right">{t("futures.mark")}</th><th className="text-right">{t("transfer.amount")}</th></tr>
            </thead>
            <tbody>
              {(funding.data?.items ?? []).map((f) => (
                <tr key={`${f.funding_time}${f.position_side}`} className="border-t border-white/5">
                  <td className="py-2 text-xs text-gray-400">{new Date(f.funding_time).toLocaleString()}</td>
                  <td className="text-right font-mono">{format(f.quantity)}</td>
                  <td className="text-right font-mono">{ratePercent(f.funding_rate)}</td>
                  <td className="text-right font-mono">{format(f.mark_price)}</td>
                  <td className={`text-right font-mono ${pnlTone(f.amount)}`}>{signed(format(f.amount))}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ))}
      </div>
    </Card>
  );
}
