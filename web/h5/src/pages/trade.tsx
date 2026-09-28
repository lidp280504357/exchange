import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CandlestickSeries, createChart, HistogramSeries, type UTCTimestamp } from "lightweight-charts";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useParams } from "react-router";
import { marketApi, tradingApi, unwrap } from "../api/client";
import type { components as MarketTypes } from "../api/gen/market";
import { Badge, Button, Card, ErrorText, Field, Notice } from "../components/ui";
import { codeText, errorText } from "../i18n";
import { checkAmount, compare, format } from "../lib/decimal";
import { applyDepth, type Book, decimalsOf, marketSocket, mul, percent } from "../lib/market";
import { useBalances } from "./assets";
import { changeTone, statusTone, usePairs } from "./markets";

type Pair = MarketTypes["schemas"]["TradingPair"];
type Ticker = MarketTypes["schemas"]["Ticker"];
type PublicTrade = MarketTypes["schemas"]["PublicTrade"];
type Candle = MarketTypes["schemas"]["Candle"];

export function TradePage() {
  const { t } = useTranslation();
  const { symbol = "" } = useParams();
  const pairs = usePairs();
  const pair = pairs.data?.pairs.find((p) => p.symbol === symbol.toUpperCase());
  const [picked, setPicked] = useState({ price: "", at: 0 });
  if (pairs.isLoading) return <p className="p-8 text-center text-sm text-gray-500">{t("common.loading")}</p>;
  if (!pair) return <ErrorText text={pairs.error ? errorText(pairs.error) : t("errors.COMMON_NOT_FOUND")} />;
  return (
    <div className="space-y-3">
      <TickerBar pair={pair} />
      <div className="grid gap-3 lg:grid-cols-[1fr_300px]">
        <CandleChart symbol={pair.symbol} />
        <OrderBook pair={pair} onPick={(price) => setPicked({ price, at: Date.now() })} />
      </div>
      <div className="grid gap-3 lg:grid-cols-[1fr_300px]">
        <OrderForm pair={pair} picked={picked} />
        <RecentTrades symbol={pair.symbol} />
      </div>
      <OrdersPanel pair={pair} />
    </div>
  );
}

function TickerBar({ pair }: { pair: Pair }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const key = ["ticker", pair.symbol];
  const ticker = useQuery({
    queryKey: key,
    queryFn: () => unwrap(marketApi.GET("/v1/market/{symbol}/ticker", { params: { path: { symbol: pair.symbol } } })),
  });
  useEffect(
    () => marketSocket.subscribe(`ticker:${pair.symbol}`, (m) => qc.setQueryData(["ticker", pair.symbol], m.data as Ticker)),
    [pair.symbol, qc],
  );
  const tk = ticker.data;
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
            {pair.base_asset}<span className="text-gray-500">/{pair.quote_asset}</span>
          </Link>{" "}
          <Badge tone={statusTone(pair.status)}>{codeText(pair.status)}</Badge>
        </div>
        <div className={`font-mono text-xl ${changeTone(tk?.change)}`}>{tk?.last ? format(tk.last) : t("trade.noAnchor")}</div>
        {stat(t("markets.change"), percent(tk?.change), changeTone(tk?.change))}
        {stat(t("markets.high"), tk?.high ? format(tk.high) : "—")}
        {stat(t("markets.low"), tk?.low ? format(tk.low) : "—")}
        {stat(`${t("markets.volume")} (${pair.base_asset})`, tk ? format(tk.volume) : "—")}
      </div>
    </Card>
  );
}

const chartIntervals = ["1m", "5m", "15m", "1h", "4h", "1d", "1w"] as const;

// Prices become numbers only to draw them; nothing is computed from them
// (ADR-0008).
function toBar(c: Candle) {
  return { time: (Date.parse(c.open_time) / 1000) as UTCTimestamp, open: Number(c.open), high: Number(c.high), low: Number(c.low), close: Number(c.close) };
}

function toVolume(c: Candle) {
  const up = compare(c.close, c.open) >= 0;
  return { time: (Date.parse(c.open_time) / 1000) as UTCTimestamp, value: Number(c.volume), color: up ? "rgba(14,203,129,0.4)" : "rgba(246,70,93,0.4)" };
}

function CandleChart({ symbol }: { symbol: string }) {
  const { t } = useTranslation();
  const [interval, setChartInterval] = useState<(typeof chartIntervals)[number]>("15m");
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const chart = createChart(el, {
      autoSize: true,
      layout: { background: { color: "transparent" }, textColor: "#9ca3af", attributionLogo: true },
      grid: { vertLines: { color: "rgba(255,255,255,0.04)" }, horzLines: { color: "rgba(255,255,255,0.04)" } },
      rightPriceScale: { borderColor: "rgba(255,255,255,0.1)" },
      timeScale: { borderColor: "rgba(255,255,255,0.1)", timeVisible: true, secondsVisible: false },
    });
    const candles = chart.addSeries(CandlestickSeries, {
      upColor: "#0ecb81", downColor: "#f6465d", borderVisible: false, wickUpColor: "#0ecb81", wickDownColor: "#f6465d",
    });
    const volume = chart.addSeries(HistogramSeries, { priceFormat: { type: "volume" }, priceScaleId: "", lastValueVisible: false, priceLineVisible: false });
    volume.priceScale().applyOptions({ scaleMargins: { top: 0.8, bottom: 0 } });
    let live = true;
    void marketApi.GET("/v1/market/{symbol}/candles", { params: { path: { symbol }, query: { interval, limit: 300 } } }).then(({ data }) => {
      if (!live || !data) return;
      candles.setData(data.candles.map(toBar));
      volume.setData(data.candles.map(toVolume));
    });
    const off = marketSocket.subscribe(`candles:${symbol}:${interval}`, (m) => {
      try {
        candles.update(toBar(m.data as Candle));
        volume.update(toVolume(m.data as Candle));
      } catch {
        // older than the last bar drawn: the fetched data already has it
      }
    });
    return () => {
      live = false;
      off();
      chart.remove();
    };
  }, [symbol, interval]);
  return (
    <Card
      title={t("trade.chart")}
      actions={
        <div className="flex gap-1">
          {chartIntervals.map((i) => (
            <button key={i} onClick={() => setChartInterval(i)} className={`rounded px-2 py-0.5 text-xs ${i === interval ? "bg-white/15 text-white" : "text-gray-400 hover:text-white"}`}>
              {i}
            </button>
          ))}
        </div>
      }
    >
      <div ref={box} className="h-64 sm:h-80" data-testid="chart" />
    </Card>
  );
}

function OrderBook({ pair, onPick }: { pair: Pair; onPick: (price: string) => void }) {
  const { t } = useTranslation();
  const [book, setBook] = useState<Book | null>(null);
  useEffect(() => {
    setBook(null);
    const ch = `depth:${pair.symbol}`;
    let current: Book | null = null;
    return marketSocket.subscribe(ch, (m) => {
      const next = applyDepth(current, m);
      if (!next) {
        // An update did not follow: start over from a fresh snapshot.
        current = null;
        marketSocket.resubscribe(ch);
        return;
      }
      current = next;
      setBook(next);
    });
  }, [pair.symbol]);
  const asks = (book?.asks ?? []).slice(0, 10).reverse();
  const bids = (book?.bids ?? []).slice(0, 10);
  const row = (l: [string, string], tone: string) => (
    <button key={l[0]} onClick={() => onPick(l[0])} className="flex w-full justify-between px-1 py-0.5 font-mono text-xs hover:bg-white/5">
      <span className={tone}>{format(l[0])}</span>
      <span className="text-gray-300">{format(l[1])}</span>
    </button>
  );
  return (
    <Card title={t("trade.book")}>
      <div className="flex justify-between px-1 text-xs text-gray-500">
        <span>{t("trade.price")} ({pair.quote_asset})</span>
        <span>{t("trade.quantity")} ({pair.base_asset})</span>
      </div>
      <div data-testid="asks">{asks.map((l) => row(l, "text-red-400"))}</div>
      <div className="my-1 border-t border-white/10" />
      <div data-testid="bids">{bids.map((l) => row(l, "text-emerald-400"))}</div>
      {book && asks.length + bids.length === 0 && <p className="py-4 text-center text-xs text-gray-500">{t("common.none")}</p>}
    </Card>
  );
}

function RecentTrades({ symbol }: { symbol: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const trades = useQuery({
    queryKey: ["trades", symbol],
    queryFn: () => unwrap(marketApi.GET("/v1/market/{symbol}/trades", { params: { path: { symbol }, query: { limit: 30 } } })),
  });
  useEffect(
    () =>
      marketSocket.subscribe(`trades:${symbol}`, (m) => {
        const tr = m.data as PublicTrade;
        qc.setQueryData<{ symbol: string; trades: PublicTrade[] }>(["trades", symbol], (old) =>
          old ? { ...old, trades: [tr, ...old.trades.filter((x) => x.trade_id !== tr.trade_id)].slice(0, 30) } : old,
        );
      }),
    [symbol, qc],
  );
  return (
    <Card title={t("trade.trades")}>
      <div className="max-h-72 overflow-y-auto" data-testid="trades">
        {(trades.data?.trades ?? []).map((tr) => (
          <div key={tr.trade_id} className="flex justify-between py-0.5 font-mono text-xs">
            <span className={tr.taker_side === "BUY" ? "text-emerald-400" : "text-red-400"}>{format(tr.price)}</span>
            <span className="text-gray-300">{format(tr.quantity)}</span>
            <span className="text-gray-500">{new Date(tr.executed_at).toLocaleTimeString()}</span>
          </div>
        ))}
        {trades.data?.trades.length === 0 && <p className="py-4 text-center text-xs text-gray-500">{t("common.none")}</p>}
      </div>
    </Card>
  );
}

function OrderForm({ pair, picked }: { pair: Pair; picked: { price: string; at: number } }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [side, setSide] = useState<"BUY" | "SELL">("BUY");
  const [type, setType] = useState<"LIMIT" | "MARKET">("LIMIT");
  const [price, setPrice] = useState("");
  const [qty, setQty] = useState("");
  const [quote, setQuote] = useState("");
  const [done, setDone] = useState("");
  const balances = useBalances();
  const assets = useQuery({ queryKey: ["assets"], queryFn: () => unwrap(marketApi.GET("/v1/market/assets")), staleTime: 60000 });
  useEffect(() => {
    if (picked.price) {
      setPrice(picked.price);
      setType("LIMIT");
    }
  }, [picked]);
  const asset = side === "BUY" ? pair.quote_asset : pair.base_asset;
  const available = balances.data?.balances.find((b) => b.account_type === "SPOT" && b.asset === asset)?.available ?? "0";
  const quoteDecimals = assets.data?.assets.find((a) => a.asset_code === pair.quote_asset)?.decimals ?? 8;
  const byQuote = type === "MARKET" && side === "BUY";
  const checks: [string, string, number][] = byQuote
    ? [[quote, "quote", quoteDecimals]]
    : type === "LIMIT"
      ? [[price, "price", decimalsOf(pair.tick_size)], [qty, "qty", decimalsOf(pair.lot_size)]]
      : [[qty, "qty", decimalsOf(pair.lot_size)]];
  const problem = checks.map(([v, , n]) => (v ? checkAmount(v, n) : "empty")).find((r) => r !== "ok");
  const firstBad = checks.find(([v, , n]) => v && checkAmount(v, n) !== "ok");
  const hint = firstBad ? t(`errors.${checkAmount(firstBad[0], firstBad[2])}`, { n: firstBad[2] }) : "";
  const total = type === "LIMIT" && !problem ? mul(price, qty) : "";
  const place = useMutation({
    mutationFn: () => {
      const body: Record<string, string> = { symbol: pair.symbol, side, type };
      if (type === "LIMIT") body.price = price;
      if (byQuote) body.quote_amount = quote;
      else body.quantity = qty;
      return unwrap(
        tradingApi.POST("/v1/orders", {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          body: body as any,
          headers: { "Idempotency-Key": crypto.randomUUID() },
        }),
      );
    },
    onSuccess: () => {
      setDone(t("trade.placed"));
      setQty("");
      setQuote("");
      void qc.invalidateQueries({ queryKey: ["orders"] });
      void qc.invalidateQueries({ queryKey: ["balances"] });
    },
  });
  useEffect(() => {
    if (!done) return;
    const tm = setTimeout(() => setDone(""), 4000);
    return () => clearTimeout(tm);
  }, [done]);
  const tab = (active: boolean, tone: string) => `flex-1 rounded-md py-1.5 text-sm font-medium ${active ? tone : "text-gray-400 hover:text-white"}`;
  const trading = pair.status === "TRADING";
  return (
    <Card>
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          setDone("");
          if (!problem) place.mutate();
        }}
      >
        <div className="flex gap-1 rounded-lg bg-black/30 p-1">
          <button type="button" className={tab(side === "BUY", "bg-emerald-500/20 text-emerald-300")} onClick={() => setSide("BUY")}>{t("trade.buy")}</button>
          <button type="button" className={tab(side === "SELL", "bg-red-500/20 text-red-300")} onClick={() => setSide("SELL")}>{t("trade.sell")}</button>
        </div>
        <div className="flex gap-4 text-sm">
          {(["LIMIT", "MARKET"] as const).map((k) => (
            <button key={k} type="button" onClick={() => setType(k)} className={type === k ? "text-[#f0b90b]" : "text-gray-400 hover:text-white"}>
              {k === "LIMIT" ? t("trade.limit") : t("trade.market")}
            </button>
          ))}
        </div>
        {type === "LIMIT" && (
          <Field label={`${t("trade.price")} (${pair.quote_asset})`} inputMode="decimal" name="price" value={price} onChange={(e) => setPrice(e.target.value)} />
        )}
        {byQuote ? (
          <Field label={`${t("trade.spend")} (${pair.quote_asset})`} inputMode="decimal" name="quote" value={quote} onChange={(e) => setQuote(e.target.value)} hint={t("trade.marketHint")} />
        ) : (
          <Field
            label={`${t("trade.quantity")} (${pair.base_asset})`}
            inputMode="decimal"
            name="quantity"
            value={qty}
            onChange={(e) => setQty(e.target.value)}
            hint={type === "MARKET" ? t("trade.marketHint") : undefined}
          />
        )}
        <div className="flex justify-between text-xs text-gray-400">
          <span>{t("trade.available")}: <span className="font-mono text-gray-200">{format(available)} {asset}</span></span>
          {total && <span>{t("trade.total")}: <span className="font-mono text-gray-200">{format(total)} {pair.quote_asset}</span></span>}
        </div>
        <ErrorText text={hint || (place.error ? errorText(place.error) : !trading ? t("trade.notTrading") : "")} />
        <Notice text={done} />
        <Button
          type="submit"
          disabled={!trading || Boolean(problem) || place.isPending}
          className={`w-full ${side === "BUY" ? "!bg-emerald-500 hover:!bg-emerald-400" : "!bg-red-500 hover:!bg-red-400"} !text-white`}
        >
          {side === "BUY" ? t("trade.placeBuy", { base: pair.base_asset }) : t("trade.placeSell", { base: pair.base_asset })}
        </Button>
        <p className="text-xs text-gray-500">
          {t("markets.rules")}: {t("markets.tick")} {pair.tick_size} · {t("markets.lot")} {pair.lot_size} · {t("markets.minNotional")} {pair.min_notional} {pair.quote_asset} · {t("markets.fees")} {percent(pair.maker_fee_rate).replace("+", "")} / {percent(pair.taker_fee_rate).replace("+", "")}
        </p>
      </form>
    </Card>
  );
}

function OrdersPanel({ pair }: { pair: Pair }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [tab, setTab] = useState<"open" | "history" | "fills">("open");
  const symbol = pair.symbol;
  const open = useQuery({
    queryKey: ["orders", symbol, "ACTIVE"],
    queryFn: () => unwrap(tradingApi.GET("/v1/orders", { params: { query: { symbol, status: "ACTIVE", limit: 50 } } })),
  });
  const history = useQuery({
    queryKey: ["orders", symbol, "ALL"],
    queryFn: () => unwrap(tradingApi.GET("/v1/orders", { params: { query: { symbol, limit: 50 } } })),
    enabled: tab === "history",
  });
  const fills = useQuery({
    queryKey: ["fills", symbol],
    queryFn: () => unwrap(tradingApi.GET("/v1/fills", { params: { query: { symbol, limit: 50 } } })),
    enabled: tab === "fills",
  });
  const refresh = () => void qc.invalidateQueries({ queryKey: ["orders"] });
  const cancel = useMutation({
    mutationFn: (id: string) => unwrap(tradingApi.DELETE("/v1/orders/{order_id}", { params: { path: { order_id: id } } })),
    // Show the request at once; the engine's answer arrives over "orders".
    onMutate: (id: string) =>
      qc.setQueryData<typeof open.data>(["orders", symbol, "ACTIVE"], (old) =>
        old ? { ...old, items: old.items.map((o) => (o.order_id === id ? { ...o, cancel_requested: true } : o)) } : old,
      ),
    onSettled: refresh,
  });
  const cancelAll = useMutation({
    mutationFn: () => unwrap(tradingApi.DELETE("/v1/orders", { params: { query: { symbol } } })),
    onMutate: () =>
      qc.setQueryData<typeof open.data>(["orders", symbol, "ACTIVE"], (old) =>
        old ? { ...old, items: old.items.map((o) => ({ ...o, cancel_requested: true })) } : old,
      ),
    onSettled: refresh,
  });
  const tabs = [["open", t("trade.openOrders")], ["history", t("trade.history")], ["fills", t("trade.fills")]] as const;
  const orders = (tab === "open" ? open.data?.items : history.data?.items) ?? [];
  const err = open.error ?? history.error ?? fills.error ?? cancel.error ?? cancelAll.error;
  return (
    <Card
      title={
        <div className="flex gap-4">
          {tabs.map(([k, label]) => (
            <button key={k} onClick={() => setTab(k)} className={tab === k ? "text-white" : "text-gray-500 hover:text-gray-300"}>
              {label}{k === "open" && open.data ? ` (${open.data.items.length})` : ""}
            </button>
          ))}
        </div>
      }
      actions={
        tab === "open" && (open.data?.items.length ?? 0) > 0 ? (
          <Button variant="danger" className="!px-3 !py-1 text-xs" onClick={() => cancelAll.mutate()} disabled={cancelAll.isPending}>
            {t("trade.cancelAll")}
          </Button>
        ) : undefined
      }
    >
      <ErrorText text={err ? errorText(err) : ""} />
      <div className="overflow-x-auto">
        {tab !== "fills" ? (
          <table className="w-full min-w-[640px] text-sm" data-testid={`orders-${tab}`}>
            <thead className="text-left text-xs text-gray-500">
              <tr>
                <th className="py-1">{t("trade.time")}</th><th>{t("trade.side")}</th><th>{t("trade.type")}</th>
                <th className="text-right">{t("trade.price")}</th><th className="text-right">{t("trade.filled")}</th>
                <th className="text-right">{t("trade.status")}</th><th />
              </tr>
            </thead>
            <tbody>
              {orders.map((o) => (
                <tr key={o.order_id} className="border-t border-white/5">
                  <td className="py-2 text-xs text-gray-400">{new Date(o.created_at).toLocaleString()}</td>
                  <td className={o.side === "BUY" ? "text-emerald-400" : "text-red-400"}>{codeText(o.side)}</td>
                  <td>{codeText(o.type)}</td>
                  <td className="text-right font-mono">{o.price ? format(o.price) : codeText("MARKET")}</td>
                  <td className="text-right font-mono">{format(o.filled_quantity)} / {o.quantity ? format(o.quantity) : `${format(o.quote_amount ?? "0")} ${pair.quote_asset}`}</td>
                  <td className="text-right">
                    <span title={o.cancel_reason ? codeText(o.cancel_reason) : o.reject_reason ? t(`errors.${o.reject_reason}`, { defaultValue: o.reject_reason }) : undefined}>
                      {o.cancel_requested && tab === "open" ? t("trade.canceling") : codeText(o.status)}
                    </span>
                  </td>
                  <td className="text-right">
                    {tab === "open" && !o.cancel_requested && (
                      <button className="text-xs text-[#f0b90b] hover:underline" onClick={() => cancel.mutate(o.order_id)}>{t("trade.cancel")}</button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <table className="w-full min-w-[640px] text-sm" data-testid="fills">
            <thead className="text-left text-xs text-gray-500">
              <tr>
                <th className="py-1">{t("trade.time")}</th><th>{t("trade.side")}</th><th>{t("trade.role")}</th>
                <th className="text-right">{t("trade.price")}</th><th className="text-right">{t("trade.quantity")}</th>
                <th className="text-right">{t("trade.fee")}</th>
              </tr>
            </thead>
            <tbody>
              {(fills.data?.items ?? []).map((f) => (
                <tr key={f.trade_id + f.order_id} className="border-t border-white/5">
                  <td className="py-2 text-xs text-gray-400">{new Date(f.executed_at).toLocaleString()}</td>
                  <td className={f.side === "BUY" ? "text-emerald-400" : "text-red-400"}>{codeText(f.side)}</td>
                  <td>{codeText(f.role)}</td>
                  <td className="text-right font-mono">{format(f.price)}</td>
                  <td className="text-right font-mono">{format(f.quantity)}</td>
                  <td className="text-right font-mono">{format(f.fee)} {f.fee_asset}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {((tab !== "fills" && orders.length === 0) || (tab === "fills" && fills.data?.items.length === 0)) && (
          <p className="py-4 text-center text-sm text-gray-500">{t("common.none")}</p>
        )}
      </div>
    </Card>
  );
}
