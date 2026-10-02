import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { Button, DataTable, Segmented, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, lastFour } from "../../kit/actions";
import { TimeText } from "../../kit/format";
import { RowActions } from "../../kit/lists";
import { Card, Page } from "../../kit/Page";
import { EventStatus, price, simEventsKey, simKey, useEventText, type SimEvent } from "./common";

/**
 * The events' schedule (ASTRA design §6.1): the price events queued,
 * running and past, who asked for them and who approved; a queued one is
 * canceled and a running one ended here (a HALT ends by resuming trading).
 */
export default function SimEvents({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const [view, setView] = useState("all");
  const q = useQuery({
    queryKey: [...simEventsKey, view],
    queryFn: async () =>
      adminData(await adminApi.GET("/admin/v1/sim/events", { params: { query: view === "all" ? { all: true, limit: 100 } : {} } })).items,
    refetchInterval: 5_000,
  });
  const eventText = useEventText();
  const control = can(admin, "sim.control");
  const columns = useMemo<ColumnDef<SimEvent, unknown>[]>(
    () => [
      { id: "created", header: t("admin.common.createdAt"), cell: ({ row }) => <TimeText value={row.original.created_at} style="datetime" /> },
      {
        id: "what", header: t("admin.sim.event"),
        cell: ({ row: { original: e } }) => (
          <span className="flex flex-col">
            <span className="font-medium text-fg-1">{eventText(e)}</span>
            <span className="text-xs text-fg-3">{e.reason}</span>
          </span>
        ),
      },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <EventStatus e={row.original} /> },
      {
        id: "when", header: t("admin.sim.when"),
        cell: ({ row: { original: e } }) => (
          <span className="flex flex-col text-xs">
            <span>{t("admin.sim.begins")} <TimeText value={e.started_at ?? e.starts_at} style="datetimeSeconds" /></span>
            {e.ended_at && <span className="text-fg-3">{t("admin.sim.endedAt")} <TimeText value={e.ended_at} style="datetimeSeconds" /></span>}
            {e.from_price && <span className="text-fg-3">{t("admin.sim.fromPrice")} <span className="font-mono">{price(e.from_price)}</span></span>}
          </span>
        ),
      },
      {
        id: "who", header: t("admin.sim.who"),
        cell: ({ row: { original: e } }) => (
          <span className="flex flex-col text-xs">
            <span>{e.created_by}</span>
            {e.approved_by && <span className="text-fg-3">{t("admin.sim.approvedBy")} {e.approved_by}</span>}
            {e.ended_by && <span className="text-fg-3">{t("admin.sim.endedBy")} {e.ended_by}</span>}
          </span>
        ),
      },
      {
        id: "actions", header: "", meta: { align: "right" },
        cell: ({ row: { original: e } }) =>
          control && (e.status === "SCHEDULED" || e.status === "RUNNING") ? (
            <RowActions>
              <DangerAction
                trigger={(open) => (
                  <Button size="sm" variant={e.status === "RUNNING" ? "danger" : "secondary"} onClick={open} data-testid={`sim-end-${e.id}`}>
                    {t(e.status === "RUNNING" ? "admin.sim.end" : "admin.sim.cancel")}
                  </Button>
                )}
                title={t(e.status === "RUNNING" ? "admin.sim.endTitle" : "admin.sim.cancelTitle")}
                description={e.type === "HALT" ? t("admin.sim.endHalt") : undefined}
                target={eventText(e)}
                confirmWord={lastFour(e.id)}
                run={async (reason) =>
                  adminData(await adminApi.POST("/admin/v1/sim/events/{id}/end", { params: { path: { id: e.id } }, body: { reason } }))
                }
                success={t("admin.sim.ended")}
                invalidate={[simEventsKey, simKey]}
              />
            </RowActions>
          ) : null,
      },
    ],
    [t, eventText, control],
  );
  return (
    <Page
      title={t("admin.nav.simEvents")}
      help={t("admin.sim.eventsHelp")}
      actions={
        <Segmented
          size="sm"
          value={view}
          onValueChange={setView}
          items={[
            { value: "all", label: t("admin.sim.allEvents") },
            { value: "active", label: t("admin.sim.activeEvents") },
          ]}
        />
      }
    >
      <Card className="stagger">
        <DataTable
          columns={columns}
          data={q.data ?? []}
          getRowId={(e) => e.id}
          loading={q.isPending}
          error={q.error}
          onRetry={() => void q.refetch()}
          empty={t("admin.sim.noEvents")}
          density="compact"
          aria-label={t("admin.nav.simEvents")}
        />
      </Card>
    </Page>
  );
}
