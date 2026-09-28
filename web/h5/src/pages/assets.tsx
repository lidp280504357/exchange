import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { accountApi, marketApi, unwrap } from "../api/client";
import { Badge, Button, Card, ErrorText, Time } from "../components/ui";
import { codeText, errorText } from "../i18n";
import { format } from "../lib/decimal";

export function useBalances() {
  return useQuery({ queryKey: ["balances"], queryFn: () => unwrap(accountApi.GET("/v1/account/balances")) });
}

export function AssetsPage() {
  const { t } = useTranslation();
  const [tab, setTab] = useState<"SPOT" | "FUTURES">("SPOT");
  const balances = useBalances();
  const assets = useQuery({ queryKey: ["assets"], queryFn: () => unwrap(marketApi.GET("/v1/market/assets")) });
  const rows = (balances.data?.balances ?? []).filter((b) => b.account_type === tab);
  const names = new Map((assets.data?.assets ?? []).map((a) => [a.asset_code, a.name]));

  return (
    <div className="space-y-4">
      <Card
        title={t("assets.title")}
        actions={
          <div className="flex items-center gap-2">
            <Badge tone="green">{t("assets.live")}</Badge>
            <Link to="/deposit"><Button variant="ghost">{t("nav.deposit")}</Button></Link>
            <Link to="/withdraw"><Button variant="ghost">{t("nav.withdraw")}</Button></Link>
            <Link to="/transfer"><Button variant="ghost">{t("nav.transfer")}</Button></Link>
          </div>
        }
      >
        <div className="mb-3 flex gap-2">
          {(["SPOT", "FUTURES"] as const).map((k) => (
            <button key={k} onClick={() => setTab(k)} className={`rounded-md px-3 py-1 text-sm ${tab === k ? "bg-white/10 text-white" : "text-gray-400"}`}>
              {t(k === "SPOT" ? "assets.spot" : "assets.futures")}
            </button>
          ))}
        </div>
        <ErrorText text={balances.error ? errorText(balances.error) : ""} />
        {rows.length === 0 && !balances.isLoading ? (
          <p className="text-sm text-gray-500">{t("assets.empty")}</p>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-gray-500">
              <tr><th className="py-1">{t("assets.asset")}</th><th className="text-right">{t("assets.available")}</th><th className="text-right">{t("assets.frozen")}</th></tr>
            </thead>
            <tbody>
              {rows.map((b) => (
                <tr key={b.asset} className="border-t border-white/5">
                  <td className="py-2"><span className="font-medium">{b.asset}</span> <span className="text-xs text-gray-500">{names.get(b.asset)}</span></td>
                  <td className="text-right font-mono">{format(b.available)}</td>
                  <td className="text-right font-mono text-gray-400">{format(b.frozen)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
      <LedgerCard />
    </div>
  );
}

function LedgerCard() {
  const { t } = useTranslation();
  const entries = useInfiniteQuery({
    queryKey: ["ledger"],
    initialPageParam: "",
    queryFn: ({ pageParam }) => unwrap(accountApi.GET("/v1/account/ledger", { params: { query: { limit: 20, cursor: pageParam || undefined } } })),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
  const items = entries.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <Card title={t("ledger.title")}>
      {items.length === 0 ? (
        <p className="text-sm text-gray-500">{t("common.none")}</p>
      ) : (
        <ul className="divide-y divide-white/5 text-sm">
          {items.map((e) => (
            <li key={e.id} className="flex items-center justify-between py-2">
              <div>
                <div>{codeText(e.entry_type)} <span className="text-xs text-gray-500">{codeText(e.account_type)}{e.balance_kind === "FROZEN" ? ` · ${codeText("FROZEN")}` : ""}</span></div>
                <div className="text-xs text-gray-500"><Time value={e.posted_at} /></div>
              </div>
              <div className={`font-mono ${e.amount.startsWith("-") ? "text-red-300" : "text-emerald-300"}`}>
                {e.amount.startsWith("-") ? "" : "+"}{format(e.amount)} {e.asset}
              </div>
            </li>
          ))}
        </ul>
      )}
      {entries.hasNextPage && <Button variant="ghost" className="mt-3" onClick={() => entries.fetchNextPage()}>{t("common.more")}</Button>}
    </Card>
  );
}
