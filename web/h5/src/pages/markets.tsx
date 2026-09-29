import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { marketApi, unwrap } from "../api/client";
import type { components } from "../api/gen/market";
import { Badge, Card, ErrorText } from "../components/ui";
import { codeText, errorText } from "../i18n";
import { format } from "../lib/decimal";
import { countdown, ratePercent } from "../lib/futures";
import { marketSocket, percent } from "../lib/market";

type Ticker = components["schemas"]["Ticker"];
type Pair = components["schemas"]["TradingPair"];
type MarkPrice = components["schemas"]["MarkPrice"];

export function usePairs() {
  return useQuery({ queryKey: ["pairs"], queryFn: () => unwrap(marketApi.GET("/v1/market/pairs")), staleTime: 60000 });
}

export function useContracts() {
  return useQuery({ queryKey: ["contracts"], queryFn: () => unwrap(marketApi.GET("/v1/market/contracts")), staleTime: 60000 });
}

// useNow ticks every second, for countdowns.
export function useNow(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  return now;
}

// useMarkPrice follows a contract's mark price, index price and funding
// estimate (REST, then the mark-price: channel every second).
export function useMarkPrice(symbol: string) {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["mark", symbol],
    queryFn: () => unwrap(marketApi.GET("/v1/market/{symbol}/mark-price", { params: { path: { symbol } } })),
    enabled: Boolean(symbol),
  });
  useEffect(() => {
    if (!symbol) return;
    return marketSocket.subscribe(`mark-price:${symbol}`, (m) =>
      qc.setQueryData<MarkPrice>(["mark", symbol], (old) => (old ? { ...old, ...(m.data as Partial<MarkPrice>), degraded: false } : old)),
    );
  }, [symbol, qc]);
  return q;
}

// changeTone colors a 24h change: green up, red down.
export function changeTone(change: string | null | undefined): string {
  if (!change || /^-?0(\.0+)?$/.test(change)) return "text-gray-300";
  return change.startsWith("-") ? "text-red-400" : "text-emerald-400";
}

export function statusTone(s: string) {
  return s === "TRADING" ? "green" : s === "HALT" || s === "CANCEL_ONLY" ? "red" : "yellow";
}

export function MarketsPage() {
  const { t } = useTranslation();
  const [tab, setTab] = useState<"spot" | "perp">("spot");
  return (
    <Card
      title={
        <div className="flex gap-4">
          {(["spot", "perp"] as const).map((k) => (
            <button key={k} onClick={() => setTab(k)} className={tab === k ? "text-white" : "text-gray-500 hover:text-gray-300"}>
              {t(k === "spot" ? "markets.title" : "futures.title")}
            </button>
          ))}
        </div>
      }
    >
      {tab === "spot" ? <SpotMarkets /> : <ContractMarkets />}
    </Card>
  );
}

// useLiveTickers keeps the tickers of symbols up to date.
function useLiveTickers(symbols: string) {
  const qc = useQueryClient();
  const tickers = useQuery({ queryKey: ["tickers"], queryFn: () => unwrap(marketApi.GET("/v1/market/tickers")) });
  useEffect(() => {
    if (!symbols) return;
    const offs = symbols.split(",").map((symbol) =>
      marketSocket.subscribe(`ticker:${symbol}`, (m) => {
        qc.setQueryData<{ tickers: Ticker[] }>(["tickers"], (old) =>
          old ? { tickers: old.tickers.map((tk) => (tk.symbol === symbol ? (m.data as Ticker) : tk)) } : old,
        );
      }),
    );
    return () => offs.forEach((off) => off());
  }, [symbols, qc]);
  return tickers;
}

function SpotMarkets() {
  const { t } = useTranslation();
  const pairs = usePairs();
  const symbols = (pairs.data?.pairs ?? []).map((p) => p.symbol).join(",");
  const tickers = useLiveTickers(symbols);
  const bySymbol = new Map((tickers.data?.tickers ?? []).map((tk) => [tk.symbol, tk]));
  return (
    <>
      <ErrorText text={pairs.error ? errorText(pairs.error) : tickers.error ? errorText(tickers.error) : ""} />
      <div className="overflow-x-auto">
        <table className="w-full min-w-[560px] text-sm">
          <thead className="text-left text-xs text-gray-500">
            <tr>
              <th className="py-1">{t("markets.pair")}</th>
              <th className="text-right">{t("markets.last")}</th>
              <th className="text-right">{t("markets.change")}</th>
              <th className="text-right">{t("markets.high")} / {t("markets.low")}</th>
              <th className="text-right">{t("markets.volume")}</th>
              <th className="text-right">{t("markets.status")}</th>
            </tr>
          </thead>
          <tbody>
            {(pairs.data?.pairs ?? []).map((p: Pair) => {
              const tk = bySymbol.get(p.symbol);
              return (
                <tr key={p.symbol} className="border-t border-white/5">
                  <td className="py-2 font-medium">
                    <Link to={`/trade/${p.symbol}`} className="hover:text-[#f0b90b]">
                      {p.base_asset}<span className="text-gray-500">/{p.quote_asset}</span>
                    </Link>
                  </td>
                  <td className="text-right font-mono">{tk?.last ? format(tk.last) : "—"}</td>
                  <td className={`text-right font-mono ${changeTone(tk?.change)}`}>{percent(tk?.change)}</td>
                  <td className="text-right font-mono text-gray-400">{tk?.high ? `${format(tk.high)} / ${format(tk.low ?? "")}` : "—"}</td>
                  <td className="text-right font-mono text-gray-400">{tk ? format(tk.volume) : "—"}</td>
                  <td className="text-right"><Badge tone={statusTone(p.status)}>{codeText(p.status)}</Badge></td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}

function ContractMarkets() {
  const { t } = useTranslation();
  const contracts = useContracts();
  const list = contracts.data?.contracts ?? [];
  const symbols = list.map((c) => c.symbol).join(",");
  const tickers = useLiveTickers(symbols);
  const bySymbol = new Map((tickers.data?.tickers ?? []).map((tk) => [tk.symbol, tk]));
  const marks = useQueries({
    queries: list.map((c) => ({
      queryKey: ["mark", c.symbol],
      queryFn: () => unwrap(marketApi.GET("/v1/market/{symbol}/mark-price", { params: { path: { symbol: c.symbol } } })),
    })),
  });
  const qc = useQueryClient();
  useEffect(() => {
    if (!symbols) return;
    const offs = symbols.split(",").map((symbol) =>
      marketSocket.subscribe(`mark-price:${symbol}`, (m) =>
        qc.setQueryData<MarkPrice>(["mark", symbol], (old) => (old ? { ...old, ...(m.data as Partial<MarkPrice>) } : old)),
      ),
    );
    return () => offs.forEach((off) => off());
  }, [symbols, qc]);
  const now = useNow();
  return (
    <>
      <ErrorText text={contracts.error ? errorText(contracts.error) : ""} />
      <div className="overflow-x-auto">
        <table className="w-full min-w-[640px] text-sm" data-testid="contracts">
          <thead className="text-left text-xs text-gray-500">
            <tr>
              <th className="py-1">{t("futures.contract")}</th>
              <th className="text-right">{t("markets.last")}</th>
              <th className="text-right">{t("futures.mark")}</th>
              <th className="text-right">{t("markets.change")}</th>
              <th className="text-right">{t("futures.funding")} / {t("futures.countdown")}</th>
              <th className="text-right">{t("futures.maxLeverage")}</th>
              <th className="text-right">{t("markets.status")}</th>
            </tr>
          </thead>
          <tbody>
            {list.map((c, i) => {
              const tk = bySymbol.get(c.symbol);
              const mark = marks[i]?.data;
              return (
                <tr key={c.symbol} className="border-t border-white/5">
                  <td className="py-2 font-medium">
                    <Link to={`/futures/${c.symbol}`} className="hover:text-[#f0b90b]">
                      {c.base_asset}{c.quote_asset}<span className="text-gray-500"> {t("futures.perpetual")}</span>
                    </Link>
                  </td>
                  <td className="text-right font-mono">{tk?.last ? format(tk.last) : "—"}</td>
                  <td className="text-right font-mono">{mark?.mark_price ? format(mark.mark_price) : "—"}</td>
                  <td className={`text-right font-mono ${changeTone(tk?.change)}`}>{percent(tk?.change)}</td>
                  <td className="text-right font-mono text-gray-300">
                    {ratePercent(mark?.funding_rate)} <span className="text-gray-500">{countdown(mark?.next_funding_time, now)}</span>
                  </td>
                  <td className="text-right font-mono text-gray-400">{c.max_leverage}x</td>
                  <td className="text-right"><Badge tone={statusTone(c.status)}>{codeText(c.status)}</Badge></td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}
