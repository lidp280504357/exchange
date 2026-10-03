import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { Button, Drawer, toast } from "@exchange/ui";
import { Download } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { errorToast } from "../kit/actions";
import { dayEnd, dayStart, FilterBar, useFilters } from "../kit/filters";
import { TimeText } from "../kit/format";
import { Page } from "../kit/Page";
import { AuditTable, clean, useAudit, type AuditEntry } from "./records/tables";
import { AuditDetail } from "./system/auditDetail";

/**
 * Audit (design §10.3, 2026-10-02 §4.6): the whole trail by actor, target,
 * event and time; the server exports what the filters match as CSV (at
 * most 10,000 entries, audited too; it has email and IP addresses, so
 * only ADMIN and AUDITOR export, C5.5 ⑪), a row opens its detail with
 * what changed field by field.
 */
export default function Audit({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const filters = useFilters(["actor", "target", "event_type", "from", "to"]);
  const f = filters.values;
  const query = { actor: f.actor, target: f.target, event_type: f.event_type, from: dayStart(f.from ?? ""), to: dayEnd(f.to ?? "") };
  const list = useAudit(query);
  const [open, setOpen] = useState<AuditEntry | null>(null);
  const [exporting, setExporting] = useState(false);
  const exportCsv = async () => {
    setExporting(true);
    try {
      const res = await adminApi.GET("/admin/v1/audit-logs/export", { params: { query: clean(query) }, parseAs: "blob" });
      const blob = adminData(res) as Blob;
      const name = /filename="([^"]+)"/.exec(res.response.headers.get("Content-Disposition") ?? "")?.[1] ?? "audit.csv";
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = name;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      if (res.response.headers.get("X-Truncated") === "true") toast.info(t("admin.audit.exportTruncated"), { duration: 10_000 });
      else toast.success(t("admin.audit.exported"));
    } catch (err) {
      errorToast(err);
    } finally {
      setExporting(false);
    }
  };
  return (
    <Page title={t("admin.nav.audit")}>
      <FilterBar
        page="audit"
        filters={filters}
        defs={[
          { key: "actor", label: t("admin.audit.actor"), kind: "text", placeholder: "admin@example.com" },
          { key: "target", label: t("admin.audit.target"), kind: "text", placeholder: "user:… / pair:BTC-USDT / flag:wallet.withdraw" },
          { key: "event_type", label: t("admin.audit.eventType"), kind: "text", placeholder: "audit.AdminActionPerformed" },
          { key: "from", label: t("admin.common.from"), kind: "date" },
          { key: "to", label: t("admin.common.to"), kind: "date" },
        ]}
        extra={
          can(admin, "audit.export") && (
            <Button
              size="sm"
              variant="secondary"
              icon={<Download size={14} />}
              loading={exporting}
              title={t("admin.audit.exportServerHint")}
              onClick={() => void exportCsv()}
              data-testid="audit-export"
            >
              {t("admin.audit.exportServer")}
            </Button>
          )
        }
      />
      <AuditTable list={list} onRowClick={setOpen} />
      {open && (
        <Drawer open onOpenChange={(o) => !o && setOpen(null)} title={open.event_type} description={<TimeText value={open.occurred_at} />}>
          <AuditDetail entry={open} />
        </Drawer>
      )}
    </Page>
  );
}
