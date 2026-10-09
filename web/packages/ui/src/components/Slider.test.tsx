import "../test/setup";
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Slider } from "./Slider";

// The thumb's halo is its extra element; the dot is always there.
const halo = (container: HTMLElement) => container.querySelector("[role=slider] > .slider-thumb-halo");
const effect = (container: HTMLElement) => container.querySelector<HTMLElement>(".slider-fx:not(.slider-thumb-halo)");

describe("Slider at its maximum (B176)", () => {
  it("plays once on arriving, not on showing at it or staying there", () => {
    const marks = [0, 25, 50, 75, 100];
    const at = (v: number) => <Slider value={v} onValueChange={() => {}} marks={marks} aria-label="p" />;
    const { container, rerender } = render(at(100));
    expect(halo(container)).toBeNull();
    expect(effect(container)).toBeNull();
    rerender(at(75));
    rerender(at(100));
    const first = halo(container);
    expect(first).not.toBeNull();
    expect(container.querySelector("[role=slider] > span:last-child")?.className).toContain("slider-dot-burst");
    // Two glow layers; under reduced motion (the stylesheet) only the outer
    // one stays, fading in and out: the moving parts are in slider-fx-motion
    // - a soft glow for each mark below the end, timed to the first band's
    // passing, the two bands, the flare and ten sparks.
    const fx = effect(container) as HTMLElement;
    expect(fx.querySelectorAll(".slider-glow-1, .slider-glow-2")).toHaveLength(2);
    const moving = fx.querySelector(".slider-fx-motion") as HTMLElement;
    const glows = [...moving.querySelectorAll<HTMLElement>(".slider-mark-glow")];
    expect(glows.map((g) => g.style.left)).toEqual(["0%", "25%", "50%", "75%"]);
    // The band runs back from the end in 760 ms after the 200 ms charge:
    // the mark at 75% is lit 190 ms in, at 0% 760 ms in (less 35% of the
    // glow's 480 ms, its brightest).
    expect(glows.map((g) => g.style.animation.match(/ (\d+)ms both/)?.[1])).toEqual(["792", "602", "412", "222"]);
    expect(moving.querySelectorAll(".slider-band")).toHaveLength(2);
    expect(moving.querySelectorAll(".slider-band-head, .slider-band-trail")).toHaveLength(4);
    expect(moving.querySelectorAll(".slider-burst")).toHaveLength(1);
    expect(moving.querySelectorAll(".slider-spark")).toHaveLength(10);
    rerender(at(100));
    expect(halo(container)).toBe(first);
    rerender(at(60));
    expect(halo(container)).toBeNull();
    expect(effect(container)).toBeNull();
    rerender(at(100));
    expect(halo(container)).not.toBe(first);
  });

  it("lights the fill from above, in the tone", () => {
    const { container } = render(<Slider value={40} onValueChange={() => {}} tone="down" aria-label="p" />);
    const fill = container.querySelector(".slider-fill");
    expect(fill?.className).toContain("text-down");
  });

  it("does not pulse when turned off or disabled", () => {
    for (const props of [{ pulseAtMax: false }, { disabled: true }]) {
      const { container, rerender } = render(<Slider value={50} onValueChange={() => {}} aria-label="p" {...props} />);
      rerender(<Slider value={100} onValueChange={() => {}} aria-label="p" {...props} />);
      expect(halo(container)).toBeNull();
      expect(effect(container)).toBeNull();
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
