import { ApiError, dec } from "@exchange/core";
import { adminApi, adminData, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, Input, Segmented, Select, Skeleton } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Search } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";
import { errorToast, lastFour } from "../../kit/actions";
import { Check } from "../../kit/Check";
import { EnumBadge } from "../../kit/enums";
import { IdText, Num, useOpenUser } from "../../kit/format";
import { FundAction, type Approval } from "../../kit/funds";
import { stagger } from "../../kit/motion";
import { Card, Page } from "../../kit/Page";
import { useConsoleSettings } from "../../live";
import { useInstrumentConfig } from "../instruments/config";
import { ApprovalsTable, useApprovals } from "./approvalsTable";
import { ModeBanner } from "./ModeBanner";

type UserView = AdminSchemas["UserView"];

/**
 * Adjustments (design 2026-10-02 §2, §4.1): credit or debit a user's spot
 * balance. In single-person mode within the limits the ledger books it at
 * once and the page shows the journal and its two lines; otherwise it
 * waits for a second administrator. ?uid=<id> picks the user.
 */
export default function Adjustments({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const [params] = useSearchParams();
  const [userId, setUserId] = useState(params.get("uid") ?? "");
  const [last, setLast] = useState<Approval | null>(null);
  const list = useApprovals("");
  return (
    <Page title={t("admin.nav.adjustments")} help={t("admin.funds.adjustHelp")}>
      <ModeBanner className="stagger" />
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)]">
        <Card title={t("admin.funds.newAdjustment")} className="stagger" style={stagger(1)}>
          <AdjustForm userId={userId} onUser={setUserId} onDone={setLast} />
        </Card>
        <Card title={t("admin.funds.outcome")} className="stagger" style={stagger(2)}>
          {last ? <Outcome a={last} /> : <p className="py-6 text-center text-sm text-fg-3">{t("admin.funds.noOutcome")}</p>}
        </Card>
      </div>
      <Card title={t("admin.funds.recent")} className="stagger" style={stagger(3)}>
        <ApprovalsTable admin={admin} list={list} />
      </Card>
    </Page>
  );
}

type Account = "SPOT" | "FUTURES";

/** AdjustForm credits or debits a user's spot or futures balance; without onUser the user is fixed (the user's page). */
export function AdjustForm({ userId, onUser, onDone }: { userId: string; onUser?: (id: string) => void; onDone: (a: Approval) => void }) {
  const { t } = useTranslation();
  const settings = useConsoleSettings().data;
  const [query, setQuery] = useState(userId);
  const [direction, setDirection] = useState<"credit" | "debit">("credit");
  const [account, setAccount] = useState<Account>("SPOT");
  const [asset, setAsset] = useState("USDT");
  // The assets the reference data knows: a misspelt one cannot become a
  // FAILED request (the C1 review). Typed in only while they cannot be read.
  const config = useInstrumentConfig();
  const assets = config.data?.assets.map((x) => x.asset_code).sort() ?? [];
  const [amount, setAmount] = useState("");
  const [reference, setReference] = useState("");
  const user = useQuery({
    queryKey: ["admin", "user", userId],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/users/lookup", { params: { query: { q: userId } } })),
    enabled: userId !== "",
    retry: false,
  });
  const find = async () => {
    const q = query.trim();
    if (!q || !onUser) return;
    try {
      onUser(adminData(await adminApi.GET("/admin/v1/users/lookup", { params: { query: { q } } })).user.id);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) errorToast(err, t("admin.search.notFound", { q }));
      else errorToast(err);
    }
  };
  const a = amount.trim();
  const amountOk = dec.isDecimal(a) && dec.gt(a, "0");
  const signed = direction === "credit" ? a : `-${a}`;
  const ready = !!user.data && amountOk && asset.trim() !== "";
  const balance = user.data?.balances.find((b) => b.account_type === account && b.asset === asset.trim().toUpperCase());
  return (
    <div className="flex flex-col gap-4">
      {onUser && (
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.funds.user")}
          <Input
            value={query}
            onValueChange={setQuery}
            onKeyDown={(e) => e.key === "Enter" && void find()}
            prefix={<Search size={16} className="text-fg-3" />}
            suffix={
              <Button size="sm" variant="ghost" className="mr-1" onClick={() => void find()}>
                {t("admin.users.lookup")}
              </Button>
            }
            placeholder={t("admin.users.lookupHint")}
            aria-label={t("admin.funds.user")}
          />
        </label>
      )}
      {userId && <UserLine view={user.data} loading={user.isPending} error={user.isError} asset={asset.trim().toUpperCase()} account={account} />}
      <label className="flex flex-col gap-1.5 text-sm text-fg-2">
        {t("admin.money.account")}
        <Segmented
          size="md"
          value={account}
          onValueChange={(v) => setAccount(v as Account)}
          items={[
            { value: "SPOT", label: t("admin.enum.accountType.SPOT") },
            { value: "FUTURES", label: t("admin.enum.accountType.FUTURES") },
          ]}
        />
      </label>
      <div className="grid gap-3 sm:grid-cols-[auto_minmax(0,1fr)_minmax(0,1.4fr)]">
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.funds.direction")}
          <Segmented
            size="md"
            value={direction}
            onValueChange={(v) => setDirection(v as "credit" | "debit")}
            items={[
              { value: "credit", label: t("admin.funds.credit") },
              { value: "debit", label: t("admin.funds.debit") },
            ]}
          />
        </label>
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.common.asset")}
          {config.isError ? (
            <Input value={asset} onValueChange={(v) => setAsset(v.toUpperCase())} placeholder="USDT" aria-label={t("admin.common.asset")} />
          ) : (
            <Select
              value={asset}
              onValueChange={setAsset}
              options={(assets.length > 0 ? assets : ["USDT"]).map((code) => ({ value: code, label: code }))}
              aria-label={t("admin.common.asset")}
            />
          )}
        </label>
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.common.amount")}
          <Input
            value={amount}
            onValueChange={setAmount}
            inputMode="decimal"
            placeholder="100"
            unit={asset.trim().toUpperCase() || undefined}
            error={amount !== "" && !amountOk ? t("admin.funds.invalidAmount") : undefined}
            aria-label={t("admin.common.amount")}
          />
        </label>
      </div>
      <label className="flex flex-col gap-1.5 text-sm text-fg-2">
        {t("admin.funds.reference")}
        <Input value={reference} onValueChange={setReference} maxLength={64} placeholder={t("admin.funds.referenceHint")} />
      </label>
      {direction === "debit" && balance && amountOk && dec.gt(a, balance.available) && (
        <p className="text-sm text-warn-strong">{t("admin.funds.overBalance", { available: balance.available, asset: balance.asset })}</p>
      )}
      <FundAction
        trigger={(open) => (
          <Button disabled={!ready} onClick={open} icon={<ArrowRight size={16} />} className="self-start">
            {settings?.two_person_approval ? t("admin.funds.submitRequest") : t("admin.funds.submitDirect")}
          </Button>
        )}
        danger={direction === "debit"}
        title={t(direction === "credit" ? "admin.funds.creditTitle" : "admin.funds.debitTitle")}
        description={settings && !settings.two_person_approval ? t("admin.funds.directHint") : t("admin.funds.requestHint")}
        target={
          <span className="inline-flex flex-wrap items-center gap-2">
            <span className="font-mono text-xs">{userId}</span>
            <EnumBadge group="accountType" code={account} />
            <Num value={signed} unit={asset.trim().toUpperCase()} signed />
          </span>
        }
        // A debit is confirmed by typing its amount (design §2), a credit by
        // the user ID's last four.
        confirmWord={direction === "debit" ? a : lastFour(userId)}
        run={async (reason, key) =>
          adminData(
            await adminApi.POST("/admin/v1/users/{id}/adjustments", {
              params: { path: { id: userId }, header: { "Idempotency-Key": key } },
              body: {
                account_type: account, asset: asset.trim().toUpperCase(), amount: signed, reason,
                ...(reference.trim() ? { reference: reference.trim() } : {}),
              },
            }),
          )
        }
        onDone={(op) => {
          onDone(op);
          setAmount("");
          setReference("");
          void user.refetch();
        }}
      >
        {account === "FUTURES" && direction === "debit" && amountOk && asset.trim().toUpperCase() === "USDT" && <CrossMarginNote userId={userId} debit={a} />}
      </FundAction>
    </div>
  );
}

/**
 * CrossMarginNote says what a debit of the FUTURES balance leaves of the
 * user's cross margin: the equity drops at once, and the next round of the
 * margin monitor may liquidate the cross positions (C5.5 ⑧).
 */
function CrossMarginNote({ userId, debit }: { userId: string; debit: string }) {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: ["admin", "user", userId, "futures-margin", debit],
    queryFn: async () =>
      adminData(await adminApi.GET("/admin/v1/users/{id}/futures-margin", { params: { path: { id: userId }, query: { debit } } })),
    retry: false,
  });
  if (q.isPending) return <Skeleton className="h-12 w-full" />;
  if (q.isError) return <p className="text-sm text-warn-strong">{t("admin.margin.unknown")}</p>;
  const m = q.data;
  if (m.positions === 0) return <p className="text-sm text-fg-3">{t("admin.margin.noCross")}</p>;
  if (m.unmeasured) return <p className="text-sm text-warn-strong">{t("admin.margin.unmeasured")}</p>;
  const tone = m.state_after === "LIQUIDATE" ? "border-danger text-danger-strong" : m.state_after === "WARNING" ? "border-warn text-warn-strong" : "border-line-1 text-fg-2";
  return (
    <div className={`flex flex-col gap-1 rounded-2 border px-3 py-2 text-sm ${tone}`} data-testid="futures-margin">
      <span className="flex flex-wrap items-center gap-x-2">
        {t("admin.margin.equity")} <Num value={m.equity} unit={m.asset} /> → {t("admin.margin.after")} <Num value={m.equity_after} unit={m.asset} />
        <span className="text-fg-3">
          · {t("admin.margin.maintenance")} <Num value={m.maintenance} />
        </span>
      </span>
      <span className="font-medium">{t(`admin.margin.state.${m.state_after}`)}</span>
    </div>
  );
}

/** UserLine is the chosen account with its balance of the asset in the account chosen. */
function UserLine({ view, loading, error, asset, account }: { view?: UserView; loading: boolean; error: boolean; asset: string; account: Account }) {
  const { t } = useTranslation();
  const open = useOpenUser();
  if (loading) return <Skeleton className="h-12 w-full" />;
  if (error || !view) return <p className="text-sm text-danger-strong">{t("admin.funds.noUser")}</p>;
  const b = view.balances.find((x) => x.account_type === account && x.asset === asset);
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-2 border border-line-1 bg-bg-0 px-3 py-2.5 text-sm">
      <IdText value={view.user.id} chars={13} />
      <EnumBadge group="userStatus" code={view.user.status} />
      <span className="text-fg-3">
        {account === "SPOT" ? t("admin.funds.spotBalance", { asset }) : t("admin.money.futuresBalance", { asset })}
      </span>
      <Num value={b?.available ?? "0"} unit={asset} />
      {b && dec.gt(b.frozen, "0") && (
        <span className="text-xs text-fg-3">
          {t("admin.users.frozen")} <Num value={b.frozen} />
        </span>
      )}
      <button type="button" onClick={() => open(view.user.id)} className="ml-auto text-info-strong hover:underline">
        {t("admin.common.openUser")}
      </button>
    </div>
  );
}

/** Outcome shows the last operation: booked with its journal and two lines, refused, or waiting. */
export function Outcome({ a }: { a: Approval }) {
  const { t } = useTranslation();
  const open = useOpenUser();
  const p = a.payload as Record<string, string>;
  const negated = dec.isDecimal(p.amount ?? "") ? dec.neg(p.amount!) : p.amount;
  return (
    <div className="flex flex-col gap-3 text-sm animate-rise">
      <div className="flex items-center gap-2">
        {a.status === "EXECUTED" && <Check size={22} />}
        <EnumBadge group="approvalStatus" code={a.status} />
        <span className="text-fg-3">{a.mode === "SINGLE" ? t("admin.funds.single") : t(`admin.funds.escalation.${a.escalation || "REQUESTED"}`)}</span>
      </div>
      {a.status === "EXECUTED" && (
        <>
          <div className="text-fg-3">
            {t("admin.funds.journalLabel")} <IdText value={a.journal_id} chars={13} />
          </div>
          <table className="w-full text-left">
            <thead className="text-xs text-fg-3">
              <tr>
                <th className="py-1 font-normal">{t("admin.funds.entryAccount")}</th>
                <th className="py-1 text-right font-normal">{t("admin.common.amount")}</th>
              </tr>
            </thead>
            <tbody className="font-mono text-xs">
              <tr className="border-t border-line-1">
                <td className="py-1.5">
                  {p.account_type === "FUTURES"
                    ? t("admin.money.entryUserFutures", { id: (p.user_id ?? "").slice(0, 8) })
                    : t("admin.funds.entryUser", { id: (p.user_id ?? "").slice(0, 8) })}
                </td>
                <td className="py-1.5 text-right">
                  <Num value={p.amount} unit={p.asset} signed />
                </td>
              </tr>
              <tr className="border-t border-line-1">
                <td className="py-1.5">{t("admin.funds.entrySystem")}</td>
                <td className="py-1.5 text-right">
                  <Num value={negated} unit={p.asset} signed />
                </td>
              </tr>
            </tbody>
          </table>
        </>
      )}
      {a.status === "FAILED" && <p className="text-danger-strong">{a.result}</p>}
      {a.status === "PENDING" && <p className="text-fg-2">{t("admin.funds.waitingHint")}</p>}
      {a.value_usdt && (
        <div className="text-fg-3">
          {t("admin.funds.value")} <Num value={a.value_usdt} decimals={2} unit="USDT" />
        </div>
      )}
      {p.user_id && (
        <Badge tone="neutral" className="self-start">
          <button type="button" onClick={() => open(p.user_id!)} className="hover:underline">
            {t("admin.funds.checkBalance")}
          </button>
        </Badge>
      )}
    </div>
  );
}
