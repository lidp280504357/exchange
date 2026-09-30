import "../test/setup";
import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { NumberInput, percentOf, ratioPercent, sanitizeDecimal, stepValue, type NumberInputProps } from "./NumberInput";

describe("sanitizeDecimal", () => {
  it("trims digits beyond the precision", () => {
    expect(sanitizeDecimal("1.23456", 2)).toBe("1.23");
    expect(sanitizeDecimal("12.7", 0)).toBe("12");
    expect(sanitizeDecimal("0.1", 8)).toBe("0.1");
  });
  it("accepts what people type or paste", () => {
    expect(sanitizeDecimal("", 4)).toBe("");
    expect(sanitizeDecimal("00012", 4)).toBe("12");
    expect(sanitizeDecimal(".5", 4)).toBe("0.5");
    expect(sanitizeDecimal("1.", 4)).toBe("1.");
    expect(sanitizeDecimal("1,234.5", 4)).toBe("1234.5");
    expect(sanitizeDecimal("1,234", 4)).toBe("1234");
    expect(sanitizeDecimal("1,5", 4)).toBe("1.5");
    expect(sanitizeDecimal(" 2 000 ", 4)).toBe("2000");
    expect(sanitizeDecimal("１２．５", 4)).toBe("12.5");
    expect(sanitizeDecimal("3。25", 4)).toBe("3.25");
  });
  it("rejects what is not a number", () => {
    expect(sanitizeDecimal("abc", 4)).toBeNull();
    expect(sanitizeDecimal("1.2.3", 4)).toBeNull();
    expect(sanitizeDecimal("-1", 4)).toBeNull();
    expect(sanitizeDecimal("1e5", 4)).toBeNull();
  });
});

describe("stepValue", () => {
  it("moves to the next multiple of the step", () => {
    expect(stepValue("1.23", "0.1", 1)).toBe("1.3");
    expect(stepValue("1.23", "0.1", -1)).toBe("1.2");
    expect(stepValue("1.3", "0.1", 1)).toBe("1.4");
    expect(stepValue("1.3", "0.1", -1)).toBe("1.2");
    expect(stepValue("63214.7", "0.5", 1)).toBe("63215");
    expect(stepValue("", "0.5", 1)).toBe("0.5");
  });
  it("stays within min and max", () => {
    expect(stepValue("0.05", "0.1", -1)).toBe("0");
    expect(stepValue("0.95", "0.1", 1, "0", "1")).toBe("1");
    expect(stepValue("1", "0.3", 1, "0", "1")).toBe("0.9");
    expect(stepValue("1", "1", -1, "1")).toBe("1");
  });
});

describe("percentOf", () => {
  it("rounds down to the decimals, then to the step", () => {
    expect(percentOf("1.23456", 50, 4)).toBe("0.6172");
    expect(percentOf("1.23456", 50, 4, "0.001")).toBe("0.617");
    expect(percentOf("1.23456", 100, 3)).toBe("1.234");
    expect(percentOf("10.5", 25, 2)).toBe("2.62");
    expect(percentOf("10", 0, 2)).toBe("0");
    expect(percentOf("", 50, 2)).toBe("0");
  });
  it("draws the slider from value / max", () => {
    expect(ratioPercent("5", "10")).toBe(50);
    expect(ratioPercent("20", "10")).toBe(100);
    expect(ratioPercent("", "10")).toBe(0);
    expect(ratioPercent("1", "0")).toBe(0);
  });
});

function Harness(props: Partial<NumberInputProps> & { initial?: string; onChange?: (v: string) => void }) {
  const { initial = "", onChange, ...rest } = props;
  const [v, setV] = useState(initial);
  return (
    <NumberInput
      aria-label="Amount"
      {...rest}
      value={v}
      onValueChange={(next) => {
        setV(next);
        onChange?.(next);
      }}
    />
  );
}

describe("NumberInput", () => {
  it("keeps typed text within the precision", () => {
    const onChange = vi.fn();
    render(<Harness decimals={2} onChange={onChange} />);
    const input = screen.getByLabelText("Amount") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "1.23456" } });
    expect(onChange).toHaveBeenLastCalledWith("1.23");
    expect(input.value).toBe("1.23");
    expect(input.getAttribute("inputmode")).toBe("decimal");
  });

  it("ignores text that is not a number", () => {
    const onChange = vi.fn();
    render(<Harness decimals={2} initial="5" onChange={onChange} />);
    const input = screen.getByLabelText("Amount") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "5x" } });
    expect(onChange).not.toHaveBeenCalled();
    expect(input.value).toBe("5");
  });

  it("steps with the buttons and the arrow keys", () => {
    const onChange = vi.fn();
    render(<Harness initial="1.23" step="0.1" decimals={2} onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Increase" }));
    expect(onChange).toHaveBeenLastCalledWith("1.3");
    fireEvent.click(screen.getByRole("button", { name: "Decrease" }));
    expect(onChange).toHaveBeenLastCalledWith("1.2");
    fireEvent.keyDown(screen.getByLabelText("Amount"), { key: "ArrowUp" });
    expect(onChange).toHaveBeenLastCalledWith("1.3");
  });

  it("fills the maximum, rounded down", () => {
    const onChange = vi.fn();
    render(<Harness max="10.555" decimals={2} onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Max" }));
    expect(onChange).toHaveBeenLastCalledWith("10.55");
  });

  it("sets a percentage of the maximum from the slider marks", () => {
    const onChange = vi.fn();
    render(<Harness max="10.5" decimals={2} step="0.1" slider onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "50%" }));
    expect(onChange).toHaveBeenLastCalledWith("5.2");
    fireEvent.click(screen.getByRole("button", { name: "100%" }));
    expect(onChange).toHaveBeenLastCalledWith("10.5");
  });

  it("snaps to the step when leaving the field", () => {
    const onChange = vi.fn();
    render(<Harness initial="63214.7" step="0.5" decimals={1} snap onChange={onChange} />);
    fireEvent.blur(screen.getByLabelText("Amount"));
    expect(onChange).toHaveBeenLastCalledWith("63214.5");
  });
});
