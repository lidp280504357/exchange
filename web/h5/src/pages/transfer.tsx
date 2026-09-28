import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { accountApi, marketApi, unwrap } from "../api/client";
import { Badge, Button, Card, ErrorText, Field, Notice, Time } from "../components/ui";
import { codeText, errorText } from "../i18n";
import { checkAmount, compare, format } from "../lib/decimal";
import { useBalances } from "./assets";

export function TransferPage() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const assets = useQuery({ queryKey: ["assets"], queryFn: () => unwrap(marketApi.GET("/v1/market/assets")) });
  const balances = useBalances();
  const [asset, setAsset] = useState("USDT");
  const [from, setFrom] = useState<"SPOT" | "FUTURES">("SPOT");
  const [amount, setAmount] = useState("");
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const [busy, setBusy] = useState(false);
  // One key per intended transfer: a retry after a network error reuses
  // it, so the transfer happens once (§7.1).
  const key = useRef(crypto.randomUUID());

  const to = from === "SPOT" ? "FUTURES" : "SPOT";
  const decimals = assets.data?.assets.find((a) => a.asset_code === asset)?.decimals ?? 0;
  const available = balances.data?.balances.find((b) => b.account_type === from && b.asset === asset)?.available ?? "0";
  const check = amount ? checkAmount(amount, decimals) : "ok";
  const tooMuch = check === "ok" && amount !== "" && compare(amount.trim(), available) > 0;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setDone("");
    try {
      await unwrap(
        accountApi.POST("/v1/account/transfers", {
          params: { header: { "Idempotency-Key": key.current } },
          body: { asset, amount: amount.trim(), from_account_type: from, to_account_type: to },
        }),
      );
      setDone(t("transfer.done"));
      setAmount("");
      key.current = crypto.randomUUID();
      void qc.invalidateQueries({ queryKey: ["balances"] });
      void qc.invalidateQueries({ queryKey: ["transfers"] });
      void qc.invalidateQueries({ queryKey: ["ledger"] });
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }

  const amountError =
    check === "format" ? t("errors.format") : check === "zero" ? t("errors.zero") : check === "precision" ? t("errors.precision", { n: decimals }) : tooMuch ? t("errors.LEDGER_INSUFFICIENT_BALANCE") : "";

  return (
    <div className="space-y-4">
      <Card title={t("transfer.title")}>
        <form className="space-y-3" onSubmit={submit}>
          <div className="flex items-end gap-2">
            <div className="flex-1 rounded-lg border border-white/10 p-2 text-sm">
              <div className="text-xs text-gray-500">{t("transfer.from")}</div>{t(from === "SPOT" ? "assets.spot" : "assets.futures")}
            </div>
            <Button type="button" variant="ghost" onClick={() => setFrom(to)} aria-label={t("transfer.swap")}>⇄</Button>
            <div className="flex-1 rounded-lg border border-white/10 p-2 text-sm">
              <div className="text-xs text-gray-500">{t("transfer.to")}</div>{t(to === "SPOT" ? "assets.spot" : "assets.futures")}
            </div>
          </div>
          <label className="block space-y-1">
            <span className="text-xs text-gray-400">{t("assets.asset")}</span>
            <select className="w-full rounded-lg border border-white/10 bg-[#0b0e11] px-3 py-2 text-sm" value={asset} onChange={(e) => setAsset(e.target.value)}>
              {(assets.data?.assets ?? []).map((a) => <option key={a.asset_code} value={a.asset_code}>{a.asset_code}</option>)}
            </select>
          </label>
          <Field
            label={`${t("transfer.amount")} · ${t("assets.available")} ${format(available)} ${asset}`}
            inputMode="decimal"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            hint={amountError}
          />
          <div className="flex gap-2">
            <Button type="button" variant="ghost" onClick={() => setAmount(available)}>{t("transfer.max")}</Button>
            <Button className="flex-1" disabled={busy || !amount || check !== "ok" || tooMuch}>{t("common.confirm")}</Button>
          </div>
          <ErrorText text={error} />
          <Notice text={done} />
        </form>
      </Card>
      <TransferHistory />
    </div>
  );
}

function TransferHistory() {
  const { t } = useTranslation();
  const list = useInfiniteQuery({
    queryKey: ["transfers"],
    initialPageParam: "",
    queryFn: ({ pageParam }) => unwrap(accountApi.GET("/v1/account/transfers", { params: { query: { limit: 20, cursor: pageParam || undefined } } })),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <Card title={t("transfer.history")}>
      {items.length === 0 ? <p className="text-sm text-gray-500">{t("common.none")}</p> : (
        <ul className="divide-y divide-white/5 text-sm">
          {items.map((tr) => (
            <li key={tr.transfer_id} className="flex items-center justify-between py-2">
              <div>
                <div>{codeText(tr.from_account_type)} → {codeText(tr.to_account_type)}</div>
                <div className="text-xs text-gray-500"><Time value={tr.created_at} /></div>
              </div>
              <div className="text-right">
                <div className="font-mono">{format(tr.amount)} {tr.asset}</div>
                <Badge tone={tr.status === "COMPLETED" ? "green" : "red"}>{t(`transfer.status.${tr.status}`)}</Badge>
              </div>
            </li>
          ))}
        </ul>
      )}
      {list.hasNextPage && <Button variant="ghost" className="mt-3" onClick={() => list.fetchNextPage()}>{t("common.more")}</Button>}
    </Card>
  );
}
