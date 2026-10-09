import { ApiError } from "@exchange/core";
import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { Input, toast, type ColumnDef } from "@exchange/ui";

import { Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { errorToast } from "../../kit/actions";
import { EnumBadge, useEnum } from "../../kit/enums";
import { dayEnd, dayStart, FilterBar, options, useFilters } from "../../kit/filters";
import { IdText, TimeText, useOpenUser } from "../../kit/format";
import { ListTable, pageSize, useCursorList } from "../../kit/lists";
import { Page } from "../../kit/Page";
import { clean } from "../records/tables";
import { UserIdentity } from "./identity";
import { TagChips } from "./NotesTags";

type UserSummary = AdminSchemas["UserSummary"];
const KEYS = ["status", "region", "from", "to", "q"] as const;

/**
 * Users (design §10.3): find one by ID, email, phone or username, or browse the accounts by a keyword and filters
 * (A93); a row opens the user's page.
 */
export default function Users() {
  const { t } = useTranslation();
  const label = useEnum();
  const open = useOpenUser();
  const filters = useFilters(KEYS);
  const f = filters.values;
  // A keyword from the address or a saved view is checked as the search box checks it (A104): one the services would
  // refuse filters nothing, and the box says so.
  const keyword = f.q?.trim() ?? "";
  const keywordOk = keyword === "" || usableKeyword(keyword);
  const q = { status: f.status, region: f.region?.toUpperCase(), q: keywordOk ? keyword : "", from: dayStart(f.from ?? ""), to: dayEnd(f.to ?? "") };
  const list = useCursorList<UserSummary>(["admin", "users", q], async (cursor) =>
    adminData(await adminApi.GET("/admin/v1/users", { params: { query: { ...clean(q), status: (q.status || undefined) as never, cursor, limit: pageSize() } } })),
  );
  const columns = useMemo<ColumnDef<UserSummary, unknown>[]>(
    () => [
      { id: "user", header: t("admin.users.username"), cell: ({ row }) => <UserIdentity user={row.original} /> },
      { id: "id", header: t("admin.users.id"), cell: ({ row }) => <IdText value={row.original.id} chars={13} /> },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <EnumBadge group="userStatus" code={row.original.status} /> },
      { accessorKey: "region", header: t("admin.users.region") },
      { accessorKey: "language", header: t("admin.users.language") },
      { accessorKey: "kyc_level", header: t("admin.users.kyc") },
      { id: "tags", header: t("admin.user.tags"), cell: ({ row }) => (row.original.tags.length ? <TagChips tags={row.original.tags} /> : null) },
      { id: "created", header: t("admin.users.createdAt"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
    ],
    [t],
  );
  return (
    <Page title={t("admin.nav.users")}>
      <UserSearch keyword={keyword} keywordOk={keywordOk} onFound={open} onKeyword={(k) => filters.set({ q: k })} />
      <FilterBar
        page="users"
        filters={filters}
        extraKeys={["q"]}
        defs={[
          { key: "status", label: t("admin.users.filterStatus"), kind: "select", options: options(t("admin.common.all"), ["ACTIVE", "RISK_REVIEW", "FROZEN", "CLOSED"], (c) => label("userStatus", c)) },
          { key: "region", label: t("admin.users.filterRegion"), kind: "text", placeholder: t("admin.users.regionHint"), width: 100 },
          { key: "from", label: t("admin.users.filterFrom"), kind: "date" },
          { key: "to", label: t("admin.users.filterTo"), kind: "date" },
        ]}
      />
      <ListTable list={list} columns={columns} getRowId={(u) => u.id} onRowClick={(u) => open(u.id)} aria-label={t("admin.nav.users")} />
    </Page>
  );
}

/** invalid is an input no account could match (A93): over 254 characters, or with invisible ones. */
const invalid = (s: string) => [...s].length > 254 || /\p{Cc}/u.test(s);

/**
 * usableKeyword is a list's keyword as auth-service and user-service take it (B170): 2 to 64 characters, none invisible -
 * the search box's and the address's alike (A104, A106).
 */
const usableKeyword = (s: string) => [...s].length >= 2 && [...s].length <= 64 && !invalid(s);

/**
 * UserSearch (A93): on Enter, an exact ID, email, phone or username opens the user (B167); anything else filters the
 * list by it as a keyword (kept in the address, so views save it); only what no account could match is refused.
 */
function UserSearch({
  keyword, keywordOk, onFound, onKeyword,
}: { keyword: string; keywordOk: boolean; onFound: (id: string) => void; onKeyword: (q: string) => void }) {
  const { t } = useTranslation();
  const [q, setQ] = useState(keyword);
  const [busy, setBusy] = useState(false);
  useEffect(() => setQ(keyword), [keyword]);
  const find = async () => {
    const query = q.trim();
    if (!query) return onKeyword("");
    if (invalid(query)) return void toast.error(t("admin.users.searchInvalid"));
    setBusy(true);
    try {
      onFound(adminData(await adminApi.GET("/admin/v1/users/lookup", { params: { query: { q: query } } })).user.id);
    } catch (err) {
      // No such account: the input as the list's keyword, of 2 to 64 characters (B170).
      if (err instanceof ApiError && err.status === 404) {
        if (usableKeyword(query)) onKeyword(query);
        else toast.info(t("admin.users.keywordLength"));
      } else if (err instanceof ApiError && err.status === 400) toast.error(t("admin.users.searchInvalid"));
      else errorToast(err);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="flex max-w-xl flex-col gap-1">
      <Input
        value={q}
        onValueChange={setQ}
        onKeyDown={(e) => e.key === "Enter" && void find()}
        prefix={<Search size={16} className="text-fg-3" />}
        placeholder={t("admin.users.lookupHint")}
        disabled={busy}
        clearable
        onClear={() => {
          setQ("");
          onKeyword("");
        }}
        aria-label={t("admin.users.lookup")}
        data-testid="users-search"
      />
      {keyword && (
        <span className="text-xs text-fg-3" data-testid="users-keyword" data-ok={keywordOk}>
          {t("admin.users.keyword")}：<span className="font-medium text-fg-1">{keyword}</span>
          {t("admin.summary.clause")}
          {keywordOk ? t("admin.users.keywordHint") : <span className="text-warn-strong">{t("admin.users.keywordUnused")}</span>}
        </span>
      )}
    </div>
  );
}
