import "../test/setup";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ShortList, SummaryRow, SummaryTable, withKeys } from "./SummaryTable";

describe("SummaryTable", () => {
  it("shows a row's details only once it is opened, by a click on the row or its arrow", () => {
    render(
      <SummaryTable label="checks">
        <SummaryRow data-testid="fund" title="Insurance fund" status={{ tone: "success", label: "Ready" }} summary="23 assets, all above 0" details={<span>ASTRA 99,999.5</span>} />
        <SummaryRow data-testid="mode" title="Test mode" summary="Off" />
      </SummaryTable>,
    );
    const fund = screen.getByTestId("fund");
    expect(fund.getAttribute("data-open")).toBe("false");
    expect(screen.queryByText("ASTRA 99,999.5")).toBeNull();
    fireEvent.click(screen.getByText("23 assets, all above 0"));
    expect(fund.getAttribute("data-open")).toBe("true");
    expect(screen.getByText("ASTRA 99,999.5")).toBeTruthy();
    const arrow = fund.querySelector("button[aria-expanded]")!;
    expect(arrow.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(arrow);
    expect(screen.queryByText("ASTRA 99,999.5")).toBeNull();
    // A row without details has nothing to open.
    const mode = screen.getByTestId("mode");
    expect(mode.getAttribute("data-open")).toBeNull();
    expect(mode.querySelector("button[aria-expanded]")).toBeNull();
  });

  it("marks the status badge for tests and leaves it out without a status column", () => {
    const { rerender } = render(
      <SummaryTable label="checks">
        <SummaryRow title="Withdrawals" status={{ tone: "danger", label: "Blocking" }} statusTestId="launch-withdraw" statusData="FAIL" summary="Off" />
      </SummaryTable>,
    );
    expect(screen.getByTestId("launch-withdraw").getAttribute("data-status")).toBe("FAIL");
    expect(screen.getByText("Blocking")).toBeTruthy();
    rerender(
      <SummaryTable label="values" noStatus>
        <SummaryRow title="Withdrawals" status={{ tone: "danger", label: "Blocking" }} summary="Off" />
      </SummaryTable>,
    );
    expect(screen.queryByText("Blocking")).toBeNull();
  });

  it("sets switch keys and account types in a text as tags, and nothing else", () => {
    render(<span data-testid="t">{withKeys("开关 market.house_liquidity 与 HOUSE 库存，账本 INSURANCE_FUND（各结算币）")}</span>);
    const tags = Array.from(screen.getByTestId("t").querySelectorAll("code")).map((c) => c.textContent);
    expect(tags).toEqual(["market.house_liquidity", "INSURANCE_FUND"]);
    expect(screen.getByTestId("t").textContent).toBe("开关 market.house_liquidity 与 HOUSE 库存，账本 INSURANCE_FUND（各结算币）");
  });

  it("names a long list's first three and how many more", () => {
    render(<span data-testid="l"><ShortList items={["USDT", "BTC", "ETH", "SOL", "BNB"]} /></span>);
    expect(screen.getByTestId("l").textContent).toBe("USDT · BTC · ETH +2");
  });
});
