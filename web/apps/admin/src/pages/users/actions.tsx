import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Button, Select } from "@exchange/ui";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, lastFour } from "../../kit/actions";
import { useEnum } from "../../kit/enums";
import { ProfileResets } from "./identity";

const STATUSES = ["ACTIVE", "RISK_REVIEW", "FROZEN", "CLOSED"] as const;
const REASONS = ["SUSPICIOUS_LOGIN", "FRAUD_SUSPECTED", "COMPLIANCE_REVIEW", "USER_REQUEST", "REVIEW_CLEARED"];

/**
 * StatusAction moves an account to another status (user-service's state machine) with a reason code and a note;
 * initialTo and initialReason preselect them (the risk tab offers RISK_REVIEW and back).
 */
export function StatusAction({ userId, status, initialTo, initialReason }: { userId: string; status: string; initialTo?: string; initialReason?: string }) {
  const { t } = useTranslation();
  const label = useEnum();
  // Never preselect a freeze: the first status other than the current one.
  const [to, setTo] = useState<string>(initialTo && initialTo !== status ? initialTo : (STATUSES.find((s) => s !== status) ?? "ACTIVE"));
  const [reason, setReason] = useState(initialReason && REASONS.includes(initialReason) ? initialReason : REASONS[0]!);
  return (
    <div className="flex flex-col gap-2">
      <div className="grid grid-cols-2 gap-2">
        <label className="flex flex-col gap-1 text-xs text-fg-3">
          {t("admin.users.to")}
          <Select
            size="sm"
            value={to}
            onValueChange={setTo}
            options={STATUSES.filter((s) => s !== status).map((s) => ({ value: s, label: label("userStatus", s) }))}
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-fg-3">
          {t("admin.users.reasonCode")}
          <Select size="sm" value={reason} onValueChange={setReason} options={REASONS.map((r) => ({ value: r, label: label("reasonCode", r) }))} />
        </label>
      </div>
      <DangerAction
        trigger={(open) => (
          <Button size="sm" variant={to === "ACTIVE" ? "primary" : "danger"} onClick={open} block>
            {t("admin.users.changeStatus")}
          </Button>
        )}
        danger={to !== "ACTIVE"}
        title={t("admin.users.changeTitle", { to: label("userStatus", to) })}
        target={<span className="font-mono text-xs">{userId}</span>}
        confirmWord={lastFour(userId)}
        run={async (why) =>
          adminData(
            await adminApi.POST("/admin/v1/users/{id}/status", {
              params: { path: { id: userId } },
              body: { to: to as (typeof STATUSES)[number], reason, note: why },
            }),
          )
        }
        success={t("admin.users.changed", { from: label("userStatus", status), to: label("userStatus", to) })}
        invalidate={[["admin", "user", userId], ["admin", "users"]]}
      />
    </div>
  );
}

/** CancelOrdersAction cancels every open spot order of an account. */
export function CancelOrdersAction({ userId }: { userId: string }) {
  const { t } = useTranslation();
  return (
    <DangerAction
      trigger={(open) => (
        <Button size="sm" variant="secondary" onClick={open} block>
          {t("admin.users.cancelOrders")}
        </Button>
      )}
      title={t("admin.users.cancelTitle")}
      target={<span className="font-mono text-xs">{userId}</span>}
      confirmWord={lastFour(userId)}
      run={async (reason) => {
        const res = await adminApi.POST("/admin/v1/users/{id}/cancel-orders", { params: { path: { id: userId } }, body: { reason } });
        if (!res.response.ok) adminData(res);
      }}
      success={t("admin.users.cancelled")}
      invalidate={[["admin", "orders"]]}
    />
  );
}

/** UserActions are what the administrator's role may do to an account from its page. */
export function UserActions({ admin, user }: { admin: Admin; user: AdminSchemas["UserSummary"] }) {
  return (
    <div className="flex flex-col gap-3">
      {can(admin, "users.status") && <StatusAction key={user.status} userId={user.id} status={user.status} />}
      {can(admin, "orders.cancel") && <CancelOrdersAction userId={user.id} />}
      {can(admin, "users.status") && <ProfileResets user={user} />}
    </div>
  );
}
