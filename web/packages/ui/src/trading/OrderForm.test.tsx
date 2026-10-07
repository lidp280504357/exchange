import "../test/setup";
import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { OrderForm, type OrderFormValues } from "./OrderForm";
import {
  estimateFee,
  maxQuantity,
  percentOfAmount,
  percentOfQuantity,
  quantityForTotal,
  snapToStep,
  totalOf,
  validateOrder,
  type Balances,
  type OrderSide,
  type OrderType,
  type PairRules,
} from "./orderMath";

const pair: PairRules = {
  base: "BTC",
  quote: "USDT",
  tickSize: "0.1",
  lotSize: "0.0001",
  minQuantity: "0.001",
  maxQuantity: "100",
  minNotional: "10",
  makerFeeRate: "0.001",
  takerFeeRate: "0.002",
};

const balances: Balances = { base: "0.53219", quote: "1000" };

describe("order arithmetic", () => {
  it("keeps total = price × quantity exact", () => {
    expect(totalOf("63214.5", "0.01")).toBe("632.145");
    expect(totalOf("0.1", "0.0001")).toBe("0.00001");
    expect(totalOf("", "1")).toBe("");
    expect(totalOf("100", "0")).toBe("");
  });
  it("derives the quantity from a total, down to the lot", () => {
    expect(quantityForTotal("1000", "63214.5", "0.0001")).toBe("0.0158");
    expect(quantityForTotal("1000", "50000", "0.0001")).toBe("0.02");
    expect(quantityForTotal("1000", "50000", "0.005")).toBe("0.02");
    expect(quantityForTotal("1000", "60000", "0.005")).toBe("0.015");
    expect(quantityForTotal("1000", "", "0.0001")).toBe("");
  });
  it("snaps prices and quantities to their steps", () => {
    expect(snapToStep("0.123456", "0.001")).toBe("0.123");
    expect(snapToStep("0.123", "0.005")).toBe("0.12");
    expect(snapToStep("63214.57", "0.1")).toBe("63214.5");
    expect(snapToStep("63214.7", "0.5")).toBe("63214.5");
    expect(snapToStep("", "0.1")).toBe("");
  });
  it("finds the most one can buy or sell", () => {
    expect(maxQuantity("BUY", "50000", balances, "0.0001")).toBe("0.02");
    expect(maxQuantity("SELL", "50000", balances, "0.0001")).toBe("0.5321");
    expect(maxQuantity("BUY", "", balances, "0.0001")).toBe("0");
    expect(maxQuantity("BUY", "50000", null, "0.0001")).toBe("");
  });
  it("turns slider percentages into amounts, rounded down", () => {
    expect(percentOfQuantity("0.02", 50, "0.0001")).toBe("0.01");
    expect(percentOfQuantity("0.5321", 25, "0.0001")).toBe("0.133");
    expect(percentOfQuantity("0.5321", 0, "0.0001")).toBe("");
    expect(percentOfAmount("1000", 33, 2)).toBe("330");
    expect(percentOfAmount("999.99", 50, 2)).toBe("499.99");
  });
  it("estimates the fee: buyers pay in base, sellers in quote, rounded up", () => {
    expect(estimateFee({ side: "BUY", type: "limit", rules: pair, price: "50000", quantity: "0.01", quoteAmount: "" })).toEqual({
      amount: "0.00001",
      asset: "BTC",
      rate: "0.001",
    });
    expect(estimateFee({ side: "SELL", type: "limit", rules: pair, price: "50000", quantity: "0.01", quoteAmount: "" })?.amount).toBe("0.5");
    expect(estimateFee({ side: "SELL", type: "market", rules: pair, price: "", quantity: "0.01", quoteAmount: "", lastPrice: "50000" })?.amount).toBe("1");
    expect(estimateFee({ side: "BUY", type: "limit", rules: pair, price: "50000", quantity: "0.0001", quoteAmount: "", baseDecimals: 6 })?.amount).toBe("0.000001");
    expect(estimateFee({ side: "BUY", type: "market", rules: pair, price: "", quantity: "", quoteAmount: "100", lastPrice: "50000" })?.amount).toBe("0.000004");
    expect(estimateFee({ side: "BUY", type: "limit", rules: pair, price: "50000", quantity: "", quoteAmount: "" })).toBeNull();
  });
});

describe("validateOrder", () => {
  const base = { rules: pair, available: balances, lastPrice: "50000" };
  it("asks for the missing fields", () => {
    const e = validateOrder({ ...base, side: "BUY", type: "limit", price: "", quantity: "", total: "" });
    expect(e.price?.key).toBe("priceRequired");
    expect(e.quantity?.key).toBe("quantityRequired");
    expect(validateOrder({ ...base, side: "BUY", type: "market", price: "", quantity: "", total: "" }).total?.key).toBe("totalRequired");
  });
  it("checks the minimum and maximum quantity", () => {
    const low = validateOrder({ ...base, side: "SELL", type: "limit", price: "50000", quantity: "0.0005", total: "25" });
    expect(low.quantity).toEqual({ key: "minQuantity", values: { value: "0.001", unit: "BTC" } });
    expect(validateOrder({ ...base, side: "SELL", type: "limit", price: "1", quantity: "101", total: "101" }).quantity?.key).toBe("maxQuantity");
  });
  it("checks the minimum notional", () => {
    const e = validateOrder({ ...base, side: "BUY", type: "limit", price: "5000", quantity: "0.001", total: "5" });
    expect(e.total).toEqual({ key: "minNotional", values: { value: "10", unit: "USDT" } });
    expect(validateOrder({ ...base, side: "BUY", type: "market", price: "", quantity: "", total: "9.99" }).total?.key).toBe("minNotional");
    expect(validateOrder({ ...base, side: "SELL", type: "market", price: "", quantity: "0.0001", total: "" }).quantity?.key).toBe("minQuantity");
    expect(validateOrder({ ...base, side: "SELL", type: "market", price: "", quantity: "0.001", total: "", lastPrice: "5000" }).quantity?.key).toBe("minNotional");
  });
  it("checks what the balance covers", () => {
    expect(validateOrder({ ...base, side: "BUY", type: "limit", price: "50000", quantity: "0.03", total: "1500" }).total?.key).toBe("insufficient");
    expect(validateOrder({ ...base, side: "SELL", type: "limit", price: "50000", quantity: "0.6", total: "30000" }).quantity?.key).toBe("insufficient");
    expect(validateOrder({ ...base, side: "BUY", type: "market", price: "", quantity: "", total: "1000.01" }).total?.key).toBe("insufficient");
    expect(validateOrder({ ...base, available: null, side: "BUY", type: "limit", price: "50000", quantity: "0.03", total: "1500" })).toEqual({});
  });
  it("accepts a valid order", () => {
    expect(validateOrder({ ...base, side: "BUY", type: "limit", price: "50000", quantity: "0.01", total: "500" })).toEqual({});
  });
});

function Harness({ onSubmit, initialType = "limit", initialSide = "BUY" }: { onSubmit: (o: OrderFormValues) => void; initialType?: OrderType; initialSide?: OrderSide }) {
  const [side, setSide] = useState<OrderSide>(initialSide);
  const [type, setType] = useState<OrderType>(initialType);
  return (
    <OrderForm
      side={side}
      onSideChange={setSide}
      type={type}
      onTypeChange={setType}
      pair={pair}
      available={balances}
      lastPrice="50000"
      signedIn
      onSubmit={onSubmit}
    />
  );
}

const field = (name: string) => screen.getByLabelText(name) as HTMLInputElement;

describe("OrderForm", () => {
  it("starts from the last price and keeps price × quantity = total", () => {
    render(<Harness onSubmit={() => {}} />);
    expect(field("Price").value).toBe("50000");
    fireEvent.change(field("Amount"), { target: { value: "0.01" } });
    expect(field("Total").value).toBe("500");
    fireEvent.change(field("Price"), { target: { value: "60000" } });
    expect(field("Total").value).toBe("600");
  });

  it("derives the quantity from the total, down to the lot size", () => {
    render(<Harness onSubmit={() => {}} />);
    fireEvent.change(field("Total"), { target: { value: "1000" } });
    expect(field("Amount").value).toBe("0.02");
    fireEvent.change(field("Price"), { target: { value: "63214.5" } });
    fireEvent.change(field("Total"), { target: { value: "1000" } });
    expect(field("Amount").value).toBe("0.0158");
  });

  it("limits typing to the tick and lot precision", () => {
    render(<Harness onSubmit={() => {}} />);
    fireEvent.change(field("Price"), { target: { value: "50000.123" } });
    expect(field("Price").value).toBe("50000.1");
    fireEvent.change(field("Amount"), { target: { value: "0.123456" } });
    expect(field("Amount").value).toBe("0.1234");
  });

  it("shows the pair's limits and the balance as one types", () => {
    render(<Harness onSubmit={() => {}} />);
    fireEvent.change(field("Amount"), { target: { value: "0.0005" } });
    expect(screen.getByText("Minimum amount 0.001 BTC")).toBeTruthy();
    fireEvent.change(field("Amount"), { target: { value: "0.03" } });
    expect(screen.getByText("Exceeds your available balance")).toBeTruthy();
    fireEvent.change(field("Price"), { target: { value: "5000" } });
    fireEvent.change(field("Amount"), { target: { value: "0.001" } });
    expect(screen.getByText("Minimum total 10 USDT")).toBeTruthy();
  });

  it("submits a valid limit order and blocks an invalid one", () => {
    const onSubmit = vi.fn();
    render(<Harness onSubmit={onSubmit} />);
    fireEvent.click(screen.getByRole("button", { name: "Buy BTC" }));
    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByText("Enter an amount")).toBeTruthy();
    fireEvent.change(field("Amount"), { target: { value: "0.01" } });
    fireEvent.click(screen.getByRole("button", { name: "Buy BTC" }));
    expect(onSubmit).toHaveBeenCalledWith({ side: "BUY", type: "limit", price: "50000", quantity: "0.01" });
  });

  it("sizes a market buy by its total", () => {
    const onSubmit = vi.fn();
    render(<Harness onSubmit={onSubmit} initialType="market" />);
    expect(screen.queryByLabelText("Amount")).toBeNull();
    fireEvent.change(field("Total"), { target: { value: "250" } });
    fireEvent.click(screen.getByRole("button", { name: "Buy BTC" }));
    expect(onSubmit).toHaveBeenCalledWith({ side: "BUY", type: "market", quoteAmount: "250" });
  });

  it("keeps one amount for a market order, the slider under it (B157)", () => {
    render(<Harness onSubmit={() => {}} initialType="market" />);
    // The price row says 市价 and takes nothing; a buy has the total only.
    expect(field("Price").disabled).toBe(true);
    const total = field("Total");
    const slider = screen.getByRole("slider");
    expect(total.compareDocumentPosition(slider) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "50%" }));
    expect(total.value).toBe("500");
    // A sell has the quantity only, sized from the base it holds.
    fireEvent.click(screen.getByRole("radio", { name: "Sell" }));
    expect(screen.queryByLabelText("Total")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "50%" }));
    expect(field("Amount").value).toBe("0.266");
  });

  it("fills a percentage of the balance from the slider", () => {
    render(<Harness onSubmit={() => {}} initialSide="SELL" />);
    fireEvent.click(screen.getByRole("button", { name: "25%" }));
    expect(field("Amount").value).toBe("0.133");
    expect(field("Total").value).toBe("6650");
  });
});
