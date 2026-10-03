import { dec, formatDecimal, i18n } from "@exchange/core";
import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { Button, ConfirmDialog, toast, type RowSelectionState } from "@exchange/ui";
import { useQueryClient, type QueryKey } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { errorToast, useOperationKey } from "../../kit/actions";
import { useEnum } from "../../kit/enums";
import { ALL, FilterBar, useFilters } from "../../kit/filters";
import { NewerBar, useNewer } from "../../kit/lists";
import { Page } from "../../kit/Page";
import { todoKey } from "../../live";
import { WithdrawalDrawer, WithdrawalsTable, useWithdrawals, type Withdrawal } from "./withdrawalTable";

const STATUSES = [
  "PENDING_REVIEW", "APPROVED", "SIGNING", "BROADCAST", "CONFIRMING", "SUBMITTED", "CONFIRMED", "INTERNAL_TRANSFER", "REJECTED", "CANCELED", "FAILED",
];

// Risk scores under this count as low for the quick selection.
const LOW_RISK = 50;

/**
 * Withdrawals (design §10.3, 2026-10-02 §4.2): the review queue by
 * default, oldest first, filtered also by worth, risk score and hold; a
 * row opens its risk, address book entry, the user's withdrawals so far,
 * approvals and progress with the review actions (approve, reject, hold
 * with a note). In the queue rows can be checked and approved or rejected
 * together (the low-risk ones in one click). New withdrawals to review
 * show as a bar instead of reloading the list.
 */
export default function Withdrawals({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const label = useEnum();
  const filters = useFilters(["status", "user_id", "asset", "network", "held", "min_value_usdt", "max_value_usdt", "min_risk"]);
  const f = filters.values;
  const status = f.status || "PENDING_REVIEW";
  const decimal = (v: string | undefined) => (v && dec.isDecimal(v) ? v : undefined);
  const q = {
    status, user_id: f.user_id, asset: f.asset?.toUpperCase(), network: f.network?.toUpperCase(), held: f.held,
    min_value_usdt: decimal(f.min_value_usdt), max_value_usdt: decimal(f.max_value_usdt), min_risk: f.min_risk,
  };
  const list = useWithdrawals(q);
  const [open, setOpen] = useState<Withdrawal | null>(null);
  const [selection, setSelection] = useState<RowSelectionState>({});
  const batch = status === "PENDING_REVIEW" && can(admin, "withdrawals.review");
  const newer = useNewer(
    list.key,
    async () =>
      adminData(await adminApi.GET("/admin/v1/withdrawals", { params: { query: { status, order: "desc", limit: 1 } } })).items[0]?.id,
    status === "PENDING_REVIEW" ? newestOf(list.rows) : list.rows[0]?.id,
  );
  const chosen = list.rows.filter((w) => selection[w.id]);
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
          { key: "network", label: t("admin.common.network"), kind: "text", placeholder: "TRON", width: 120 },
          { key: "min_value_usdt", label: t("admin.hold.minValue"), kind: "text", placeholder: "0", width: 110 },
          { key: "max_value_usdt", label: t("admin.hold.maxValue"), kind: "text", placeholder: "20000", width: 110 },
          { key: "min_risk", label: t("admin.hold.minRisk"), kind: "text", placeholder: "50", width: 100 },
          {
            key: "held",
            label: t("admin.hold.filter"),
            kind: "select",
            options: [
              { value: ALL, label: t("admin.common.all") },
              { value: "true", label: t("admin.hold.held") },
              { value: "false", label: t("admin.hold.notHeld") },
            ],
            width: 110,
          },
        ]}
      />
      {batch && list.rows.length > 0 && (
        <BatchBar
          chosen={chosen}
          onLowRisk={() => setSelection(Object.fromEntries(list.rows.filter((w) => (w.risk_score ?? 0) < LOW_RISK).map((w) => [w.id, true])))}
          onClear={() => setSelection({})}
          listKey={list.key}
        />
      )}
      {newer && <NewerBar listKey={list.key} />}
      <WithdrawalsTable
        list={list}
        onRowClick={setOpen}
        selection={batch ? selection : undefined}
        onSelectionChange={batch ? setSelection : undefined}
      />
      {open && <WithdrawalDrawer admin={admin} w={open} onClose={() => setOpen(null)} />}
    </Page>
  );
}

/** BatchBar reviews the checked withdrawals together, each on its own. */
function BatchBar({ chosen, onLowRisk, onClear, listKey }: { chosen: Withdrawal[]; onLowRisk: () => void; onClear: () => void; listKey: QueryKey }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const op = useOperationKey();
  const [approve, setApproveState] = useState<boolean | null>(null);
  const setApprove = (a: boolean | null) => {
    if (a === null) op.reset();
    setApproveState(a);
  };
  const value = chosen.reduce((sum, w) => (w.value_usdt && dec.isDecimal(w.value_usdt) ? dec.add(sum, w.value_usdt) : sum), "0");
  const run = async (reason: string) => {
    try {
      const res = adminData(
        await adminApi.POST("/admin/v1/withdrawals/review-batch", {
          params: { header: { "Idempotency-Key": op.get() } },
          body: { ids: chosen.map((w) => w.id), approve: approve === true, reason },
        }),
      );
      const failed = res.results.filter((r) => !r.ok);
      const ok = res.results.length - failed.length;
      if (failed.length === 0) toast.success(t("admin.batch.done", { ok }));
      else
        toast.error(t("admin.batch.partly", { ok, failed: failed.length }), {
          description: failed.map((r) => i18n.t("admin.batch.failedLine", { id: r.id.slice(-8), message: r.message ?? r.code })).join("\n"),
          duration: 12000,
        });
      setApprove(null);
      onClear();
    } catch (err) {
      op.failed(err);
      errorToast(err);
    } finally {
      void qc.invalidateQueries({ queryKey: listKey });
      void qc.invalidateQueries({ queryKey: todoKey });
    }
  };
  return (
    <div className="card flex flex-wrap items-center gap-2 px-4 py-2.5 text-sm animate-rise">
      <span className="text-fg-2">{t("admin.batch.selected", { n: chosen.length, value: formatDecimal(value, { decimals: 2 }) })}</span>
      <Button size="sm" variant="ghost" onClick={onLowRisk}>
        {t("admin.batch.selectLowRisk", { max: LOW_RISK })}
      </Button>
      {chosen.length > 0 && (
        <Button size="sm" variant="ghost" onClick={onClear}>
          {t("admin.batch.clear")}
        </Button>
      )}
      <div className="ml-auto flex gap-2">
        <Button size="sm" disabled={chosen.length === 0} onClick={() => setApprove(true)}>
          {t("admin.batch.approve")}
        </Button>
        <Button size="sm" variant="danger" disabled={chosen.length === 0} onClick={() => setApprove(false)}>
          {t("admin.batch.reject")}
        </Button>
      </div>
      <ConfirmDialog
        open={approve !== null}
        onOpenChange={(o) => !o && setApprove(null)}
        title={t(approve ? "admin.batch.approveTitle" : "admin.batch.rejectTitle", { n: chosen.length })}
        description={t("admin.batch.hint")}
        target={t("admin.batch.selected", { n: chosen.length, value: formatDecimal(value, { decimals: 2 }) })}
        confirmWord={String(chosen.length)}
        danger={approve === false}
        onConfirm={run}
      />
    </div>
  );
}

/** newestOf is the newest of the review queue (listed oldest first). */
function newestOf(rows: Withdrawal[]): string | undefined {
  let best: Withdrawal | undefined;
  for (const w of rows) if (!best || w.created_at > best.created_at) best = w;
  return best?.id;
}
