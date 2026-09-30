import "../test/setup";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TpSlDialog } from "./TpSlDialog";
import { estimatePnl, validateTpSl } from "./tpsl";

const ref = { MARK: "100", LAST: "102" };

describe("validateTpSl", () => {
  it("wants a long's take profit above and stop loss below the price", () => {
    expect(validateTpSl("LONG", { takeProfit: { price: "110", triggerType: "MARK" }, stopLoss: { price: "90", triggerType: "MARK" } }, ref)).toEqual({});
    expect(validateTpSl("LONG", { takeProfit: { price: "95", triggerType: "MARK" } }, ref)).toEqual({ takeProfit: "tpAbove" });
    expect(validateTpSl("LONG", { takeProfit: { price: "100", triggerType: "MARK" } }, ref)).toEqual({ takeProfit: "tpAbove" });
    expect(validateTpSl("LONG", { stopLoss: { price: "105", triggerType: "MARK" } }, ref)).toEqual({ stopLoss: "slBelow" });
  });

  it("reverses the rules for a short", () => {
    expect(validateTpSl("SHORT", { takeProfit: { price: "90", triggerType: "MARK" }, stopLoss: { price: "110", triggerType: "MARK" } }, ref)).toEqual({});
    expect(validateTpSl("SHORT", { takeProfit: { price: "110", triggerType: "MARK" } }, ref)).toEqual({ takeProfit: "tpBelow" });
    expect(validateTpSl("SHORT", { stopLoss: { price: "95", triggerType: "MARK" } }, ref)).toEqual({ stopLoss: "slAbove" });
  });

  it("compares each leg with the price its trigger watches", () => {
    // 101 is above the mark (100) but below the last price (102).
    expect(validateTpSl("LONG", { takeProfit: { price: "101", triggerType: "MARK" } }, ref)).toEqual({});
    expect(validateTpSl("LONG", { takeProfit: { price: "101", triggerType: "LAST" } }, ref)).toEqual({ takeProfit: "tpAbove" });
    expect(validateTpSl("SHORT", { stopLoss: { price: "101", triggerType: "LAST" } }, ref)).toEqual({ stopLoss: "slAbove" });
  });

  it("lets empty legs and unknown prices pass", () => {
    expect(validateTpSl("LONG", {}, ref)).toEqual({});
    expect(validateTpSl("LONG", { takeProfit: { price: "", triggerType: "MARK" } }, ref)).toEqual({});
    expect(validateTpSl("LONG", { takeProfit: { price: "50", triggerType: "LAST" } }, { MARK: "100" })).toEqual({});
  });

  it("estimates the PnL at a trigger", () => {
    expect(estimatePnl("LONG", "100", "110", "2")).toBe("20");
    expect(estimatePnl("SHORT", "100", "110", "2")).toBe("-20");
    expect(estimatePnl("SHORT", "100", "90.5", "0.5")).toBe("4.75");
    expect(estimatePnl("LONG", "100", "", "2")).toBeNull();
  });
});

describe("TpSlDialog", () => {
  it("flags a trigger on the wrong side and confirms a valid one", () => {
    const onConfirm = vi.fn();
    render(
      <TpSlDialog
        open
        onOpenChange={() => {}}
        side="LONG"
        entryPrice="95"
        quantity="2"
        markPrice="100"
        lastPrice="102"
        priceDecimals={1}
        onConfirm={onConfirm}
      />,
    );
    const confirm = screen.getByRole("button", { name: "Confirm" }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    expect(screen.getByText("Set a take profit or a stop loss")).toBeTruthy();

    const tp = screen.getByLabelText("Take profit Trigger price");
    fireEvent.change(tp, { target: { value: "90" } });
    expect(screen.getByText("Take profit must be above the current Mark price")).toBeTruthy();
    expect(confirm.disabled).toBe(true);

    fireEvent.change(tp, { target: { value: "110" } });
    expect(screen.queryByText("Take profit must be above the current Mark price")).toBeNull();
    expect(screen.getByText("+30.00 USDT")).toBeTruthy();

    const sl = screen.getByLabelText("Stop loss Trigger price");
    fireEvent.change(sl, { target: { value: "101" } });
    expect(screen.getByText("Stop loss must be below the current Mark price")).toBeTruthy();
    fireEvent.change(sl, { target: { value: "92.5" } });

    expect(confirm.disabled).toBe(false);
    fireEvent.click(confirm);
    expect(onConfirm).toHaveBeenCalledWith({
      takeProfit: { price: "110", triggerType: "MARK" },
      stopLoss: { price: "92.5", triggerType: "MARK" },
    });
  });

  it("checks a short against the last price when asked to", () => {
    render(
      <TpSlDialog open onOpenChange={() => {}} side="SHORT" markPrice="100" lastPrice="102" priceDecimals={1} onConfirm={() => {}} />,
    );
    const radios = screen.getAllByRole("radio", { name: "Last price" });
    const first = radios[0];
    if (!first) throw new Error("no trigger switch");
    fireEvent.click(first);
    fireEvent.change(screen.getByLabelText("Take profit Trigger price"), { target: { value: "101" } });
    expect(screen.queryByText("Take profit must be below the current Last price")).toBeNull();
    fireEvent.change(screen.getByLabelText("Take profit Trigger price"), { target: { value: "103" } });
    expect(screen.getByText("Take profit must be below the current Last price")).toBeTruthy();
  });
});
