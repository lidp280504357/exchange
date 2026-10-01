import { errorText } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, DataTable, ErrorState, Input, Switch, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";

import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction } from "../kit/actions";
import { TimeText } from "../kit/format";
import { Page } from "../kit/Page";

type Flag = AdminSchemas["Flag"];

/**
 * Risk and feature flags (design §10.3): every flag with its rules and
 * last change; switching one needs a confirmation with a reason. Rules
 * stay with exchangectl flags set.
 */
export default function Flags({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const [q, setQ] = useState("");
  const [pending, setPending] = useState<Flag | null>(null);
  const flags = useQuery({ queryKey: ["admin", "flags"], queryFn: async () => adminData(await adminApi.GET("/admin/v1/flags")).items });
  const writable = can(admin, "flags.write");
  const columns = useMemo<ColumnDef<Flag, unknown>[]>(
    () => [
      {
        id: "enabled",
        header: "",
        meta: { width: 64 },
        cell: ({ row }) => (
          <Switch
            checked={row.original.enabled}
            disabled={!writable}
            onCheckedChange={() => setPending(row.original)}
            size="sm"
            label={<span className="sr-only">{row.original.key}</span>}
          />
        ),
      },
      { accessorKey: "key", header: t("admin.risk.key"), cell: ({ row }) => <span className="font-mono text-xs">{row.original.key}</span> },
      { accessorKey: "description", header: t("admin.risk.description"), cell: ({ row }) => <span className="text-sm text-fg-2">{row.original.description}</span> },
      { id: "rules", header: t("admin.risk.rules"), cell: ({ row }) => <Rules rules={row.original.rules} /> },
      {
        id: "updated",
        header: t("admin.risk.updatedBy"),
        cell: ({ row }) => (
          <span className="flex flex-col text-xs">
            <span>{row.original.updated_by || "—"}</span>
            <TimeText value={row.original.updated_at} />
          </span>
        ),
      },
    ],
    [t, writable],
  );
  const shown = (flags.data ?? []).filter((f) => !q || f.key.includes(q.trim()) || f.description?.includes(q.trim()));
  return (
    <Page title={t("admin.risk.flags")} help={t("admin.risk.rulesHint")} actions={<Input size="sm" value={q} onValueChange={setQ} placeholder={t("admin.common.search")} containerClassName="w-56" clearable onClear={() => setQ("")} />}>
      {flags.isError ? (
        <ErrorState message={errorText(flags.error)} onRetry={() => void flags.refetch()} />
      ) : (
        <DataTable columns={columns} data={shown} getRowId={(f) => f.key} loading={flags.isPending} density="compact" />
      )}
      {pending && (
        <DangerAction
          open
          onOpenChange={(o) => !o && setPending(null)}
          danger={!pending.enabled}
          title={t("admin.risk.switchTitle", { action: pending.enabled ? t("admin.risk.turnOff") : t("admin.risk.turnOn"), key: pending.key })}
          description={pending.description}
          target={<span className="font-mono">{pending.key}</span>}
          confirmWord={pending.key.split(".").pop() ?? pending.key}
          run={async (reason) =>
            adminData(await adminApi.PUT("/admin/v1/flags/{key}", { params: { path: { key: pending.key } }, body: { enabled: !pending.enabled, reason } }))
          }
          success={t("admin.risk.switched", { key: pending.key, state: pending.enabled ? t("admin.risk.off") : t("admin.risk.on") })}
          invalidate={[["admin", "flags"]]}
        />
      )}
    </Page>
  );
}

/** Rules shows a flag's dimensions: allow lists and deny lists. */
function Rules({ rules }: { rules: unknown }) {
  if (!rules || typeof rules !== "object") return <span className="text-fg-3">—</span>;
  const entries = Object.entries(rules as Record<string, { allow?: string[]; deny?: string[] } | null>).filter(([, v]) => v);
  if (entries.length === 0) return <span className="text-fg-3">—</span>;
  return (
    <span className="flex flex-col gap-0.5 text-xs">
      {entries.map(([dim, v]) => (
        <span key={dim} className="flex flex-wrap items-center gap-1">
          <Badge tone="neutral">{dim}</Badge>
          {v?.allow?.length ? <span title={v.allow.join(", ")}>allow {v.allow.length > 4 ? `${v.allow.slice(0, 4).join(", ")} +${v.allow.length - 4}` : v.allow.join(", ")}</span> : null}
          {v?.deny?.length ? <span className="text-danger">deny {v.deny.join(", ")}</span> : null}
        </span>
      ))}
    </span>
  );
}
