import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, Select, type BadgeTone, type ColumnDef } from "@exchange/ui";
import { Clock } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, lastFour } from "../../kit/actions";

import { TimeText } from "../../kit/format";
import { ListTable, pageSize, RowActions, useCursorList } from "../../kit/lists";

// The changes of trading parameters (design 2026-10-02 §2 item 6): what a
// preview says of them, how a status move is confirmed, and the changes
// waiting for a second ADMIN or their time.

export type ChangeGuard = AdminSchemas["ChangeGuard"];
export type InstrumentChange = AdminSchemas["InstrumentChange"];
export type StatusPreview = AdminSchemas["StatusPreview"];
type ParamChange = AdminSchemas["ParamChange"];
type TierImpact = AdminSchemas["TierImpact"];
type ChangeStatus = InstrumentChange["status"];

export const changesKey = ["admin", "instruments", "changes"];

const minutes = (seconds: number) => Math.max(1, Math.round(seconds / 60));

const statusTone: Record<ChangeStatus, BadgeTone> = {
  PENDING_APPROVAL: "warn", SCHEDULED: "info", APPLIED: "success", CANCELED: "neutral", REJECTED: "neutral", FAILED: "danger",
};

/** valueText shows a stored JSON value of a parameter (a ladder by its tiers). */
function valueText(v: unknown): string {
  if (v === null || v === undefined || v === "") return "—";
  if (Array.isArray(v)) {
    return v
      .map((t: { max_notional?: string; max_leverage?: number; mmr?: string }) => `≤${t.max_notional} ${t.max_leverage}x ${t.mmr}`)
      .join(" · ");
  }
  return typeof v === "object" ? JSON.stringify(v) : String(v);
}

/** ParamList lists the trading parameters a change moves, before and after. */
export function ParamList({ params }: { params: ParamChange[] }) {
  const { t } = useTranslation();
  return (
    <table className="w-full text-left text-xs" data-testid="trading-params">
      <tbody>
        {params.map((p) => (
          <tr key={`${p.entity}/${p.key}/${p.field}`} className="border-t border-line-1 first:border-t-0 align-top">
            <td className="px-3 py-1.5 font-mono text-fg-2">{p.key}</td>
            <td className="px-3 py-1.5 text-fg-3">{t(`admin.listing.fields.${p.field}`, { defaultValue: p.field })}</td>
            <td className="px-3 py-1.5 font-mono text-fg-3 line-through">{valueText(p.before)}</td>
            <td className="px-3 py-1.5 font-mono text-fg-1">{valueText(p.after)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/** ImpactLine says what a new risk ladder would do to the open positions. */
export function ImpactLine({ impact }: { impact: TierImpact }) {
  const { t } = useTranslation();
  const vars = {
    symbol: impact.symbol, liquidated: impact.liquidated, notional: impact.notional, accounts: impact.accounts, warned: impact.warned,
    over: impact.over_limit, positions: impact.positions,
  };
  return (
    <p className={impact.liquidated > 0 ? "text-sm text-danger" : "text-sm text-fg-2"} data-testid="tier-impact">
      {t(impact.liquidated > 0 ? "admin.changes.impact" : "admin.changes.impactNone", vars)}
      {impact.unmeasured > 0 && t("admin.changes.unmeasured", { n: impact.unmeasured })}
    </p>
  );
}

/** GuardNote is a preview's word on its trading parameters: what they are, their impact, how they take effect. */
export function GuardNote({ guard, canConfirm }: { guard: ChangeGuard; canConfirm: boolean }) {
  const { t } = useTranslation();
  if (guard.params.length === 0) return null;
  const note = !canConfirm
    ? t("admin.changes.needAdmin")
    : !guard.confirmation
      ? t("admin.changes.impactUnknown")
      : t(guard.two_person ? "admin.changes.paramsTwoPerson" : "admin.changes.paramsHint", { minutes: minutes(guard.delay_seconds) });
  return (
    <section className="rounded-2 border border-warn/40 bg-warn/10">
      <header className="flex items-center gap-2 border-b border-warn/30 px-3 py-2 text-sm">
        <Clock size={14} className="text-warn" />
        <span className="font-medium">{t("admin.changes.params")}</span>
      </header>
      <ParamList params={guard.params} />
      <div className="flex flex-col gap-1 border-t border-warn/30 px-3 py-2">
        {guard.impacts.map((imp) => (
          <ImpactLine key={imp.symbol} impact={imp} />
        ))}
        <p className="text-xs text-fg-2">{note}</p>
      </div>
    </section>
  );
}

/** scheduledText is the toast of a change confirmed. */
export function scheduledText(t: (k: string, o?: Record<string, unknown>) => string, c: InstrumentChange, delaySeconds: number) {
  return c.status === "PENDING_APPROVAL" ? t("admin.changes.pendingApproval") : t("admin.changes.scheduled", { minutes: minutes(delaySeconds) });
}

/**
 * moveNote says how a status move takes effect, and what rests on the pair
 * or contract (C5.5 ⑩); resuming a halt that a running HALT event of the
 * simulated market holds (simHalt) warns that it is halted again within 10
 * seconds (end the event; review ㉔).
 */
export function moveNote(t: (k: string, o?: Record<string, unknown>) => string, p: StatusPreview, simHalt = false) {
  const when = p.immediate
    ? t("admin.changes.immediate")
    : t(p.two_person ? "admin.changes.delayedTwoPerson" : "admin.changes.delayed", { minutes: minutes(p.delay_seconds) });
  const sim = simHalt && p.from === "HALT" && p.to === "TRADING" ? t("admin.changes.simHalt") : "";
  if (p.open_orders == null) return `${when}${sim}`;
  const orders = t("admin.changes.openOrders", { n: p.open_orders });
  return `${when}${orders}${p.to === "HALT" && p.open_orders > 0 ? t("admin.changes.openOrdersHalt") : ""}${sim}`;
}

/** ChangesTab lists the changes of trading parameters with what an ADMIN may do to them. */
export function ChangesTab({ admin }: { admin: Admin }) {
  const { t } = useTranslation();

  const [status, setStatus] = useState<ChangeStatus | "">("");
  const list = useCursorList<InstrumentChange>([...changesKey, status], async (cursor) =>
    adminData(
      await adminApi.GET("/admin/v1/instruments/changes", { params: { query: { status: status || undefined, cursor, limit: pageSize() } } }),
    ),
  );
  const columns = useMemo<ColumnDef<InstrumentChange, unknown>[]>(
    () => [
      { id: "time", header: t("admin.common.createdAt"), cell: ({ row }) => <TimeText value={row.original.created_at} style="datetime" /> },
      {
        id: "status", header: t("admin.common.status"),
        cell: ({ row }) =>
          row.original.status === "SCHEDULED" && row.original.applying_at ? (
            <Badge tone="warn" title={t("admin.changes.applyingHint")}>
              {t("admin.changes.applying")}
            </Badge>
          ) : (
            <Badge tone={statusTone[row.original.status]} title={row.original.status}>
              {t(`admin.changes.status.${row.original.status}`)}
            </Badge>
          ),
      },
      {
        id: "what", header: t("admin.changes.what"),
        cell: ({ row: { original: c } }) => (
          <span className="flex flex-col gap-0.5">
            <span className="text-xs text-fg-3">
              {t(`admin.changes.kind.${c.kind}`)} · <span className="font-mono">{c.target}</span>
            </span>
            {c.summary.params.slice(0, 3).map((p) => (
              <span key={`${p.key}/${p.field}`} className="font-mono text-xs">
                {p.key} {t(`admin.listing.fields.${p.field}`, { defaultValue: p.field })}: {valueText(p.before)} → {valueText(p.after)}
              </span>
            ))}
            {c.summary.params.length > 3 && <span className="text-xs text-fg-3">+{c.summary.params.length - 3}</span>}
            {c.summary.impacts.map((imp) => (
              <ImpactLine key={imp.symbol} impact={imp} />
            ))}
            <span className="text-xs text-fg-3">{c.reason}</span>
          </span>
        ),
      },
      { id: "requester", header: t("admin.changes.requester"), cell: ({ row }) => <span className="text-xs">{row.original.requested_by_email}</span> },
      {
        id: "effective", header: t("admin.changes.effective"),
        cell: ({ row: { original: c } }) =>
          c.effective_at ? (
            <span className="flex flex-col">
              <TimeText value={c.effective_at} style="datetime" />
              {c.approved_by_email && <span className="text-xs text-fg-3">{t("admin.changes.approver")}: {c.approved_by_email}</span>}
            </span>
          ) : (
            "—"
          ),
      },
      {
        id: "result", header: t("admin.changes.result"),
        cell: ({ row: { original: c } }) => (
          <span className="block max-w-[16rem] whitespace-normal break-words text-xs text-fg-2">
            {c.result || c.closed_by_email || ""}
          </span>
        ),
      },
      {
        id: "actions", header: "",
        cell: ({ row: { original: c } }) => <ChangeActions admin={admin} change={c} />,
      },
    ],
    [t, admin],
  );
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm text-fg-3">{t("admin.changes.help")}</p>
      <div className="w-48">
        <Select
          aria-label={t("admin.changes.filter")}
          size="sm"
          value={status || "ALL"}
          onValueChange={(v) => setStatus(v === "ALL" ? "" : (v as ChangeStatus))}
          options={[
            { value: "ALL", label: t("admin.common.all") },
            ...(["PENDING_APPROVAL", "SCHEDULED", "APPLIED", "CANCELED", "REJECTED", "FAILED"] as const).map((s) => ({
              value: s, label: t(`admin.changes.status.${s}`),
            })),
          ]}
        />
      </div>
      <ListTable list={list} columns={columns} getRowId={(c) => c.id} aria-label="instrument changes" />
    </div>
  );
}

function ChangeActions({ admin, change }: { admin: Admin; change: InstrumentChange }) {
  const { t } = useTranslation();
  const [action, setAction] = useState<"approve" | "reject" | "cancel" | null>(null);
  if (!can(admin, "instruments.trading") || (change.status !== "PENDING_APPROVAL" && change.status !== "SCHEDULED")) return null;
  const mine = change.requested_by === admin.id;
  const id = { params: { path: { id: change.id } } };
  const runs = {
    approve: async (reason: string) =>
      adminData(await adminApi.POST("/admin/v1/instruments/changes/{id}/decide", { ...id, body: { approve: true, reason } })),
    reject: async (reason: string) =>
      adminData(await adminApi.POST("/admin/v1/instruments/changes/{id}/decide", { ...id, body: { approve: false, reason } })),
    cancel: async (reason: string) => adminData(await adminApi.POST("/admin/v1/instruments/changes/{id}/cancel", { ...id, body: { reason } })),
  };
  return (
    <RowActions className="flex justify-end gap-1">
      {change.status === "PENDING_APPROVAL" && !mine && (
        <>
          <Button size="sm" onClick={() => setAction("approve")}>
            {t("admin.changes.approve")}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setAction("reject")}>
            {t("admin.changes.reject")}
          </Button>
        </>
      )}
      {!change.applying_at && (
        <Button size="sm" variant="ghost" onClick={() => setAction("cancel")} data-testid={`cancel-change-${change.id}`}>
          {t("admin.changes.cancel")}
        </Button>
      )}
      {action && (
        <DangerAction
          open
          onOpenChange={(o) => !o && setAction(null)}
          danger={action !== "approve"}
          title={t(`admin.changes.${action}Title`)}
          target={
            <span className="flex flex-col">
              <span className="font-mono">{change.target}</span>
              <span className="text-xs text-fg-3">{change.reason}</span>
            </span>
          }
          confirmWord={lastFour(change.id)}
          run={runs[action]}
          success={t(`admin.changes.${action === "approve" ? "approved" : action === "reject" ? "rejected" : "canceled"}`)}
          invalidate={[changesKey, ["admin", "instruments"], ["admin", "todo"]]}
          onDone={() => setAction(null)}
        >
          <ParamList params={change.summary.params} />
        </DangerAction>
      )}
    </RowActions>
  );
}
