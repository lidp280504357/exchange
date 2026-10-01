import { DataTable, type DataTableProps } from "@exchange/ui";
import { useInfiniteQuery, useQuery, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

// The console's lists (design §10.2): the server pages with an opaque
// cursor, 50 at a time, loaded as the page scrolls; lists do not poll
// whole, a light probe of the newest record offers "new data" instead.

export type Page<T> = { items: T[]; next_cursor?: string | null };

export const PAGE_SIZE = 50;

export type CursorList<T> = ReturnType<typeof useCursorList<T>>;

/** useCursorList pages through a list with its cursor. */
export function useCursorList<T>(key: QueryKey, fetchPage: (cursor: string | undefined) => Promise<Page<T>>, enabled = true) {
  const query = useInfiniteQuery({
    queryKey: key,
    queryFn: ({ pageParam }) => fetchPage(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last: Page<T>) => last.next_cursor ?? undefined,
    enabled,
  });
  const rows = useMemo(() => query.data?.pages.flatMap((p) => p.items) ?? [], [query.data]);
  const loadMore = () => {
    if (query.hasNextPage && !query.isFetchingNextPage) void query.fetchNextPage();
  };
  return { rows, query, loadMore, key };
}

/**
 * useNewer asks every 15 seconds for the newest record's ID; true when it
 * is not the first row shown (the list is behind).
 */
export function useNewer(key: QueryKey, newest: () => Promise<string | undefined>, shownFirst: string | undefined, enabled = true): boolean {
  const probe = useQuery({ queryKey: [...key, "newest"], queryFn: newest, refetchInterval: 15_000, enabled: enabled && shownFirst !== undefined });
  return probe.data !== undefined && shownFirst !== undefined && probe.data !== shownFirst;
}

/** NewerBar offers to reload a list that has newer records. */
export function NewerBar({ listKey }: { listKey: QueryKey }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  return (
    <button
      type="button"
      onClick={() => void qc.resetQueries({ queryKey: listKey })}
      className="flex w-full items-center justify-center gap-2 rounded-2 border border-brand bg-brand-soft py-1.5 text-sm text-fg-1 hover:bg-bg-2"
    >
      <RefreshCw size={14} />
      {t("admin.common.newData")}
    </button>
  );
}

export type ListTableProps<T> = Omit<DataTableProps<T>, "data" | "loading" | "error" | "onRetry" | "onEndReached" | "loadingMore" | "hasMore"> & {
  list: CursorList<T>;
};

/** ListTable is a DataTable over a cursor list: skeleton, errors with the trace ID, loading on scroll. */
export function ListTable<T>({ list, columns, ...props }: ListTableProps<T>) {
  const q = list.query;
  // The server orders a paged list; sorting the rows loaded so far would mislead.
  const fixed = useMemo(() => columns.map((c) => ({ enableSorting: false, ...c })), [columns]);
  return (
    <DataTable
      {...props}
      columns={fixed}
      data={list.rows}
      loading={q.isPending || (q.isFetching && !q.isFetchingNextPage && list.rows.length > 0)}
      error={q.error}
      onRetry={() => void q.refetch()}
      onEndReached={list.loadMore}
      loadingMore={q.isFetchingNextPage}
      hasMore={q.hasNextPage}
      density={props.density ?? "compact"}
    />
  );
}

/** csvCell quotes a cell for CSV (RFC 4180). */
export function csvCell(v: unknown): string {
  const s = v === null || v === undefined ? "" : String(v);
  return /[",\n\r]/.test(s) ? `"${s.replaceAll('"', '""')}"` : s;
}

/** downloadCsv saves rows as a CSV file (UTF-8 with a BOM, so spreadsheets read Chinese right). */
export function downloadCsv<T>(name: string, columns: { header: string; value: (row: T) => unknown }[], rows: T[]) {
  const lines = [columns.map((c) => csvCell(c.header)).join(","), ...rows.map((r) => columns.map((c) => csvCell(c.value(r))).join(","))];
  const blob = new Blob(["\uFEFF" + lines.join("\r\n")], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
