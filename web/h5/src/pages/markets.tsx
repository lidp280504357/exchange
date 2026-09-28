import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { marketApi, unwrap } from "../api/client";
import type { components } from "../api/gen/market";
import { Badge, Card, ErrorText } from "../components/ui";
import { codeText, errorText } from "../i18n";
import { format } from "../lib/decimal";
import { marketSocket, percent } from "../lib/market";

type Ticker = components["schemas"]["Ticker"];
type Pair = components["schemas"]["TradingPair"];

export function usePairs() {
  return useQuery({ queryKey: ["pairs"], queryFn: () => unwrap(marketApi.GET("/v1/market/pairs")), staleTime: 60000 });
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
  const qc = useQueryClient();
  const pairs = usePairs();
  const tickers = useQuery({ queryKey: ["tickers"], queryFn: () => unwrap(marketApi.GET("/v1/market/tickers")) });
  const symbols = (pairs.data?.pairs ?? []).map((p) => p.symbol).join(",");

  // Live tickers replace the fetched ones.
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

  const bySymbol = new Map((tickers.data?.tickers ?? []).map((tk) => [tk.symbol, tk]));
  return (
    <Card title={t("markets.title")}>
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
    </Card>
  );
}
