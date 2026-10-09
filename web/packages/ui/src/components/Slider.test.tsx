import "../test/setup";
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Slider } from "./Slider";

// The pulse is the thumb's extra element (B173); the dot is always there.
const pulse = (container: HTMLElement) => {
  const spans = container.querySelectorAll("[role=slider] > span");
  return spans.length > 1 ? spans[0] : null;
};

describe("Slider at its maximum (B173)", () => {
  it("plays once on arriving, not on showing at it or staying there", () => {
    const marks = [0, 25, 50, 75, 100];
    const at = (v: number) => <Slider value={v} onValueChange={() => {}} marks={marks} aria-label="p" />;
    const { container, rerender } = render(at(100));
    expect(pulse(container)).toBeNull();
    expect(container.querySelector(".slider-sweep")).toBeNull();
    rerender(at(75));
    rerender(at(100));
    const first = pulse(container);
    expect(first?.className).toContain("motion-safe:animate-slider-peak");
    expect(first?.className).toContain("motion-reduce:opacity-30");
    // Two sparks with four ghosts each, a flash for each mark below the
    // end, the band over the fill: hidden under reduced motion.
    const effect = container.querySelector(".slider-sweep")?.parentElement?.parentElement as HTMLElement;
    expect(effect.className).toContain("motion-reduce:hidden");
    expect(effect.querySelectorAll("[style*=slider-spark-x]")).toHaveLength(10);
    expect(effect.querySelectorAll("[style*=slider-mark-flash]")).toHaveLength(4);
    rerender(at(100));
    expect(pulse(container)).toBe(first);
    rerender(at(60));
    expect(pulse(container)).toBeNull();
    expect(container.querySelector(".slider-sweep")).toBeNull();
    rerender(at(100));
    expect(pulse(container)).not.toBe(first);
  });

  it("does not pulse when turned off or disabled", () => {
    for (const props of [{ pulseAtMax: false }, { disabled: true }]) {
      const { container, rerender } = render(<Slider value={50} onValueChange={() => {}} aria-label="p" {...props} />);
      rerender(<Slider value={100} onValueChange={() => {}} aria-label="p" {...props} />);
      expect(pulse(container)).toBeNull();
    }
  });

  it("puts the thumb's centre on the value: a zero-size thumb Radix cannot shift", () => {
    const { container } = render(<Slider value={0} onValueChange={() => {}} marks={[0, 100]} markLabels aria-label="p" />);
    expect(container.querySelector("[role=slider]")?.className).toContain("size-0");
    // The keyboard's focus shows on the dot (B175).
    expect(container.querySelector("[role=slider] > span:last-child")?.className).toContain("group-focus-visible:ring-2");
    const labels = [...container.querySelectorAll("button")].map((b) => (b as HTMLElement).style.left);
    expect(labels).toEqual(["calc(0% - 0.5rem)", "calc(100% + 0.5rem)"]);
  });
});
