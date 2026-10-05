import { ApiError, dec, errorText } from "@exchange/core";
import {
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  useReactTable,
  type ColumnDef,
  type OnChangeFn,
  type Row,
  type RowSelectionState,
  type SortingState,
} from "@tanstack/react-table";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ArrowDown, ArrowUp, ChevronsUpDown } from "lucide-react";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Checkbox } from "../components/Checkbox";
import { Progress } from "../components/Progress";
import { Skeleton } from "../components/Skeleton";
import { Spinner } from "../components/Spinner";
import { EmptyState, ErrorState } from "../components/States";
import { cn } from "../lib/cn";
import { titleCutCells } from "./cutTitles";

// The table of every list (design §10.2): rows keyed by record ID (never
// by position, the old console's bug A1), sortable headers, loading /
// empty / error states, optional selection, and virtual scrolling that
// keeps a thousand rows at 60 fps. Columns are TanStack column
// definitions; `meta` (DataColumnMeta) aligns and sizes them. A cell that
// cuts its text short carries the whole text as a title.

/** Per-column layout, set as `meta` on a column definition. */
export type DataColumnMeta = {
  align?: "left" | "center" | "right";
  /** A fixed width (px or CSS length). */
  width?: number | string;
  /** Classes of the column's cells. */
  className?: string;
  /** Classes of the column's header. */
  headerClassName?: string;
};

export type DataTableDensity = "compact" | "normal";

export type DataTableProps<T> = {
  /** Column definitions (memoize them); value types differ per column, hence `any`. */
  columns: ColumnDef<T, any>[];
  data: T[];
  /** The record's ID: rows are keyed by it, never by index. */
  getRowId: (row: T) => string;
  /** First load: skeleton rows (later loads keep the rows and show a bar). */
  loading?: boolean;
  loadingRows?: number;
  /** A failed load: shows ErrorState (ApiError codes are localized). */
  error?: unknown;
  errorMessage?: ReactNode;
  onRetry?: () => void;
  /** Shown when there are no rows; defaults to EmptyState. */
  empty?: ReactNode;
  sorting?: SortingState;
  defaultSorting?: SortingState;
  onSortingChange?: (sorting: SortingState) => void;
  /** Sorting happens on the server: headers only report the choice. */
  manualSorting?: boolean;
  /** Adds a checkbox column (bulk export in the console). */
  selectable?: boolean;
  rowSelection?: RowSelectionState;
  onRowSelectionChange?: (selection: RowSelectionState) => void;
  onRowClick?: (row: T) => void;
  /** Marks a row (the one open in a detail drawer). */
  isRowActive?: (row: T) => boolean;
  rowClassName?: (row: T) => string | undefined;
  density?: DataTableDensity;
  /** Virtual scrolling (needs a fixed height; default 480 px). */
  virtual?: boolean;
  /** Height of the scroll area; without it the page scrolls. */
  height?: number | string;
  stickyHeader?: boolean;
  /**
   * Offset of the sticky header when the page scrolls (under a top bar): px
   * or a CSS length ("3.5rem"). Without a `height`, a table with an offset
   * clips instead of scrolling sideways (it must fit its width): a scroll
   * container between the header and the page would hold the header by
   * that offset inside it, over the first rows. Its ancestors must not
   * clip with overflow: hidden either (overflow: clip is fine).
   */
  stickyTop?: number | string;
  /** Called near the end of the rows (infinite scroll). */
  onEndReached?: () => void;
  loadingMore?: boolean;
  /** false shows "no more" under the rows. */
  hasMore?: boolean;
  className?: string;
  "aria-label"?: string;
};

/** sortDecimal sorts a column of decimal strings exactly (set it as a column's sortingFn). */
export function sortDecimal<T>(a: Row<T>, b: Row<T>, id: string): number {
  const x = a.getValue<unknown>(id);
  const y = b.getValue<unknown>(id);
  const ok = (v: unknown): v is string => typeof v === "string" && dec.isDecimal(v);
  if (!ok(x)) return ok(y) ? -1 : 0;
  if (!ok(y)) return 1;
  return dec.cmp(x, y);
}

const ROW_HEIGHT: Record<DataTableDensity, number> = { compact: 32, normal: 44 };
const PAD: Record<DataTableDensity, string> = { compact: "px-3 text-xs", normal: "px-4 text-sm" };
const ALIGN = { left: "text-left", center: "text-center", right: "text-right" } as const;
const JUSTIFY = { left: "justify-start", center: "justify-center", right: "justify-end" } as const;

function metaOf(meta: unknown): DataColumnMeta {
  return (meta as DataColumnMeta | undefined) ?? {};
}

function widthStyle(m: DataColumnMeta): CSSProperties | undefined {
  return m.width === undefined ? undefined : { width: m.width, minWidth: m.width, maxWidth: m.width };
}

/**
 * DataTable renders records with TanStack Table. Give it `getRowId` (the
 * record ID) and memoized `columns`; set `virtual` and a `height` for long
 * lists, `onEndReached` for infinite scroll.
 */
export function DataTable<T>({
  columns, data, getRowId, loading, loadingRows = 8, error, errorMessage, onRetry, empty, sorting, defaultSorting, onSortingChange,
  manualSorting, selectable, rowSelection, onRowSelectionChange, onRowClick, isRowActive, rowClassName, density = "normal", virtual,
  height, stickyHeader = true, stickyTop = 0, onEndReached, loadingMore, hasMore, className, "aria-label": ariaLabel,
}: DataTableProps<T>) {
  const { t } = useTranslation();
  const [innerSorting, setInnerSorting] = useState<SortingState>(defaultSorting ?? []);
  const [innerSelection, setInnerSelection] = useState<RowSelectionState>({});
  const sortingState = sorting ?? innerSorting;
  const selectionState = rowSelection ?? innerSelection;

  const handleSorting: OnChangeFn<SortingState> = (u) => {
    const next = typeof u === "function" ? u(sortingState) : u;
    if (sorting === undefined) setInnerSorting(next);
    onSortingChange?.(next);
  };
  const handleSelection: OnChangeFn<RowSelectionState> = (u) => {
    const next = typeof u === "function" ? u(selectionState) : u;
    if (rowSelection === undefined) setInnerSelection(next);
    onRowSelectionChange?.(next);
  };

  const allColumns = useMemo(() => {
    if (!selectable) return columns;
    const select: ColumnDef<T, unknown> = {
      id: "__select",
      enableSorting: false,
      meta: { width: 44 } satisfies DataColumnMeta,
      header: ({ table }) => (
        <Checkbox
          aria-label={t("ui.selectAll")}
          checked={table.getIsAllRowsSelected() ? true : table.getIsSomeRowsSelected() ? "indeterminate" : false}
          onCheckedChange={(c) => table.toggleAllRowsSelected(c)}
        />
      ),
      // Compact rows (32 px) are lower than the box's 44 px touch ring, which
      // would reach into the next rows' boxes: there the ring goes and the
      // whole cell takes the tap instead (review BG).
      cell: ({ row }) => (
        <Checkbox
          aria-label={t("ui.selectRow")}
          checked={row.getIsSelected()}
          disabled={!row.getCanSelect()}
          onCheckedChange={(c) => row.toggleSelected(c)}
          hitArea={density !== "compact"}
        />
      ),
    };
    return [select, ...columns];
  }, [selectable, columns, t, density]);

  const table = useReactTable({
    data,
    columns: allColumns,
    getRowId: (row) => getRowId(row),
    state: { sorting: sortingState, rowSelection: selectionState },
    onSortingChange: handleSorting,
    onRowSelectionChange: handleSelection,
    enableRowSelection: Boolean(selectable),
    manualSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: manualSorting ? undefined : getSortedRowModel(),
  });

  const rows = table.getRowModel().rows;
  const colCount = table.getVisibleLeafColumns().length;
  const rowHeight = ROW_HEIGHT[density];
  const scrollRef = useRef<HTMLDivElement>(null);
  const tableRef = useRef<HTMLTableElement>(null);
  const sentinelRef = useRef<HTMLDivElement>(null);
  const areaHeight = height ?? (virtual ? 480 : undefined);

  const virtualizer = useVirtualizer({
    count: virtual ? rows.length : 0,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowHeight,
    overscan: 12,
  });
  const items = virtual ? virtualizer.getVirtualItems() : [];
  const padTop = items[0]?.start ?? 0;
  const padBottom = virtual ? Math.max(0, virtualizer.getTotalSize() - (items[items.length - 1]?.end ?? 0)) : 0;

  // Infinite scroll: the latest callback, asked once per row count.
  const endRef = useRef(onEndReached);
  endRef.current = onEndReached;
  const askedAt = useRef(-1);
  const blocked = Boolean(loading || loadingMore || error) || hasMore === false;
  const reachEnd = useCallback(() => {
    if (blocked || !endRef.current || askedAt.current === rows.length) return;
    askedAt.current = rows.length;
    endRef.current();
  }, [blocked, rows.length]);

  const lastIndex = items[items.length - 1]?.index ?? -1;
  useEffect(() => {
    if (virtual && rows.length > 0 && lastIndex >= rows.length - 6) reachEnd();
  }, [virtual, lastIndex, rows.length, reachEnd]);

  useEffect(() => {
    if (virtual || !onEndReached || typeof IntersectionObserver === "undefined") return;
    const target = sentinelRef.current;
    if (!target) return;
    const io = new IntersectionObserver((entries) => entries.some((e) => e.isIntersecting) && reachEnd(), {
      root: areaHeight !== undefined ? scrollRef.current : null,
      rootMargin: "200px",
    });
    io.observe(target);
    return () => io.disconnect();
  }, [virtual, onEndReached, areaHeight, reachEnd]);

  // Titles on cut cells: measured when the rows shown, their values, order
  // or columns change and when the table's width does, at most once a frame
  // (not on every render: a hover or a selection changes no text), and not
  // while a load runs (once it is over).
  const titleFrame = useRef(0);
  const loadingRef = useRef(loading);
  loadingRef.current = loading;
  const retitle = useCallback(() => {
    cancelAnimationFrame(titleFrame.current);
    titleFrame.current = requestAnimationFrame(() => {
      if (tableRef.current && !loadingRef.current) titleCutCells(tableRef.current);
    });
  }, []);
  const firstShown = items[0]?.index ?? 0;
  useEffect(() => retitle(), [retitle, loading, rows, allColumns, density, firstShown, lastIndex]);
  useEffect(() => {
    const table = tableRef.current;
    if (!table || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(retitle);
    ro.observe(table);
    return () => {
      ro.disconnect();
      cancelAnimationFrame(titleFrame.current);
    };
  }, [retitle]);

  const clickRef = useRef(onRowClick);
  clickRef.current = onRowClick;
  const handleRowClick = useCallback((row: T) => clickRef.current?.(row), []);

  const showSkeleton = loading && data.length === 0;
  const showError = !showSkeleton && Boolean(error) && data.length === 0;
  const showEmpty = !showSkeleton && !showError && rows.length === 0;
  const pad = PAD[density];
  const visible = virtual ? items.map((v) => ({ row: rows[v.index], index: v.index })) : rows.map((row, index) => ({ row, index }));
  // A header stuck to the page needs no scroll container in between, and
  // overflow-x: auto makes one of the wrapper (for both axes); clip does not.
  const pageSticky = stickyHeader && stickyTop !== 0 && stickyTop !== "" && areaHeight === undefined;

  return (
    <div className={cn("relative w-full", className)}>
      {loading && data.length > 0 && <Progress value={null} size="xs" className="absolute inset-x-0 top-0 z-[var(--z-sticky)]" aria-label={t("common.loading")} />}
      <div
        ref={scrollRef}
        className={cn("w-full", pageSticky ? "overflow-x-clip" : "overflow-x-auto", areaHeight !== undefined && "overflow-y-auto overscroll-contain")}
        style={areaHeight !== undefined ? { height: areaHeight } : undefined}
      >
        <table ref={tableRef} aria-label={ariaLabel} aria-busy={loading || undefined} className={cn("w-full border-collapse", virtual && "table-fixed")}>
          <thead>
            {table.getHeaderGroups().map((group) => (
              <tr key={group.id}>
                {group.headers.map((header) => {
                  const m = metaOf(header.column.columnDef.meta);
                  const align = m.align ?? "left";
                  const sortable = header.column.getCanSort();
                  const dir = header.column.getIsSorted();
                  return (
                    <th
                      key={header.id}
                      scope="col"
                      aria-sort={dir === "asc" ? "ascending" : dir === "desc" ? "descending" : sortable ? "none" : undefined}
                      style={{ ...widthStyle(m), ...(stickyHeader ? { top: stickyTop } : undefined) }}
                      className={cn(
                        "h-9 whitespace-nowrap border-b border-line-1 bg-bg-1 font-normal text-fg-3",
                        pad,
                        ALIGN[align],
                        stickyHeader && "sticky z-[var(--z-sticky)]",
                        m.headerClassName,
                      )}
                    >
                      {header.isPlaceholder ? null : sortable ? (
                        <button
                          type="button"
                          onClick={header.column.getToggleSortingHandler()}
                          className={cn("inline-flex w-full items-center gap-1 hover:text-fg-1", JUSTIFY[align], dir && "text-fg-1")}
                        >
                          {flexRender(header.column.columnDef.header, header.getContext())}
                          {dir === "asc" ? (
                            <ArrowUp size={12} aria-label={t("ui.sortAsc")} />
                          ) : dir === "desc" ? (
                            <ArrowDown size={12} aria-label={t("ui.sortDesc")} />
                          ) : (
                            <ChevronsUpDown size={12} aria-hidden className="opacity-50" />
                          )}
                        </button>
                      ) : (
                        flexRender(header.column.columnDef.header, header.getContext())
                      )}
                    </th>
                  );
                })}
              </tr>
            ))}
          </thead>
          <tbody>
            {showSkeleton &&
              Array.from({ length: loadingRows }, (_, i) => (
                <tr key={`skeleton-${i}`} style={{ height: rowHeight }} className="border-b border-line-1">
                  {table.getVisibleLeafColumns().map((col) => (
                    <td key={col.id} className={pad}>
                      <Skeleton className={cn("h-3.5", i % 3 === 0 ? "w-3/4" : i % 3 === 1 ? "w-1/2" : "w-2/3")} />
                    </td>
                  ))}
                </tr>
              ))}
            {showError && (
              <tr>
                <td colSpan={colCount}>
                  <ErrorState
                    compact
                    message={errorMessage ?? errorText(error)}
                    traceId={error instanceof ApiError ? error.traceId : undefined}
                    onRetry={onRetry}
                  />
                </td>
              </tr>
            )}
            {showEmpty && (
              <tr>
                <td colSpan={colCount}>{empty ?? <EmptyState compact />}</td>
              </tr>
            )}
            {padTop > 0 && (
              <tr aria-hidden>
                <td colSpan={colCount} style={{ height: padTop, padding: 0 }} />
              </tr>
            )}
            {!showSkeleton &&
              visible.map(({ row, index }) =>
                row ? (
                  <DataRow
                    key={row.id}
                    row={row}
                    index={index}
                    columns={allColumns}
                    selected={row.getIsSelected()}
                    active={isRowActive?.(row.original) ?? false}
                    extraClass={rowClassName?.(row.original)}
                    clickable={Boolean(onRowClick)}
                    onClick={handleRowClick}
                    height={rowHeight}
                    pad={pad}
                    animate={!virtual && index < 12}
                  />
                ) : null,
              )}
            {padBottom > 0 && (
              <tr aria-hidden>
                <td colSpan={colCount} style={{ height: padBottom, padding: 0 }} />
              </tr>
            )}
          </tbody>
        </table>
        {!virtual && onEndReached && <div ref={sentinelRef} aria-hidden className="h-px" />}
        {(loadingMore || (hasMore === false && rows.length > 0 && onEndReached)) && (
          <div className="flex h-10 items-center justify-center gap-2 text-xs text-fg-3">
            {loadingMore ? (
              <>
                <Spinner size={12} /> {t("common.loading")}
              </>
            ) : (
              t("ui.noMore")
            )}
          </div>
        )}
      </div>
    </div>
  );
}

type DataRowProps<T> = {
  row: Row<T>;
  index: number;
  /** The column definitions: a new array re-renders the row. */
  columns: ColumnDef<T, any>[];
  selected: boolean;
  active: boolean;
  extraClass: string | undefined;
  clickable: boolean;
  onClick: (row: T) => void;
  height: number;
  pad: string;
  animate: boolean;
};

// DataRowImpl is one memoized row: it re-renders only when its record,
// its selection, its highlight or the columns change.
function DataRowImpl<T>({ row, index, selected, active, extraClass, clickable, onClick, height, pad, animate }: DataRowProps<T>) {
  return (
    <tr
      data-row-id={row.id}
      data-state={selected ? "selected" : undefined}
      aria-selected={selected || undefined}
      tabIndex={clickable ? 0 : undefined}
      onClick={clickable ? () => onClick(row.original) : undefined}
      onKeyDown={
        clickable
          ? (e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onClick(row.original);
              }
            }
          : undefined
      }
      style={{ height, ...(animate ? { animationDelay: `${index * 20}ms` } : undefined) }}
      className={cn(
        "border-b border-line-1 transition-colors duration-[var(--t-fast)] hover:bg-bg-2",
        clickable && "cursor-pointer",
        selected && "bg-brand-soft hover:bg-brand-soft",
        active && "bg-bg-2",
        animate && "animate-fade-up",
        extraClass,
      )}
    >
      {row.getVisibleCells().map((cell) => {
        const m = metaOf(cell.column.columnDef.meta);
        const isSelect = cell.column.id === "__select";
        return (
          <td
            key={cell.id}
            style={widthStyle(m)}
            onClick={
              isSelect
                ? (e) => {
                    e.stopPropagation();
                    // A tap beside the box toggles it too.
                    if (e.target === e.currentTarget && row.getCanSelect()) row.toggleSelected();
                  }
                : undefined
            }
            className={cn("overflow-hidden text-ellipsis whitespace-nowrap text-fg-1 tabular-nums", pad, ALIGN[m.align ?? "left"], m.className)}
          >
            {flexRender(cell.column.columnDef.cell, cell.getContext())}
          </td>
        );
      })}
    </tr>
  );
}

const DataRow = memo(DataRowImpl) as typeof DataRowImpl;

// The apps define columns with the same TanStack Table (one copy, no extra deps).
export { createColumnHelper, type ColumnDef, type Row, type RowSelectionState, type SortingState } from "@tanstack/react-table";
