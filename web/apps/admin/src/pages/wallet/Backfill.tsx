import { dec } from "@exchange/core";
import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, Drawer, Input, KeyValue, Select } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, ShieldAlert, ShieldCheck } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { errorToast, lastFour } from "../../kit/actions";
import { Check } from "../../kit/Check";
import { EnumBadge } from "../../kit/enums";
import { Num, UserCell } from "../../kit/format";
import { FundAction, type Approval } from "../../kit/funds";
import { useConsoleSettings } from "../../live";

type Entry = AdminSchemas["BackfillRequest"];
type CheckResult = { user_id: string; asset: string; unclaimed: boolean; value_usdt: string | null };

const blank: Entry = { network: "", trade_id: "", address: "", tx_hash: "", amount: "" };

/** tidy is an entry as sent: trimmed, the network in capitals. */
const tidy = (e: Entry): Entry => ({
  network: e.network.trim().toUpperCase(), trade_id: e.trade_id.trim(), address: e.address.trim(), tx_hash: e.tx_hash.trim(),
  amount: e.amount.trim(),
});

const same = (a: Entry, b: Entry) => (Object.keys(a) as (keyof Entry)[]).every((k) => a[k] === b[k]);

/**
 * BackfillDrawer books a custodian deposit whose callback was lost (design
 * 2026-10-02 §4.3). The custodian cannot be asked about a trade, so the
 * administrator checks it in the custodian's console first; the console
 * checks the rest (a custodian's network, a user's address, an unknown
 * trade and transfer, the asset's precision) and shows what would be
 * booked. Submitting it is a fund operation: within the single-person
 * limits at once, otherwise a second administrator approves it.
 */
export function BackfillDrawer({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const settings = useConsoleSettings().data;
  const [entry, setEntry] = useState<Entry>(blank);
  const [checked, setChecked] = useState<{ entry: Entry; check: CheckResult } | null>(null);
  const [busy, setBusy] = useState(false);
  const [last, setLast] = useState<Approval | null>(null);
  const networks = useCustodianNetworks();
  const body = tidy(entry);
  const amountOk = dec.isDecimal(body.amount) && dec.gt(body.amount, "0");
  const complete = body.network !== "" && body.trade_id !== "" && body.address !== "" && body.tx_hash !== "" && amountOk;
  const current = checked && same(checked.entry, body) ? checked.check : null;
  const set = (k: keyof Entry) => (v: string) => setEntry((e) => ({ ...e, [k]: v }));
  const check = async () => {
    setBusy(true);
    try {
      setChecked({ entry: body, check: adminData(await adminApi.POST("/admin/v1/deposits/manual/check", { body })) });
    } catch (err) {
      setChecked(null);
      errorToast(err);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Drawer open onOpenChange={(o) => !o && onClose()} title={t("admin.backfill.title")} width={560}>
      <div className="flex flex-col gap-4">
        <div className="flex gap-3 rounded-2 border border-warn/40 bg-warn/10 p-3 text-sm">
          <ShieldAlert size={18} className="mt-0.5 shrink-0 text-warn-strong" />
          <p className="text-fg-2">{t("admin.backfill.help")}</p>
        </div>
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.backfill.network")}
          {networks.length > 0 ? (
            <Select
              value={entry.network || undefined}
              onValueChange={set("network")}
              options={networks.map((n) => ({ value: n, label: n }))}
              placeholder="TRON"
              aria-label={t("admin.backfill.network")}
            />
          ) : (
            <Input value={entry.network} onValueChange={(v) => set("network")(v.toUpperCase())} placeholder="TRON" aria-label={t("admin.backfill.network")} />
          )}
        </label>
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.backfill.tradeId")}
          <Input value={entry.trade_id} onValueChange={set("trade_id")} className="font-mono" aria-label={t("admin.backfill.tradeId")} />
        </label>
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.backfill.address")}
          <Input value={entry.address} onValueChange={set("address")} className="font-mono" aria-label={t("admin.backfill.address")} />
        </label>
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.backfill.txHash")}
          <Input value={entry.tx_hash} onValueChange={set("tx_hash")} className="font-mono" aria-label={t("admin.backfill.txHash")} />
        </label>
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.common.amount")}
          <Input
            value={entry.amount}
            onValueChange={set("amount")}
            inputMode="decimal"
            unit={current?.asset}
            error={entry.amount !== "" && !amountOk ? t("admin.funds.invalidAmount") : undefined}
            aria-label={t("admin.common.amount")}
          />
        </label>
        <Button variant="secondary" className="self-start" disabled={!complete} loading={busy} icon={<ShieldCheck size={16} />} onClick={() => void check()}>
          {t("admin.backfill.check")}
        </Button>
        {checked && !current && <p className="text-sm text-warn-strong">{t("admin.backfill.changed")}</p>}
        {current && (
          <div className="flex flex-col gap-3 rounded-2 border border-line-1 bg-bg-0 p-3 animate-rise">
            <p className="text-sm text-success-strong">{t("admin.backfill.checked")}</p>
            <KeyValue
              density="compact"
              items={[
                { label: t("admin.backfill.user"), value: <UserCell id={current.user_id} /> },
                { label: t("admin.common.amount"), value: <Num value={body.amount} unit={current.asset} /> },
                {
                  label: t("admin.backfill.value"),
                  value: current.value_usdt ? <Num value={current.value_usdt} decimals={2} unit="USDT" /> : <Badge tone="warn">{t("admin.backfill.noPrice")}</Badge>,
                },
              ]}
            />
            {current.unclaimed && <p className="text-sm text-warn-strong">{t("admin.backfill.unclaimed")}</p>}
          </div>
        )}
        <FundAction
          trigger={(open) => (
            <Button disabled={!current} onClick={open} icon={<ArrowRight size={16} />} className="self-start">
              {settings?.two_person_approval ? t("admin.funds.submitRequest") : t("admin.backfill.submit")}
            </Button>
          )}
          danger
          title={t("admin.backfill.submitTitle")}
          description={settings && !settings.two_person_approval ? t("admin.funds.directHint") : t("admin.funds.requestHint")}
          target={
            <span className="flex flex-col gap-1">
              <span className="inline-flex flex-wrap items-center gap-2">
                <Num value={body.amount} unit={current?.asset} /> <span className="text-fg-3">{body.network}</span>
              </span>
              <span className="font-mono text-xs">{body.trade_id}</span>
              <span className="font-mono text-xs">{current?.user_id}</span>
            </span>
          }
          confirmWord={lastFour(body.trade_id)}
          run={async (reason, key) =>
            adminData(await adminApi.POST("/admin/v1/deposits/manual", { params: { header: { "Idempotency-Key": key } }, body: { ...body, reason } }))
          }
          onDone={(a) => {
            setLast(a);
            setEntry(blank);
            setChecked(null);
          }}
        >
          <div className="flex gap-3 rounded-2 border border-danger/40 bg-danger/10 p-3 text-sm" role="alert">
            <ShieldAlert size={18} className="mt-0.5 shrink-0 text-danger-strong" />
            <div>
              <div className="font-semibold text-danger-strong">{t("admin.backfill.notVerified")}</div>
              <p className="mt-1 text-fg-2">{t("admin.backfill.notVerifiedHint")}</p>
            </div>
          </div>
        </FundAction>
        {last && <BackfillOutcome a={last} />}
      </div>
    </Drawer>
  );
}

/** BackfillOutcome is the last backfill: booked as a deposit, refused, or waiting for a second administrator. */
function BackfillOutcome({ a }: { a: Approval }) {
  const { t } = useTranslation();
  return (
    <section className="flex flex-col gap-2 border-t border-line-1 pt-4 text-sm animate-rise">
      <h3 className="font-semibold">{t("admin.backfill.outcome")}</h3>
      <div className="flex items-center gap-2">
        {a.status === "EXECUTED" && <Check size={22} />}
        <EnumBadge group="approvalStatus" code={a.status} />
        <span className="text-fg-3">{a.mode === "SINGLE" ? t("admin.funds.single") : t(`admin.funds.escalation.${a.escalation || "REQUESTED"}`)}</span>
      </div>
      {a.status === "EXECUTED" && <p className="text-fg-2">{t("admin.backfill.done", { id: a.result.replace(/^deposit /, "") })}</p>}
      {a.status === "FAILED" && <p className="text-danger-strong">{a.result}</p>}
      {a.status === "PENDING" && <p className="text-fg-2">{t("admin.funds.waitingHint")}</p>}
    </section>
  );
}

/** useCustodianNetworks lists the networks whose custodian is not the chain (empty when it cannot be read). */
function useCustodianNetworks(): string[] {
  const q = useQuery({ queryKey: ["admin", "custody"], queryFn: async () => adminData(await adminApi.GET("/admin/v1/custody")) });
  const out = new Set<string>();
  for (const c of q.data?.coins ?? []) for (const n of c.networks) out.add(n.network);
  return [...out].sort();
}
