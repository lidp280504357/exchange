import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { marketApi, unwrap, walletApi } from "../api/client";
import { Badge, Button, Card, ErrorText, Time } from "../components/ui";
import { errorText } from "../i18n";
import { format } from "../lib/decimal";

// Block explorers of the networks, for transaction links.
const explorers: Record<string, string> = {
  "ETH-SEPOLIA": "https://sepolia.etherscan.io/tx/",
};

type Option = { asset: string; network: string };

function statusTone(status: string): "gray" | "green" | "yellow" | "red" {
  if (status === "CREDITED") return "green";
  if (status === "REJECTED" || status === "ORPHANED") return "red";
  return "yellow";
}

function shortHash(h: string): string {
  return h.length > 18 ? `${h.slice(0, 10)}…${h.slice(-6)}` : h;
}

export function DepositPage() {
  const { t } = useTranslation();
  const assets = useQuery({ queryKey: ["assets"], queryFn: () => unwrap(marketApi.GET("/v1/market/assets")) });
  const options: Option[] = (assets.data?.assets ?? []).flatMap((a) =>
    a.deposit_enabled ? a.networks.filter((n) => n.deposit_enabled).map((n) => ({ asset: a.asset_code, network: n.network })) : [],
  );
  const [picked, setPicked] = useState<Option | null>(null);
  const current = picked ?? options[0] ?? null;
  const address = useQuery({
    queryKey: ["deposit-address", current?.asset, current?.network],
    enabled: current !== null,
    staleTime: Infinity,
    retry: false,
    queryFn: () =>
      unwrap(walletApi.GET("/v1/wallet/deposit-address", { params: { query: { asset: current!.asset, network: current!.network } } })),
  });
  const [copied, setCopied] = useState(false);

  async function copy(text: string) {
    await navigator.clipboard?.writeText(text).catch(() => undefined);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }

  return (
    <div className="space-y-4">
      <Card title={t("deposit.title")}>
        {options.length === 0 && !assets.isLoading ? (
          <p className="text-sm text-gray-500">{t("deposit.noneOpen")}</p>
        ) : (
          <div className="space-y-4">
            <label className="block space-y-1">
              <span className="text-xs text-gray-400">{t("deposit.assetNetwork")}</span>
              <select
                className="w-full rounded-lg border border-white/10 bg-[#0b0e11] px-3 py-2 text-sm"
                value={current ? `${current.asset}|${current.network}` : ""}
                onChange={(e) => {
                  const [asset = "", network = ""] = e.target.value.split("|");
                  setPicked({ asset, network });
                }}
              >
                {options.map((o) => (
                  <option key={`${o.asset}|${o.network}`} value={`${o.asset}|${o.network}`}>{o.asset} · {o.network}</option>
                ))}
              </select>
            </label>
            <ErrorText text={address.error ? errorText(address.error) : ""} />
            {address.data && (
              <div className="space-y-3">
                <div>
                  <div className="text-xs text-gray-400">{t("deposit.address")}</div>
                  <div className="mt-1 flex flex-wrap items-center gap-2">
                    <code data-testid="deposit-address" className="break-all rounded bg-[#0b0e11] px-2 py-1 font-mono text-sm">{address.data.address}</code>
                    <Button variant="ghost" onClick={() => void copy(address.data.address)}>{copied ? t("deposit.copied") : t("common.copy")}</Button>
                  </div>
                </div>
                <dl className="grid grid-cols-2 gap-2 text-sm">
                  <dt className="text-gray-400">{t("deposit.min")}</dt>
                  <dd className="text-right font-mono">{format(address.data.min_deposit)} {address.data.asset}</dd>
                  <dt className="text-gray-400">{t("deposit.arrival")}</dt>
                  <dd className="text-right">{t("deposit.confirmations", { n: address.data.confirmations })}</dd>
                </dl>
                <p className="rounded-md bg-yellow-500/10 px-3 py-2 text-xs text-yellow-200">
                  {t("deposit.warning", { asset: address.data.asset, network: address.data.network })}
                </p>
                {address.data.network.endsWith("SEPOLIA") && <p className="text-xs text-gray-500">{t("deposit.testnet")}</p>}
              </div>
            )}
          </div>
        )}
      </Card>
      <DepositHistory />
    </div>
  );
}

function DepositHistory() {
  const { t } = useTranslation();
  const deposits = useInfiniteQuery({
    queryKey: ["deposits"],
    initialPageParam: "",
    queryFn: ({ pageParam }) =>
      unwrap(walletApi.GET("/v1/wallet/deposits", { params: { query: { limit: 20, cursor: pageParam || undefined } } })),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    // Confirmations have no push of their own: poll while some are pending.
    refetchInterval: (q) =>
      q.state.data?.pages.some((p) => p.items.some((d) => ["DETECTED", "CONFIRMING", "CONFIRMED"].includes(d.status))) ? 15000 : false,
  });
  const items = deposits.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <Card title={t("deposit.history")}>
      <ErrorText text={deposits.error ? errorText(deposits.error) : ""} />
      {items.length === 0 ? (
        <p className="text-sm text-gray-500">{t("common.none")}</p>
      ) : (
        <ul className="divide-y divide-white/5 text-sm">
          {items.map((d) => {
            const link = explorers[d.network];
            const pending = d.status === "DETECTED" || d.status === "CONFIRMING";
            return (
              <li key={d.id} data-testid="deposit-row" data-status={d.status} className="flex items-start justify-between gap-2 py-2">
                <div className="min-w-0">
                  <div className="font-mono">
                    {d.asset ? `${format(d.amount)} ${d.asset}` : t("deposit.unknownToken")}
                  </div>
                  <div className="text-xs text-gray-500">
                    {d.network} · {link ? <a className="underline" href={link + d.tx_hash} target="_blank" rel="noreferrer">{shortHash(d.tx_hash)}</a> : shortHash(d.tx_hash)}
                  </div>
                  <div className="text-xs text-gray-500"><Time value={d.detected_at} /></div>
                </div>
                <div className="shrink-0 text-right">
                  <Badge tone={statusTone(d.status)}>{t(`deposit.status.${d.status}`)}</Badge>
                  {pending && (
                    <div className="mt-1 text-xs text-gray-400">{t("deposit.progress", { n: d.confirmations, total: d.required_confirmations })}</div>
                  )}
                  {d.reason && <div className="mt-1 text-xs text-red-300">{t(`deposit.reason.${d.reason}`)}</div>}
                </div>
              </li>
            );
          })}
        </ul>
      )}
      {deposits.hasNextPage && <Button variant="ghost" className="mt-3" onClick={() => deposits.fetchNextPage()}>{t("common.more")}</Button>}
    </Card>
  );
}
