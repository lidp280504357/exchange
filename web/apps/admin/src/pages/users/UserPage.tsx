import { errorText } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Avatar, DataTable, ErrorState, KeyValue, Skeleton, Tabs, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { ChevronLeft } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useParams, useSearchParams } from "react-router";
import { EnumBadge, EnumText } from "../../kit/enums";
import { IdText, Num, TimeText } from "../../kit/format";
import type { Approval } from "../../kit/funds";
import { stagger } from "../../kit/motion";
import { Card } from "../../kit/Page";
import { AdjustForm, Outcome } from "../funds/Adjustments";
import { AuditTable, DepositsTable, OrdersTable, TradesTable, useAudit, useDeposits, useOrders, useTrades } from "../records/tables";
import { useWithdrawals, WithdrawalsTable } from "../wallet/withdrawalTable";
import { UserActions } from "./actions";
import { Notes, TagChips, TagsEditor } from "./NotesTags";

type UserSummary = AdminSchemas["UserSummary"];
type Balance = AdminSchemas["UserView"]["balances"][number];

const right: DataColumnMeta = { align: "right" };

/**
 * UserPage is a user's page (design 2026-10-02 §4.1): the account at the
 * side with what the role may do to it, the records in tabs (?tab=) on the
 * right. /users/<id>.
 */
export default function UserPage({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const { id = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const detail = useQuery({
    queryKey: ["admin", "user", id, "detail"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/users/{id}", { params: { path: { id } } })),
    retry: false,
  });
  const tabs = [
    "profile", "balances", "orders", "trades",
    ...(can(admin, "withdrawals.read") ? ["deposits", "withdrawals"] : []),
    "notes",
    ...(can(admin, "audit.read") ? ["audit"] : []),
  ];
  const tab = tabs.includes(params.get("tab") ?? "") ? params.get("tab")! : "profile";
  const setTab = (v: string) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set("tab", v);
        return next;
      },
      { replace: true },
    );
  if (detail.isError) return <ErrorState message={errorText(detail.error)} onRetry={() => void detail.refetch()} />;
  const u = detail.data;
  return (
    <div className="flex flex-col gap-4">
      <Link to="/users" className="inline-flex w-fit items-center gap-1 text-sm text-fg-3 hover:text-fg-1">
        <ChevronLeft size={16} />
        {t("admin.user.back")}
      </Link>
      <div className="grid gap-4 xl:grid-cols-[320px_minmax(0,1fr)]">
        <aside className="flex flex-col gap-4">
          <Card className="stagger">{u ? <Summary user={u} /> : <Skeleton className="h-48 w-full" />}</Card>
          {u && (can(admin, "users.status") || can(admin, "orders.cancel")) && (
            <Card title={t("admin.user.actions")} className="stagger" style={stagger(1)}>
              <UserActions admin={admin} userId={u.id} status={u.status} />
            </Card>
          )}
        </aside>
        <section className="card stagger min-w-0 p-4" style={stagger(2)}>
          <Tabs items={tabs.map((k) => ({ value: k, label: t(`admin.user.tabs.${k}`) }))} value={tab} onValueChange={setTab} />
          <div key={tab} className="mt-4 animate-rise">
            {tab === "profile" && (u ? <Profile user={u} /> : <Skeleton className="h-32 w-full" />)}
            {tab === "balances" && <Balances admin={admin} userId={id} />}
            {tab === "orders" && <UserOrders userId={id} />}
            {tab === "trades" && <UserTrades userId={id} />}
            {tab === "deposits" && <UserDeposits userId={id} />}
            {tab === "withdrawals" && <UserWithdrawals userId={id} />}
            {tab === "notes" && (
              <div className="flex flex-col gap-6">
                <div>
                  <h3 className="mb-2 text-sm font-semibold">{t("admin.user.tags")}</h3>
                  <TagsEditor admin={admin} userId={id} tags={u?.tags ?? []} />
                </div>
                <div>
                  <h3 className="mb-2 text-sm font-semibold">{t("admin.user.notes")}</h3>
                  <Notes admin={admin} userId={id} />
                </div>
              </div>
            )}
            {tab === "audit" && <UserAudit userId={id} />}
          </div>
        </section>
      </div>
    </div>
  );
}

/** Summary is the account at a glance. */
function Summary({ user }: { user: UserSummary }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-3">
        <Avatar name={user.id.replace(/-/g, "").slice(-2)} size={48} />
        <div className="min-w-0">
          <div className="text-xs text-fg-3">{t("admin.user.uid")}</div>
          <IdText value={user.id} chars={13} className="text-sm" />
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <EnumBadge group="userStatus" code={user.status} />
        <TagChips tags={user.tags} />
      </div>
      <KeyValue
        items={[
          { label: t("admin.user.registered"), value: <TimeText value={user.created_at} /> },
          { label: t("admin.user.profileRows.region"), value: user.region || "—" },
          { label: t("admin.user.profileRows.kyc"), value: user.kyc_level },
        ]}
      />
    </div>
  );
}

function Profile({ user }: { user: UserSummary }) {
  const { t } = useTranslation();
  return (
    <KeyValue
      layout="grid"
      columns={3}
      items={[
        { label: t("admin.users.id"), value: <span className="font-mono text-xs">{user.id}</span>, copy: user.id },
        { label: t("admin.user.profileRows.status"), value: <EnumBadge group="userStatus" code={user.status} /> },
        { label: t("admin.user.registered"), value: <TimeText value={user.created_at} /> },
        { label: t("admin.user.profileRows.region"), value: user.region || "—" },
        { label: t("admin.user.profileRows.language"), value: user.language || "—" },
        { label: t("admin.user.timezone"), value: user.timezone || t("admin.user.browserZone") },
        { label: t("admin.user.profileRows.kyc"), value: user.kyc_level },
      ]}
    />
  );
}

/** Balances lists every account and asset with what is frozen; an adjustment is made right here. */
function Balances({ admin, userId }: { admin: Admin; userId: string }) {
  const { t } = useTranslation();
  const [last, setLast] = useState<Approval | null>(null);
  const view = useQuery({
    queryKey: ["admin", "user", userId],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/users/lookup", { params: { query: { q: userId } } })),
  });
  const columns = useMemo<ColumnDef<Balance, unknown>[]>(
    () => [
      { id: "account", header: t("admin.users.account"), cell: ({ row }) => <EnumText group="accountType" code={row.original.account_type} /> },
      { accessorKey: "asset", header: t("admin.common.asset") },
      { id: "available", header: t("admin.users.available"), meta: right, cell: ({ row }) => <Num value={row.original.available} /> },
      { id: "frozen", header: t("admin.users.frozen"), meta: right, cell: ({ row }) => <Num value={row.original.frozen} /> },
    ],
    [t],
  );
  return (
    <div className="flex flex-col gap-5">
      {view.isError ? (
        <ErrorState message={errorText(view.error)} onRetry={() => void view.refetch()} />
      ) : (
        <DataTable
          columns={columns}
          data={view.data?.balances ?? []}
          getRowId={(b) => `${b.account_type}:${b.asset}`}
          loading={view.isPending}
          density="compact"
          empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.user.balancesEmpty")}</p>}
        />
      )}
      {can(admin, "ledger.adjust.request") && (
        <div className="grid gap-4 lg:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
          <Card title={t("admin.user.adjust")}>
            <AdjustForm userId={userId} onDone={setLast} />
          </Card>
          <Card title={t("admin.funds.outcome")}>
            {last ? <Outcome a={last} /> : <p className="py-6 text-center text-sm text-fg-3">{t("admin.funds.noOutcome")}</p>}
          </Card>
        </div>
      )}
    </div>
  );
}

function UserOrders({ userId }: { userId: string }) {
  return <OrdersTable list={useOrders({ user_id: userId })} withUser={false} />;
}

function UserTrades({ userId }: { userId: string }) {
  return <TradesTable list={useTrades({ user_id: userId })} />;
}

function UserDeposits({ userId }: { userId: string }) {
  return <DepositsTable list={useDeposits({ user_id: userId })} withUser={false} />;
}

function UserWithdrawals({ userId }: { userId: string }) {
  return <WithdrawalsTable list={useWithdrawals({ user_id: userId, status: "ALL" })} withUser={false} />;
}

function UserAudit({ userId }: { userId: string }) {
  return <AuditTable list={useAudit({ target: `user:${userId}` })} />;
}
