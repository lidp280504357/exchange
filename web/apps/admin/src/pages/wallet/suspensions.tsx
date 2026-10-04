import { dec, formatDecimal } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { CirclePause, OctagonPause } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction } from "../../kit/actions";
import { useTimeText } from "../../kit/format";

// The custody suspension's console side (C5.5 ⑯): wallet-service suspends
// an asset's withdrawals when funds go missing on two custody checks (or
// an operator does); new requests are refused and approved ones wait. The
// withdrawals page says so above the list and marks the approved ones that
// wait; an ADMIN resumes them with a reason.

export type Suspension = AdminSchemas["WithdrawalSuspension"];

export const suspensionsKey = ["admin", "withdrawal-suspensions"] as const;

/** useSuspensions lists the suspended assets, refreshed every 30 seconds. */
export function useSuspensions() {
  return useQuery({
    queryKey: suspensionsKey,
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/withdrawals/suspensions")).items,
    refetchInterval: 30_000,
  });
}

/** useSuspended is the suspended assets by code. */
export function useSuspended(): Map<string, Suspension> {
  const q = useSuspensions();
  return useMemo(() => new Map((q.data ?? []).map((x) => [x.asset, x])), [q.data]);
}

/** SuspensionBanner says which assets' withdrawals are suspended, with the ADMIN's way to resume them. */
export function SuspensionBanner({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const time = useTimeText();
  const list = useSuspensions().data ?? [];
  if (list.length === 0) return null;
  return (
    <div className="flex flex-col gap-2" data-testid="withdrawal-suspensions">
      {list.map((x) => (
        <div key={x.asset} className="card flex flex-wrap items-center gap-x-4 gap-y-2 border-l-[3px] border-l-danger px-4 py-3 text-sm">
          <OctagonPause size={18} className="text-danger-strong" />
          <div className="min-w-0 flex-1">
            <div className="font-medium">
              {t("admin.suspensions.banner", { asset: x.asset, since: time(x.suspended_at, "datetime"), by: x.suspended_by, reason: x.reason })}
            </div>
            <div className="text-fg-3">
              {dec.isDecimal(x.shortfall) && dec.gt(x.shortfall, "0") && t("admin.suspensions.shortfall", { amount: formatDecimal(x.shortfall), asset: x.asset })}
              {t("admin.suspensions.effect")}
            </div>
          </div>
          {can(admin, "withdrawals.resume") && (
            <DangerAction
              trigger={(open) => (
                <Button size="sm" variant="danger" onClick={open}>
                  {t("admin.suspensions.resume")}
                </Button>
              )}
              title={t("admin.suspensions.resumeTitle", { asset: x.asset })}
              description={t("admin.suspensions.resumeHelp")}
              target={<span className="font-mono">{x.asset}</span>}
              confirmWord={x.asset}
              run={async (reason) =>
                adminData(
                  await adminApi.POST("/admin/v1/withdrawals/suspensions/{asset}/resume", { params: { path: { asset: x.asset } }, body: { reason } }),
                )
              }
              success={t("admin.suspensions.resumed", { asset: x.asset })}
              invalidate={[suspensionsKey, ["admin", "withdrawals"]]}
            />
          )}
        </div>
      ))}
    </div>
  );
}

/** SuspendedBadge marks an approved withdrawal that waits for its asset's withdrawals to resume. */
export function SuspendedBadge({ status, asset, suspended }: { status: string; asset: string; suspended: Map<string, Suspension> }) {
  const { t } = useTranslation();
  if (status !== "APPROVED" || !suspended.has(asset)) return null;
  return (
    <Badge tone="danger" title={t("admin.suspensions.waitingHint")} icon={<CirclePause size={12} />}>
      {t("admin.suspensions.waiting")}
    </Badge>
  );
}
