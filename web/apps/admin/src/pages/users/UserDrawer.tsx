import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Button, DataTable, Drawer, ErrorState, KeyValue, Select, Skeleton, Tabs, type DataColumnMeta, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";

import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { errorText } from "@exchange/core";
import { DangerAction, lastFour } from "../../kit/actions";
import { EnumBadge, EnumText, useEnum } from "../../kit/enums";
import { Num, TimeText } from "../../kit/format";
import { AuditTable, DepositsTable, OrdersTable, TradesTable, useAudit, useDeposits, useOrders, useTrades } from "../records/tables";
import { WithdrawalsTable, useWithdrawals } from "../wallet/withdrawalTable";

type UserView = AdminSchemas["UserView"];
type Balance = UserView["balances"][number];

const STATUSES = ["ACTIVE", "RISK_REVIEW", "FROZEN", "CLOSED"] as const;
const REASONS = ["SUSPICIOUS_LOGIN", "FRAUD_SUSPECTED", "COMPLIANCE_REVIEW", "USER_REQUEST", "REVIEW_CLEARED"];

/** UserDrawer is a user's detail panel (design §10.3): account, balances, records and actions. */
export default function UserDrawer({ admin, userId, onClose }: { admin: Admin; userId: string; onClose: () => void }) {
  const { t } = useTranslation();
  const [tab, setTab] = useState("overview");
  const view = useQuery({
    queryKey: ["admin", "user", userId],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/users/lookup", { params: { query: { q: userId } } })),
  });
  const tabs = ["overview", "orders", "trades", "deposits", "withdrawals", "audit"]
    .filter((k) => (k === "audit" ? can(admin, "audit.read") : k === "deposits" || k === "withdrawals" ? can(admin, "withdrawals.read") : true))
    .map((k) => ({ value: k, label: t(`admin.users.tabs.${k}`) }));
  return (
    <Drawer
      open
      onOpenChange={(open) => !open && onClose()}
      title={t("admin.nav.users")}
      description={<span className="font-mono">{userId}</span>}
      actions={view.data && <EnumBadge group="userStatus" code={view.data.user.status} />}
      width={880}
    >
      {view.isError ? (
        <ErrorState message={errorText(view.error)} onRetry={() => void view.refetch()} />
      ) : (
        <div className="flex flex-col gap-4">
          <Tabs items={tabs} value={tab} onValueChange={setTab} />
          {tab === "overview" && (view.data ? <Overview admin={admin} view={view.data} /> : <Skeleton className="h-40 w-full" />)}
          {tab === "orders" && <UserOrders userId={userId} />}
          {tab === "trades" && <UserTrades userId={userId} />}
          {tab === "deposits" && <UserDeposits userId={userId} />}
          {tab === "withdrawals" && <UserWithdrawals userId={userId} />}
          {tab === "audit" && <UserAudit userId={userId} />}
        </div>
      )}
    </Drawer>
  );
}

function Overview({ admin, view }: { admin: Admin; view: UserView }) {
  const { t } = useTranslation();
  const u = view.user;
  const right: DataColumnMeta = { align: "right" };
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
      <KeyValue
        layout="grid"
        columns={3}
        items={[
          { label: t("admin.users.id"), value: <span className="font-mono text-xs">{u.id}</span>, copy: u.id },
          { label: t("admin.common.status"), value: <EnumBadge group="userStatus" code={u.status} /> },
          { label: t("admin.users.createdAt"), value: <TimeText value={u.created_at} /> },
          { label: t("admin.users.region"), value: u.region || "—" },
          { label: t("admin.users.language"), value: u.language || "—" },
          { label: t("admin.users.kyc"), value: u.kyc_level },
        ]}
      />
      <section>
        <h3 className="mb-2 text-sm font-medium text-fg-2">{t("admin.users.balances")}</h3>
        <DataTable
          columns={columns}
          data={view.balances}
          getRowId={(b) => `${b.account_type}:${b.asset}`}
          density="compact"
          empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.users.noBalances")}</p>}
        />
      </section>
      {(can(admin, "users.status") || can(admin, "orders.cancel")) && <Actions admin={admin} view={view} />}
    </div>
  );
}

function Actions({ admin, view }: { admin: Admin; view: UserView }) {
  const { t } = useTranslation();
  const label = useEnum();
  const u = view.user;
  // Never preselect a freeze (A3): the first status other than the current one.
  const [to, setTo] = useState<string>(STATUSES.find((s) => s !== u.status) ?? "ACTIVE");
  const [reason, setReason] = useState(REASONS[0]!);
  const [note, setNote] = useState("");
  return (
    <section className="flex flex-col gap-3 rounded-3 border border-line-1 p-4">
      {can(admin, "users.status") && (
        <div className="flex flex-wrap items-end gap-2">
          <label className="flex flex-col gap-1 text-xs text-fg-3">
            {t("admin.users.to")}
            <Select
              size="sm"
              value={to}
              onValueChange={setTo}
              options={STATUSES.filter((s) => s !== u.status).map((s) => ({ value: s, label: label("userStatus", s) }))}
              className="w-36"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-fg-3">
            {t("admin.users.reasonCode")}
            <Select size="sm" value={reason} onValueChange={setReason} options={REASONS.map((r) => ({ value: r, label: label("reasonCode", r) }))} className="w-40" />
          </label>
          <label className="flex min-w-48 flex-1 flex-col gap-1 text-xs text-fg-3">
            {t("admin.users.note")}
            <input
              value={note}
              onChange={(e) => setNote(e.target.value)}
              className="h-8 rounded-1 border border-line-2 bg-bg-1 px-2.5 text-sm text-fg-1 outline-none focus:border-brand"
            />
          </label>
          <DangerAction
            trigger={(open) => (
              <Button size="sm" variant={to === "ACTIVE" ? "primary" : "danger"} onClick={open}>
                {t("admin.users.changeStatus")}
              </Button>
            )}
            danger={to !== "ACTIVE"}
            title={t("admin.users.changeTitle", { to: label("userStatus", to) })}
            target={<span className="font-mono text-xs">{u.id}</span>}
            confirmWord={lastFour(u.id)}
            run={async (why) =>
              adminData(
                await adminApi.POST("/admin/v1/users/{id}/status", {
                  params: { path: { id: u.id } },
                  body: { to: to as (typeof STATUSES)[number], reason, note: [note, why].filter(Boolean).join(" · ") },
                }),
              )
            }
            success={t("admin.users.changed", { from: label("userStatus", u.status), to: label("userStatus", to) })}
            invalidate={[["admin", "user", u.id], ["admin", "users"]]}
          />
        </div>
      )}
      {can(admin, "orders.cancel") && (
        <div>
          <DangerAction
            trigger={(open) => (
              <Button size="sm" variant="secondary" onClick={open}>
                {t("admin.users.cancelOrders")}
              </Button>
            )}
            title={t("admin.users.cancelTitle")}
            target={<span className="font-mono text-xs">{u.id}</span>}
            confirmWord={lastFour(u.id)}
            run={async (reason) => {
              const res = await adminApi.POST("/admin/v1/users/{id}/cancel-orders", { params: { path: { id: u.id } }, body: { reason } });
              if (!res.response.ok) adminData(res);
            }}
            success={t("admin.users.cancelled")}
            invalidate={[["admin", "orders"]]}
          />
        </div>
      )}
    </section>
  );
}

function UserOrders({ userId }: { userId: string }) {
  const list = useOrders({ user_id: userId });
  return <OrdersTable list={list} withUser={false} />;
}

function UserTrades({ userId }: { userId: string }) {
  const list = useTrades({ user_id: userId });
  return <TradesTable list={list} />;
}

function UserDeposits({ userId }: { userId: string }) {
  const list = useDeposits({ user_id: userId });
  return <DepositsTable list={list} withUser={false} />;
}

function UserWithdrawals({ userId }: { userId: string }) {
  const list = useWithdrawals({ user_id: userId, status: "ALL" });
  return <WithdrawalsTable list={list} withUser={false} />;
}

function UserAudit({ userId }: { userId: string }) {
  const list = useAudit({ target: `user:${userId}` });
  return <AuditTable list={list} />;
}
