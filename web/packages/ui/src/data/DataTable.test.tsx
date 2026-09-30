import "../test/setup";
import { ApiError } from "@exchange/core";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ColumnDef } from "@tanstack/react-table";
import { describe, expect, it, vi } from "vitest";
import { DataTable, sortDecimal } from "./DataTable";

type Row = { id: string; name: string; amount: string };

const columns: ColumnDef<Row, any>[] = [
  { accessorKey: "name", header: "Name" },
  { accessorKey: "amount", header: "Amount", sortingFn: sortDecimal },
];

const data: Row[] = [
  { id: "ord-a", name: "Alpha", amount: "10" },
  { id: "ord-b", name: "Beta", amount: "9.5" },
  { id: "ord-c", name: "Gamma", amount: "100" },
];

const rowIds = () => Array.from(document.querySelectorAll("tbody tr[data-row-id]")).map((tr) => tr.getAttribute("data-row-id"));
const rowOf = (id: string) => document.querySelector(`tbody tr[data-row-id="${id}"]`);

describe("DataTable", () => {
  it("keys rows by getRowId, so a row keeps its element when the order changes", () => {
    const { rerender } = render(<DataTable columns={columns} data={data} getRowId={(r) => r.id} />);
    expect(rowIds()).toEqual(["ord-a", "ord-b", "ord-c"]);
    const beta = rowOf("ord-b");
    expect(beta?.textContent).toContain("Beta");

    rerender(<DataTable columns={columns} data={[...data].reverse()} getRowId={(r) => r.id} />);
    expect(rowIds()).toEqual(["ord-c", "ord-b", "ord-a"]);
    // The same DOM node still shows Beta: nothing was re-used by position.
    expect(rowOf("ord-b")).toBe(beta);
    expect(beta?.textContent).toContain("Beta");

    rerender(<DataTable columns={columns} data={[{ id: "ord-z", name: "Zeta", amount: "1" }, ...data]} getRowId={(r) => r.id} />);
    expect(rowIds()).toEqual(["ord-z", "ord-a", "ord-b", "ord-c"]);
    expect(rowOf("ord-b")).toBe(beta);
  });

  it("keeps a selection on its record across reorders", () => {
    const onSel = vi.fn();
    const { rerender } = render(<DataTable columns={columns} data={data} getRowId={(r) => r.id} selectable onRowSelectionChange={onSel} />);
    const box = rowOf("ord-b")?.querySelector('[role="checkbox"]');
    if (!box) throw new Error("no checkbox");
    fireEvent.click(box);
    expect(onSel).toHaveBeenLastCalledWith({ "ord-b": true });
    rerender(<DataTable columns={columns} data={[...data].reverse()} getRowId={(r) => r.id} selectable onRowSelectionChange={onSel} />);
    expect(rowOf("ord-b")?.getAttribute("data-state")).toBe("selected");
    expect(rowOf("ord-a")?.getAttribute("data-state")).toBeNull();
  });

  it("sorts decimal strings as numbers", () => {
    render(<DataTable columns={columns} data={data} getRowId={(r) => r.id} />);
    fireEvent.click(screen.getByRole("button", { name: /Amount/ }));
    expect(rowIds()).toEqual(["ord-b", "ord-a", "ord-c"]);
    fireEvent.click(screen.getByRole("button", { name: /Amount/ }));
    expect(rowIds()).toEqual(["ord-c", "ord-a", "ord-b"]);
  });

  it("reports row clicks with the record", () => {
    const onRowClick = vi.fn();
    render(<DataTable columns={columns} data={data} getRowId={(r) => r.id} onRowClick={onRowClick} />);
    fireEvent.click(screen.getByText("Gamma"));
    expect(onRowClick).toHaveBeenCalledWith(data[2]);
  });

  it("shows loading, error and empty states", () => {
    const { rerender, container } = render(<DataTable columns={columns} data={[]} getRowId={(r: Row) => r.id} loading loadingRows={3} />);
    expect(container.querySelectorAll('tbody tr[style]').length).toBe(3);

    const onRetry = vi.fn();
    rerender(<DataTable columns={columns} data={[]} getRowId={(r: Row) => r.id} error={new ApiError(503, "COMMON_UNAVAILABLE", "x")} onRetry={onRetry} />);
    expect(screen.getByText("Temporarily unavailable, try again")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalled();

    rerender(<DataTable columns={columns} data={[]} getRowId={(r: Row) => r.id} empty={<span>No orders yet</span>} />);
    expect(screen.getByText("No orders yet")).toBeTruthy();
  });
});
