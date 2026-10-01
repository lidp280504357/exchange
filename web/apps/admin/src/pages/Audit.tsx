import { Button, Drawer } from "@exchange/ui";
import { Download } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { dayEnd, dayStart, FilterBar, useFilters } from "../kit/filters";
import { TimeText } from "../kit/format";
import { downloadCsv } from "../kit/lists";
import { Page } from "../kit/Page";
import { AuditTable, useAudit, type AuditEntry } from "./records/tables";

/** Audit (design §10.3): the whole trail by actor, target, event and time; rows export as CSV, a row shows its event. */
export default function Audit() {
  const { t } = useTranslation();
  const filters = useFilters(["actor", "target", "event_type", "from", "to"]);
  const f = filters.values;
  const list = useAudit({ actor: f.actor, target: f.target, event_type: f.event_type, from: dayStart(f.from ?? ""), to: dayEnd(f.to ?? "") });
  const [open, setOpen] = useState<AuditEntry | null>(null);
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
          <Button
            size="sm"
            variant="secondary"
            icon={<Download size={14} />}
            disabled={list.rows.length === 0}
            title={t("admin.common.exportHint", { n: list.rows.length })}
            onClick={() =>
              downloadCsv(
                "audit.csv",
                [
                  { header: "occurred_at", value: (e) => e.occurred_at },
                  { header: "event_id", value: (e) => e.event_id },
                  { header: "event_type", value: (e) => e.event_type },
                  { header: "actor", value: (e) => e.actor },
                  { header: "target", value: (e) => e.target },
                  { header: "payload", value: (e) => JSON.stringify(e.payload) },
                ],
                list.rows,
              )
            }
          >
            {t("admin.common.export")}
          </Button>
        }
      />
      <AuditTable list={list} onRowClick={setOpen} />
      {open && (
        <Drawer open onOpenChange={(o) => !o && setOpen(null)} title={open.event_type} description={<TimeText value={open.occurred_at} />}>
          <pre className="whitespace-pre-wrap break-all rounded-2 bg-bg-2 p-3 font-mono text-xs text-fg-1">{JSON.stringify(open.payload, null, 2)}</pre>
        </Drawer>
      )}
    </Page>
  );
}
