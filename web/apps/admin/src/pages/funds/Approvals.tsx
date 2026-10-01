import type { Admin } from "@exchange/core/api/admin";
import { useTranslation } from "react-i18next";
import { useEnum } from "../../kit/enums";
import { ALL, FilterBar, useFilters } from "../../kit/filters";
import { Page } from "../../kit/Page";
import { ModeBanner } from "./ModeBanner";
import { ApprovalsTable, useApprovals } from "./approvalsTable";

/**
 * Approvals (design 2026-10-02 §3): the fund operations, those waiting for
 * a decision first; each says whether its requester carried it out alone
 * or why it waits for a second administrator.
 */
export default function Approvals({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const label = useEnum();
  const filters = useFilters(["approval_status"]);
  const status = filters.values.approval_status || "PENDING";
  const list = useApprovals(status === "ALL" ? "" : status);
  return (
    <Page title={t("admin.nav.approvals")} help={t("admin.funds.approvalsHelp")}>
      <ModeBanner />
      <FilterBar
        page="approvals"
        filters={filters}
        defs={[
          {
            key: "approval_status",
            label: t("admin.common.status"),
            kind: "select",
            // No filter is the queue of pending ones.
            options: [
              { value: ALL, label: label("approvalStatus", "PENDING") },
              { value: "ALL", label: t("admin.common.all") },
              ...["EXECUTED", "REJECTED", "FAILED"].map((s) => ({ value: s, label: label("approvalStatus", s) })),
            ],
          },
        ]}
      />
      <ApprovalsTable admin={admin} list={list} />
    </Page>
  );
}
