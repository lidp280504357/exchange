import { ApiError } from "@exchange/core";
import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { Input, toast, type ColumnDef } from "@exchange/ui";

import { Search } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { errorToast } from "../../kit/actions";
import { EnumBadge, useEnum } from "../../kit/enums";
import { dayEnd, dayStart, FilterBar, options, useFilters } from "../../kit/filters";
import { IdText, TimeText, useOpenUser } from "../../kit/format";
import { ListTable, PAGE_SIZE, useCursorList } from "../../kit/lists";
import { Page } from "../../kit/Page";
import { clean } from "../records/tables";

type UserSummary = AdminSchemas["UserSummary"];
const KEYS = ["status", "region", "from", "to"] as const;

/** Users (design §10.3): find one by ID, email or phone, or browse the accounts with filters; a row opens the drawer. */
export default function Users() {
  const { t } = useTranslation();
  const label = useEnum();
  const open = useOpenUser();
  const filters = useFilters(KEYS);
  const f = filters.values;
  const q = { status: f.status, region: f.region?.toUpperCase(), from: dayStart(f.from ?? ""), to: dayEnd(f.to ?? "") };
  const list = useCursorList<UserSummary>(["admin", "users", q], async (cursor) =>
    adminData(await adminApi.GET("/admin/v1/users", { params: { query: { ...clean(q), status: (q.status || undefined) as never, cursor, limit: PAGE_SIZE } } })),
  );
  const columns = useMemo<ColumnDef<UserSummary, unknown>[]>(
    () => [
      { id: "id", header: t("admin.users.id"), cell: ({ row }) => <IdText value={row.original.id} chars={13} /> },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <EnumBadge group="userStatus" code={row.original.status} /> },
      { accessorKey: "region", header: t("admin.users.region") },
      { accessorKey: "language", header: t("admin.users.language") },
      { accessorKey: "kyc_level", header: t("admin.users.kyc") },
      { id: "created", header: t("admin.users.createdAt"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
    ],
    [t],
  );
  return (
    <Page title={t("admin.nav.users")}>
      <Lookup onFound={open} />
      <FilterBar
        page="users"
        filters={filters}
        defs={[
          { key: "status", label: t("admin.users.filterStatus"), kind: "select", options: options(t("admin.common.all"), ["ACTIVE", "RISK_REVIEW", "FROZEN", "CLOSED"], (c) => label("userStatus", c)) },
          { key: "region", label: t("admin.users.filterRegion"), kind: "text", placeholder: "SG", width: 100 },
          { key: "from", label: t("admin.users.filterFrom"), kind: "date" },
          { key: "to", label: t("admin.users.filterTo"), kind: "date" },
        ]}
      />
      <ListTable list={list} columns={columns} getRowId={(u) => u.id} onRowClick={(u) => open(u.id)} aria-label={t("admin.nav.users")} />
    </Page>
  );
}

/** Lookup opens the user an ID, email or phone number names. */
function Lookup({ onFound }: { onFound: (id: string) => void }) {
  const { t } = useTranslation();
  const [q, setQ] = useState("");
  const [busy, setBusy] = useState(false);
  const find = async () => {
    const query = q.trim();
    if (!query) return;
    setBusy(true);
    try {
      onFound(adminData(await adminApi.GET("/admin/v1/users/lookup", { params: { query: { q: query } } })).user.id);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) toast.info(t("admin.search.notFound", { q: query }));
      else errorToast(err);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Input
      value={q}
      onValueChange={setQ}
      onKeyDown={(e) => e.key === "Enter" && void find()}
      prefix={<Search size={16} className="text-fg-3" />}
      placeholder={t("admin.users.lookupHint")}
      disabled={busy}
      containerClassName="max-w-xl"
      aria-label={t("admin.users.lookup")}
    />
  );
}
