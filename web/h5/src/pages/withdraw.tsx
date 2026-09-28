import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { marketApi, unwrap, walletApi } from "../api/client";
import { StepUp } from "../components/StepUp";
import { Badge, Button, Card, ErrorText, Field, Notice, Time } from "../components/ui";
import { codeText, errorText } from "../i18n";
import { checkAmount, compare, format } from "../lib/decimal";
import { useBalances } from "./assets";

const explorers: Record<string, string> = {
  "ETH-SEPOLIA": "https://sepolia.etherscan.io/tx/",
};

type Option = { asset: string; network: string; decimals: number; min: string; fee: string };

function tone(status: string): "gray" | "green" | "yellow" | "red" {
  if (status === "CONFIRMED") return "green";
  if (["REJECTED", "CANCELED", "FAILED"].includes(status)) return "red";
  return "yellow";
}

function short(h: string): string {
  return h.length > 18 ? `${h.slice(0, 10)}…${h.slice(-6)}` : h;
}

// A pending action that needs a step-up first.
type Pending = { kind: "address" } | { kind: "withdraw" };

export function WithdrawPage() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const assets = useQuery({ queryKey: ["assets"], queryFn: () => unwrap(marketApi.GET("/v1/market/assets")) });
  const balances = useBalances();
  const book = useQuery({ queryKey: ["withdraw-addresses"], queryFn: () => unwrap(walletApi.GET("/v1/wallet/withdraw-addresses")) });
  const options: Option[] = (assets.data?.assets ?? []).flatMap((a) =>
    a.withdraw_enabled
      ? a.networks.filter((n) => n.withdraw_enabled).map((n) => ({ asset: a.asset_code, network: n.network, decimals: a.decimals, min: n.min_withdraw, fee: n.withdraw_fee }))
      : [],
  );
  const [picked, setPicked] = useState<string>("");
  const current = options.find((o) => `${o.asset}|${o.network}` === picked) ?? options[0];
  const [address, setAddress] = useState("");
  const [amount, setAmount] = useState("");
  const [newAddress, setNewAddress] = useState("");
  const [label, setLabel] = useState("");
  const [pending, setPending] = useState<Pending | null>(null);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const key = useRef(crypto.randomUUID());

  const entries = (book.data?.items ?? []).filter((e) => !current || e.network === current.network);
  const available = balances.data?.balances.find((b) => b.account_type === "SPOT" && b.asset === current?.asset)?.available ?? "0";
  const check = amount && current ? checkAmount(amount, current.decimals) : "ok";
  const amountError =
    check === "format" ? t("errors.format") : check === "zero" ? t("errors.zero") : check === "precision" ? t("errors.precision", { n: current?.decimals ?? 0 }) : "";

  async function withToken(token: string) {
    const action = pending;
    setPending(null);
    setError("");
    setDone("");
    try {
      if (action?.kind === "address" && current) {
        await unwrap(walletApi.POST("/v1/wallet/withdraw-addresses", {
          params: { header: { "X-Step-Up-Token": token } },
          body: { network: current.network, address: newAddress.trim(), label: label.trim() },
        }));
        setNewAddress("");
        setLabel("");
        setDone(t("withdraw.addressAdded"));
        void qc.invalidateQueries({ queryKey: ["withdraw-addresses"] });
      } else if (action?.kind === "withdraw" && current) {
        await unwrap(walletApi.POST("/v1/wallet/withdrawals", {
          params: { header: { "X-Step-Up-Token": token, "Idempotency-Key": key.current } },
          body: { asset: current.asset, network: current.network, address, amount: amount.trim() },
        }));
        key.current = crypto.randomUUID();
        setAmount("");
        setDone(t("withdraw.requested"));
        void qc.invalidateQueries({ queryKey: ["withdrawals"] });
        void qc.invalidateQueries({ queryKey: ["balances"] });
      }
    } catch (e) {
      setError(errorText(e));
    }
  }

  async function remove(id: string) {
    setError("");
    try {
      await unwrap(walletApi.DELETE("/v1/wallet/withdraw-addresses/{id}", { params: { path: { id } } }));
      void qc.invalidateQueries({ queryKey: ["withdraw-addresses"] });
    } catch (e) {
      setError(errorText(e));
    }
  }

  const usable = (iso: string) => Date.parse(iso) <= Date.now();
  const total = current && amount && check === "ok" ? addDecimal(amount.trim(), current.fee) : "";
  const tooMuch = total !== "" && compare(total, available) > 0;

  return (
    <div className="space-y-4">
      <Card title={t("withdraw.title")}>
        {options.length === 0 && !assets.isLoading ? (
          <p className="text-sm text-gray-500">{t("withdraw.noneOpen")}</p>
        ) : (
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault();
              setPending({ kind: "withdraw" });
            }}
          >
            <label className="block space-y-1">
              <span className="text-xs text-gray-400">{t("deposit.assetNetwork")}</span>
              <select
                className="w-full rounded-lg border border-white/10 bg-[#0b0e11] px-3 py-2 text-sm"
                value={current ? `${current.asset}|${current.network}` : ""}
                onChange={(e) => setPicked(e.target.value)}
              >
                {options.map((o) => <option key={`${o.asset}|${o.network}`} value={`${o.asset}|${o.network}`}>{o.asset} · {o.network}</option>)}
              </select>
            </label>
            <label className="block space-y-1">
              <span className="text-xs text-gray-400">{t("withdraw.address")}</span>
              <select
                name="address"
                className="w-full rounded-lg border border-white/10 bg-[#0b0e11] px-3 py-2 text-sm"
                value={address}
                onChange={(e) => setAddress(e.target.value)}
              >
                <option value="">{t("withdraw.pickAddress")}</option>
                {entries.map((e) => (
                  <option key={e.id} value={e.address} disabled={!usable(e.usable_at)}>
                    {(e.label ? `${e.label} · ` : "") + short(e.address)}{usable(e.usable_at) ? "" : ` (${t("withdraw.coolingOff")})`}
                  </option>
                ))}
              </select>
            </label>
            <Field label={t("withdraw.amount")} name="amount" inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)}
              hint={current ? t("withdraw.rules", { min: format(current.min), fee: format(current.fee), asset: current.asset, available: format(available) }) : ""} />
            <ErrorText text={amountError || (tooMuch ? t("errors.LEDGER_INSUFFICIENT_BALANCE") : "")} />
            {total && <p className="text-sm text-gray-400">{t("withdraw.total", { total: format(total), asset: current?.asset })}</p>}
            <Button type="submit" className="w-full" disabled={!address || !amount || check !== "ok" || tooMuch}>{t("withdraw.submit")}</Button>
          </form>
        )}
        <div className="mt-3 space-y-2">
          <ErrorText text={error} />
          <Notice text={done} />
        </div>
      </Card>

      <Card title={t("withdraw.book")}>
        <ul className="divide-y divide-white/5 text-sm">
          {(book.data?.items ?? []).map((e) => (
            <li key={e.id} className="flex items-center justify-between gap-2 py-2">
              <div className="min-w-0">
                <div className="truncate font-mono">{e.label && <span className="mr-2 font-sans text-gray-300">{e.label}</span>}{e.address}</div>
                <div className="text-xs text-gray-500">{e.network} · {usable(e.usable_at) ? t("withdraw.usable") : t("withdraw.usableAt", { at: new Date(e.usable_at).toLocaleString() })}</div>
              </div>
              <Button variant="ghost" onClick={() => void remove(e.id)}>{t("withdraw.remove")}</Button>
            </li>
          ))}
        </ul>
        <form
          className="mt-3 space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            setPending({ kind: "address" });
          }}
        >
          <Field label={t("withdraw.newAddress")} name="new_address" value={newAddress} onChange={(e) => setNewAddress(e.target.value)} />
          <Field label={t("withdraw.label")} name="label" maxLength={50} value={label} onChange={(e) => setLabel(e.target.value)} />
          <p className="text-xs text-gray-500">{t("withdraw.coolingOffHint")}</p>
          <Button type="submit" variant="ghost" disabled={!/^0x[0-9a-fA-F]{40}$/.test(newAddress.trim()) || !current}>{t("withdraw.addAddress")}</Button>
        </form>
      </Card>

      <WithdrawalHistory />
      {pending && <StepUp onToken={(tok) => void withToken(tok)} onCancel={() => setPending(null)} />}
    </div>
  );
}

// addDecimal adds two non-negative decimal strings exactly.
function addDecimal(a: string, b: string): string {
  const [ai = "0", af = ""] = a.split(".");
  const [bi = "0", bf = ""] = b.split(".");
  const n = Math.max(af.length, bf.length);
  const sum = BigInt(ai + af.padEnd(n, "0")) + BigInt(bi + bf.padEnd(n, "0"));
  const s = sum.toString().padStart(n + 1, "0");
  return n === 0 ? s : `${s.slice(0, s.length - n)}.${s.slice(s.length - n)}`.replace(/\.?0+$/, "");
}

function WithdrawalHistory() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [error, setError] = useState("");
  const list = useInfiniteQuery({
    queryKey: ["withdrawals"],
    initialPageParam: "",
    queryFn: ({ pageParam }) => unwrap(walletApi.GET("/v1/wallet/withdrawals", { params: { query: { limit: 20, cursor: pageParam || undefined } } })),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    refetchInterval: (q) =>
      q.state.data?.pages.some((p) => p.items.some((w) => ["APPROVED", "SIGNING", "BROADCAST", "CONFIRMING"].includes(w.status))) ? 15000 : false,
  });
  async function cancel(id: string) {
    setError("");
    try {
      await unwrap(walletApi.DELETE("/v1/wallet/withdrawals/{id}", { params: { path: { id } } }));
      void qc.invalidateQueries({ queryKey: ["withdrawals"] });
      void qc.invalidateQueries({ queryKey: ["balances"] });
    } catch (e) {
      setError(errorText(e));
    }
  }
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <Card title={t("withdraw.history")}>
      <ErrorText text={error || (list.error ? errorText(list.error) : "")} />
      {items.length === 0 ? (
        <p className="text-sm text-gray-500">{t("common.none")}</p>
      ) : (
        <ul className="divide-y divide-white/5 text-sm">
          {items.map((w) => (
            <li key={w.id} data-testid="withdrawal-row" data-status={w.status} className="flex items-start justify-between gap-2 py-2">
              <div className="min-w-0">
                <div className="font-mono">{format(w.amount)} {w.asset} <span className="text-xs text-gray-500">{t("withdraw.feeShort", { fee: format(w.fee) })}</span></div>
                <div className="truncate text-xs text-gray-500">{w.internal ? t("withdraw.internal") : w.network} · {short(w.address)}</div>
                {w.tx_hash && explorers[w.network] && (
                  <a className="text-xs underline" href={explorers[w.network] + w.tx_hash} target="_blank" rel="noreferrer">{short(w.tx_hash)}</a>
                )}
                <div className="text-xs text-gray-500"><Time value={w.created_at} /></div>
              </div>
              <div className="shrink-0 space-y-1 text-right">
                <Badge tone={tone(w.status)}>{codeText(w.status)}</Badge>
                {w.status === "PENDING_REVIEW" && <div className="text-xs text-gray-400">{t("withdraw.review")}</div>}
                {w.status === "CONFIRMING" && <div className="text-xs text-gray-400">{t("deposit.progress", { n: w.confirmations, total: w.required_confirmations })}</div>}
                {w.reject_reason && <div className="text-xs text-red-300">{codeText(w.reject_reason)}</div>}
                {["REQUESTED", "PENDING_REVIEW", "APPROVED"].includes(w.status) && (
                  <Button variant="ghost" onClick={() => void cancel(w.id)}>{t("withdraw.cancel")}</Button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
      {list.hasNextPage && <Button variant="ghost" className="mt-3" onClick={() => list.fetchNextPage()}>{t("common.more")}</Button>}
    </Card>
  );
}
