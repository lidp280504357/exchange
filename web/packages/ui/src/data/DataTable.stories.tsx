import { ApiError, dec, enumLabel, formatAmount, formatPrice } from "@exchange/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import type { ColumnDef, RowSelectionState } from "@tanstack/react-table";
import { useCallback, useMemo, useState } from "react";
import { Badge } from "../components/Badge";
import { Button } from "../components/Button";
import { DataTable, sortDecimal, type DataColumnMeta } from "./DataTable";
import { TimeText } from "./TimeText";

type Order = {
  id: string;
  time: string;
  symbol: string;
  side: "BUY" | "SELL";
  type: "LIMIT" | "MARKET";
  price: string;
  quantity: string;
  filled: string;
  status: "OPEN" | "PARTIALLY_FILLED" | "FILLED" | "CANCELED";
};

// A seeded generator: the same fake orders on every render.
function makeOrders(n: number, offset = 0): Order[] {
  let seed = 7 + offset;
  const next = () => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed;
  };
  const symbols = ["BTC-USDT", "ETH-USDT", "SOL-USDT", "DOGE-USDT"];
  const statuses: Order["status"][] = ["OPEN", "PARTIALLY_FILLED", "FILLED", "CANCELED"];
  const base = Date.parse("2026-09-30T10:00:00Z");
  return Array.from({ length: n }, (_, k) => {
    const i = k + offset;
    const qty = dec.div(String((next() % 50000) + 100), "10000", 4);
    const status = statuses[next() % statuses.length] ?? "OPEN";
    return {
      id: `ord-${String(i + 1).padStart(5, "0")}`,
      time: new Date(base - i * 61_000).toISOString(),
      symbol: symbols[next() % symbols.length] ?? "BTC-USDT",
      side: next() % 2 === 0 ? "BUY" : "SELL",
      type: next() % 4 === 0 ? "MARKET" : "LIMIT",
      price: dec.div(String(6_300_000 + (next() % 40_000)), "100", 2),
      quantity: qty,
      filled: status === "FILLED" ? qty : status === "PARTIALLY_FILLED" ? dec.div(qty, "2", 4) : "0",
      status,
    };
  });
}

const statusTone = { OPEN: "info", PARTIALLY_FILLED: "warn", FILLED: "success", CANCELED: "neutral" } as const;

function useColumns(onCancel?: (o: Order) => void): ColumnDef<Order, any>[] {
  return useMemo(
    () => [
      { accessorKey: "time", header: "时间", cell: (c) => <TimeText value={c.getValue<string>()} format="datetimeSeconds" />, meta: { width: 170 } satisfies DataColumnMeta },
      { accessorKey: "symbol", header: "交易对", meta: { width: 110 } satisfies DataColumnMeta },
      {
        accessorKey: "side",
        header: "方向",
        cell: (c) => <span className={c.getValue() === "BUY" ? "text-up" : "text-down"}>{enumLabel(c.getValue<string>())}</span>,
        meta: { width: 70 } satisfies DataColumnMeta,
      },
      { accessorKey: "type", header: "类型", cell: (c) => enumLabel(c.getValue<string>()), meta: { width: 70 } satisfies DataColumnMeta },
      {
        accessorKey: "price",
        header: "价格",
        sortingFn: sortDecimal,
        cell: (c) => formatPrice(c.getValue<string>(), 2),
        meta: { align: "right", width: 110 } satisfies DataColumnMeta,
      },
      {
        accessorKey: "quantity",
        header: "数量",
        sortingFn: sortDecimal,
        cell: (c) => formatAmount(c.getValue<string>(), 4),
        meta: { align: "right", width: 100 } satisfies DataColumnMeta,
      },
      {
        accessorKey: "filled",
        header: "已成交",
        sortingFn: sortDecimal,
        cell: (c) => formatAmount(c.getValue<string>(), 4),
        meta: { align: "right", width: 100 } satisfies DataColumnMeta,
      },
      {
        accessorKey: "status",
        header: "状态",
        cell: (c) => {
          const s = c.getValue<Order["status"]>();
          return (
            <Badge tone={statusTone[s]} dot>
              {enumLabel(s)}
            </Badge>
          );
        },
        meta: { width: 110 } satisfies DataColumnMeta,
      },
      {
        id: "action",
        header: "操作",
        enableSorting: false,
        cell: (c) =>
          c.row.original.status === "OPEN" || c.row.original.status === "PARTIALLY_FILLED" ? (
            <Button
              size="sm"
              variant="ghost"
              className="h-6 px-2 text-brand"
              onClick={(e) => {
                e.stopPropagation();
                onCancel?.(c.row.original);
              }}
            >
              撤单
            </Button>
          ) : null,
        meta: { align: "right", width: 80 } satisfies DataColumnMeta,
      },
    ],
    [onCancel],
  );
}

const meta = {
  title: "Data/DataTable",
  component: DataTable<Order>,
  args: { columns: [], data: [], getRowId: (o: Order) => o.id },
} satisfies Meta<typeof DataTable<Order>>;
export default meta;

type Story = StoryObj<typeof meta>;

/** Sortable headers (decimals sort exactly), row click, a row action. */
export const Basic: Story = {
  render: () => {
    const [data, setData] = useState(() => makeOrders(20));
    const [picked, setPicked] = useState<string>();
    const columns = useColumns(useCallback((o: Order) => setData((all) => all.filter((x) => x.id !== o.id)), []));
    return (
      <div className="w-[1000px]">
        <DataTable
          columns={columns}
          data={data}
          getRowId={(o) => o.id}
          onRowClick={(o) => setPicked(o.id)}
          isRowActive={(o) => o.id === picked}
          aria-label="Orders"
        />
        <p className="mt-2 text-xs text-fg-3">picked: {picked ?? "—"}</p>
      </div>
    );
  },
};

/** 1000 rows with virtual scrolling: only the visible rows are in the DOM. */
export const Virtual1000: Story = {
  render: () => {
    const data = useMemo(() => makeOrders(1000), []);
    const columns = useColumns();
    return (
      <div className="w-[1000px] rounded-2 border border-line-1">
        <DataTable columns={columns} data={data} getRowId={(o) => o.id} virtual height={480} density="compact" aria-label="Orders" />
      </div>
    );
  },
};

/** Infinite scroll: 50 more rows each time the end comes into view. */
export const InfiniteScroll: Story = {
  render: () => {
    const [data, setData] = useState(() => makeOrders(50));
    const [loadingMore, setLoadingMore] = useState(false);
    const columns = useColumns();
    const more = () => {
      setLoadingMore(true);
      setTimeout(() => {
        setData((all) => [...all, ...makeOrders(50, all.length)]);
        setLoadingMore(false);
      }, 600);
    };
    return (
      <div className="w-[1000px] rounded-2 border border-line-1">
        <DataTable
          columns={columns}
          data={data}
          getRowId={(o) => o.id}
          height={420}
          onEndReached={more}
          loadingMore={loadingMore}
          hasMore={data.length < 300}
          aria-label="Orders"
        />
      </div>
    );
  },
};

export const Selection: Story = {
  render: () => {
    const data = useMemo(() => makeOrders(12), []);
    const [sel, setSel] = useState<RowSelectionState>({});
    const columns = useColumns();
    return (
      <div className="w-[1040px]">
        <DataTable columns={columns} data={data} getRowId={(o) => o.id} selectable rowSelection={sel} onRowSelectionChange={setSel} />
        <p className="mt-2 text-xs text-fg-3">selected: {Object.keys(sel).join(", ") || "—"}</p>
      </div>
    );
  },
};

/**
 * A narrow table: a cell that cuts its text short carries it whole as a
 * title (hover one); a cut label beside a badge titles the label alone.
 */
export const CutCells: Story = {
  render: () => {
    const data = useMemo(() => makeOrders(6), []);
    const base = useColumns();
    const columns = useMemo<ColumnDef<Order, any>[]>(
      () => [
        ...base.slice(0, 2),
        {
          id: "note",
          header: "备注",
          enableSorting: false,
          cell: (c) => (
            <span className="flex max-w-[200px] items-center gap-1.5">
              <span className="min-w-0 truncate">{`${c.row.original.symbol} 网格第 ${c.row.index + 1} 档：按最新成交价自动调整的限价委托`}</span>
              <Badge tone="info">网格</Badge>
            </span>
          ),
          meta: { width: 232 } satisfies DataColumnMeta,
        },
        ...base.slice(4, 6),
      ],
      [base],
    );
    return (
      <div className="w-[720px]">
        <DataTable columns={columns} data={data} getRowId={(o) => o.id} aria-label="Orders" />
        <p className="mt-2 text-xs text-fg-3">Hover a note: its title is the whole note, without the badge.</p>
      </div>
    );
  },
};

/** Loading (skeletons), error (with trace ID and retry) and empty. */
export const States: Story = {
  render: () => {
    const columns = useColumns();
    return (
      <div className="flex w-[1000px] flex-col gap-8">
        <DataTable columns={columns} data={[]} getRowId={(o) => o.id} loading loadingRows={4} />
        <DataTable
          columns={columns}
          data={[]}
          getRowId={(o) => o.id}
          error={new ApiError(503, "COMMON_UNAVAILABLE", "unavailable", {}, "5d65903b0824a7e47546ab8dbc6cf761")}
          onRetry={() => {}}
        />
        <DataTable columns={columns} data={[]} getRowId={(o) => o.id} />
      </div>
    );
  },
};
