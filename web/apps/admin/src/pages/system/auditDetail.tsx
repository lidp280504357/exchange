import type { AdminSchemas } from "@exchange/core/api/admin";
import { KeyValue } from "@exchange/ui";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { TimeText } from "../../kit/format";

// An audit entry in detail (design 2026-10-02 §4.6): who did what to which
// target and why, and what changed, field by field. A configuration
// change (ConfigChanged) carries its old and new values; an
// administrator action (AdminActionPerformed) its details, which hold a
// from/to pair, before/after values or a list of changes.

type Entry = AdminSchemas["AuditEntry"];
type Payload = Record<string, unknown>;

/** A field of a change: its dotted path and its value before and after. */
export type DiffRow = { path: string; before: unknown; after: unknown };

/** parseJSON reads a JSON string, leaving anything else as it is. */
function parseJSON(v: unknown): unknown {
  if (typeof v !== "string") return v;
  try {
    return JSON.parse(v);
  } catch {
    return v;
  }
}

/** flatten turns nested objects into dotted paths; arrays stay whole. */
export function flatten(v: unknown, prefix = "", out: Record<string, unknown> = {}): Record<string, unknown> {
  if (v !== null && typeof v === "object" && !Array.isArray(v)) {
    const entries = Object.entries(v);
    if (entries.length === 0 && prefix) out[prefix] = {};
    for (const [k, x] of entries) flatten(x, prefix ? `${prefix}.${k}` : k, out);
  } else {
    out[prefix || "value"] = v;
  }
  return out;
}

/** diff compares two values field by field: the changed fields and the unchanged ones. */
export function diff(before: unknown, after: unknown): { changed: DiffRow[]; same: DiffRow[] } {
  const a = before === undefined || before === null ? {} : flatten(before);
  const b = after === undefined || after === null ? {} : flatten(after);
  const changed: DiffRow[] = [];
  const same: DiffRow[] = [];
  for (const path of [...new Set([...Object.keys(a), ...Object.keys(b)])].sort()) {
    const row = { path, before: a[path], after: b[path] };
    (JSON.stringify(row.before) === JSON.stringify(row.after) ? same : changed).push(row);
  }
  return { changed, same };
}

/** A change an entry describes: a title (an entity and key) with its before and after values, when known. */
type Change = { title?: string; before?: unknown; after?: unknown; known: boolean };

/** changesOf reads what an entry changed; null when its details are not a change. */
export function changesOf(payload: Payload): Change[] | null {
  if ("oldValue" in payload || "newValue" in payload) {
    return [{ before: parseJSON(payload.oldValue), after: parseJSON(payload.newValue), known: true }];
  }
  const d = parseJSON(payload.details);
  if (d === null || typeof d !== "object" || Array.isArray(d)) return null;
  const details = d as Payload;
  if ("from" in details && "to" in details) return [{ before: details.from, after: details.to, known: true }];
  if ("before" in details || "after" in details) return [{ before: details.before, after: details.after, known: true }];
  if (Array.isArray(details.changes)) {
    return (details.changes as Payload[]).map((c) => ({
      title: [c.entity, c.key, c.action, c.version !== undefined ? `v${String(c.version)}` : ""].filter(Boolean).join(" · "),
      before: c.before,
      after: c.after,
      known: "before" in c || "after" in c,
    }));
  }
  return null;
}

const show = (v: unknown): string => (v === undefined ? "—" : typeof v === "string" ? v : JSON.stringify(v));

/** AuditDetail is an entry in a drawer: its facts, its changes field by field, its raw event. */
export function AuditDetail({ entry }: { entry: Entry }) {
  const { t } = useTranslation();
  const p = (entry.payload && typeof entry.payload === "object" ? entry.payload : {}) as Payload;
  const changes = changesOf(p);
  const details = changes ? null : parseJSON(p.details);
  const text = (v: unknown) => (typeof v === "string" && v ? v : "—");
  return (
    <div className="flex flex-col gap-5" data-testid="audit-detail">
      <KeyValue
        density="compact"
        items={[
          { key: "time", label: t("admin.common.time"), value: <TimeText value={entry.occurred_at} /> },
          { key: "actor", label: t("admin.audit.actor"), value: entry.actor, copy: true },
          { key: "target", label: t("admin.audit.target"), value: <span className="font-mono text-xs">{entry.target}</span>, copy: entry.target },
          { key: "action", label: t("admin.audit.action"), value: <span className="font-mono text-xs">{text(p.action)}</span> },
          { key: "reason", label: t("admin.audit.reason"), value: text(p.reason) },
          { key: "event", label: t("admin.audit.eventId"), value: <span className="font-mono text-xs">{entry.event_id}</span>, copy: entry.event_id },
        ]}
      />
      {changes && (
        <section className="flex flex-col gap-3">
          <h3 className="text-sm font-semibold text-fg-1">{t("admin.audit.changes")}</h3>
          {changes.map((c, i) => (
            <ChangeView key={i} change={c} />
          ))}
        </section>
      )}
      {details !== null && details !== undefined && details !== "" && (
        <section className="flex flex-col gap-2">
          <h3 className="text-sm font-semibold text-fg-1">{t("admin.audit.details")}</h3>
          {typeof details === "object" ? (
            <KeyValue
              density="compact"
              items={Object.entries(flatten(details)).map(([k, v]) => ({
                key: k, label: <span className="font-mono text-xs">{k}</span>, value: <span className="break-all font-mono text-xs">{show(v)}</span>,
              }))}
            />
          ) : (
            <p className="break-all font-mono text-xs text-fg-2">{show(details)}</p>
          )}
        </section>
      )}
      <details className="group">
        <summary className="cursor-pointer text-sm text-fg-3 hover:text-fg-1">{t("admin.audit.raw")}</summary>
        <pre className="mt-2 whitespace-pre-wrap break-all rounded-2 bg-bg-2 p-3 font-mono text-xs text-fg-1">{JSON.stringify(entry.payload, null, 2)}</pre>
      </details>
    </div>
  );
}

function ChangeView({ change }: { change: Change }) {
  const { t } = useTranslation();
  const [all, setAll] = useState(false);
  const { changed, same } = diff(change.before, change.after);
  let body: ReactNode;
  if (!change.known) body = null;
  else if (changed.length === 0) body = <p className="text-sm text-fg-3">{t("admin.audit.noChanges")}</p>;
  else {
    const rows = all ? [...changed, ...same] : changed;
    body = (
      <div className="overflow-x-auto rounded-2 border border-line-1">
        <table className="w-full text-xs" data-testid="audit-diff">
          <thead>
            <tr className="border-b border-line-1 bg-bg-2 text-left text-fg-3">
              <th className="px-3 py-1.5 font-normal">{t("admin.audit.field")}</th>
              <th className="px-3 py-1.5 font-normal">{t("admin.audit.before")}</th>
              <th className="px-3 py-1.5 font-normal">{t("admin.audit.after")}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => {
              const moved = changed.includes(r);
              return (
                <tr key={r.path} className="border-b border-line-1 align-top last:border-0">
                  <td className="px-3 py-1.5 font-mono text-fg-2">{r.path}</td>
                  <td className={moved ? "break-all bg-danger/10 px-3 py-1.5 font-mono text-fg-1" : "break-all px-3 py-1.5 font-mono text-fg-3"}>{show(r.before)}</td>
                  <td className={moved ? "break-all bg-success/10 px-3 py-1.5 font-mono text-fg-1" : "break-all px-3 py-1.5 font-mono text-fg-3"}>{show(r.after)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {same.length > 0 && (
          <button type="button" className="w-full border-t border-line-1 py-1.5 text-xs text-info hover:bg-bg-2" onClick={() => setAll(!all)}>
            {all ? "▲" : t("admin.audit.unchanged", { n: same.length })}
          </button>
        )}
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-1.5">
      {change.title && <div className="font-mono text-xs text-fg-2">{change.title}</div>}
      {body}
    </div>
  );
}
