import { adminApi, adminData, type Admin } from "@exchange/core/api/admin";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useEnum } from "../../kit/enums";
import { ALL, FilterBar, useFilters } from "../../kit/filters";
import { NewerBar, useNewer } from "../../kit/lists";
import { Page } from "../../kit/Page";
import { WithdrawalDrawer, WithdrawalsTable, useWithdrawals, type Withdrawal } from "./withdrawalTable";

const STATUSES = ["PENDING_REVIEW", "APPROVED", "SIGNING", "BROADCAST", "CONFIRMING", "CONFIRMED", "INTERNAL_TRANSFER", "REJECTED", "CANCELED", "FAILED"];

/**
 * Withdrawals (design §10.3): the review queue by default, oldest first;
 * a row opens its risk, approvals and progress with the review actions.
 * New withdrawals to review show as a bar instead of reloading the list.
 */
export default function Withdrawals({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const label = useEnum();
  const filters = useFilters(["status", "user_id", "asset"]);
  const f = filters.values;
  const status = f.status || "PENDING_REVIEW";
  const q = { status, user_id: f.user_id, asset: f.asset?.toUpperCase() };
  const list = useWithdrawals(q);
  const [open, setOpen] = useState<Withdrawal | null>(null);
  const newer = useNewer(
    list.key,
    async () =>
      adminData(await adminApi.GET("/admin/v1/withdrawals", { params: { query: { status, order: "desc", limit: 1 } } })).items[0]?.id,
    status === "PENDING_REVIEW" ? newestOf(list.rows) : list.rows[0]?.id,
  );
  return (
    <Page title={t("admin.nav.withdrawals")}>
      <FilterBar
        page="withdrawals"
        filters={filters}
        defs={[
          {
            key: "status",
            label: t("admin.common.status"),
            kind: "select",
            // No filter is the review queue; ALL lists every status.
            options: [
              { value: ALL, label: t("admin.withdrawals.queue") },
              { value: "ALL", label: label("withdrawalStatus", "ALL") },
              ...STATUSES.slice(1).map((s) => ({ value: s, label: label("withdrawalStatus", s) })),
            ],
            width: 150,
          },
          { key: "user_id", label: t("admin.orders.userFilter"), kind: "text" },
          { key: "asset", label: t("admin.common.asset"), kind: "text", placeholder: "USDT", width: 100 },
        ]}
      />
      {newer && <NewerBar listKey={list.key} />}
      <WithdrawalsTable list={list} onRowClick={setOpen} />
      {open && <WithdrawalDrawer admin={admin} w={open} onClose={() => setOpen(null)} />}
    </Page>
  );
}

/** newestOf is the newest of the review queue (listed oldest first). */
function newestOf(rows: Withdrawal[]): string | undefined {
  let best: Withdrawal | undefined;
  for (const w of rows) if (!best || w.created_at > best.created_at) best = w;
  return best?.id;
}
