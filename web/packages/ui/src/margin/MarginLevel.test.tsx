import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MarginLevel } from "./MarginLevel";

describe("MarginLevel", () => {
  it("colours the level by its zone and reports it to assistive technology", () => {
    const { container, rerender } = render(<MarginLevel level="1.18" warn="1.3" liquidation="1.1" />);
    expect(container.firstElementChild?.getAttribute("data-zone")).toBe("danger");
    expect(screen.getByTestId("margin-level").textContent).toBe("1.18");
    expect(screen.getByRole("meter").getAttribute("aria-valuenow")).toBe("5");

    rerender(<MarginLevel level="1.5" warn="1.3" liquidation="1.1" />);
    expect(container.firstElementChild?.getAttribute("data-zone")).toBe("caution");

    rerender(<MarginLevel level={null} warn="1.3" liquidation="1.1" />);
    expect(container.firstElementChild?.getAttribute("data-zone")).toBe("none");
    expect(screen.getByTestId("margin-level").textContent).toBe("999");
    expect(screen.getByRole("meter").getAttribute("aria-valuenow")).toBe("100");
  });
});
