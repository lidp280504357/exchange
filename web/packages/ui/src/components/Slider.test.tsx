import "../test/setup";
import { fireEvent, render } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { Slider } from "./Slider";

// The effect's layers, the thumb's halo among them, are in one clip box
// (B194); the dot is always there.
const halo = (container: HTMLElement) => container.querySelector(".slider-fx-clip > .slider-fx > .slider-thumb-halo");
const effect = (container: HTMLElement) => container.querySelector<HTMLElement>(".slider-fx-clip > .slider-fx");

// A form's slider: the value the user moves it to, or settle(value) once
// the form has worked out what it means (a lot-rounded share of a
// balance: 99.99 for 100); outside is a value the page sets without the
// user (a live price, a balance).
function Form({ start, settle = (v: number) => v, outside, ...props }: {
  start: number;
  settle?: (v: number) => number;
  outside?: number;
  pulseAtMax?: boolean;
  disabled?: boolean;
}) {
  const [v, setV] = useState(start);
  return (
    <Slider
      value={outside ?? v}
      onValueChange={(x) => setV(x)}
      onValueCommit={(x) => setV(settle(x))}
      marks={[0, 25, 50, 75, 100]}
      markLabels
      formatMark={(m) => `${m}%`}
      aria-label="p"
      {...props}
    />
  );
}

const thumb = (container: HTMLElement) => container.querySelector("[role=slider]") as HTMLElement;
const label = (container: HTMLElement, text: string) => [...container.querySelectorAll("button")].find((b) => b.textContent === text) as HTMLElement;

describe("Slider at its maximum (B176, B183)", () => {
  it("plays once on the user's arrival, not on showing at it or staying there", () => {
    const { container, rerender } = render(<Form start={100} />);
    expect(halo(container)).toBeNull();
    expect(effect(container)).toBeNull();
    fireEvent.keyDown(thumb(container), { key: "Home" });
    fireEvent.keyDown(thumb(container), { key: "End" });
    const first = halo(container);
    expect(first).not.toBeNull();
    expect(container.querySelector("[role=slider] > span:last-child")?.classList.contains("slider-dot-burst")).toBe(true);
    // Nothing of it is drawn outside the clip box (B194): the thumb holds
    // its dot alone, and the clip box's one child is the effect.
    expect(thumb(container).children).toHaveLength(1);
    expect(container.querySelectorAll(".slider-fx-clip")).toHaveLength(1);
    expect(container.querySelector(".slider-fx-clip")?.children).toHaveLength(1);
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
    rerender(<Form start={100} />);
    expect(halo(container)).toBe(first);
    fireEvent.keyDown(thumb(container), { key: "PageDown" });
    expect(halo(container)).toBeNull();
    expect(effect(container)).toBeNull();
    // A mark's label is the user's too.
    fireEvent.click(label(container, "100%"));
    expect(halo(container)).not.toBeNull();
    expect(halo(container)).not.toBe(first);
  });

  it("takes half a step below max for max: a form settling at 99.99% keeps it playing (①)", () => {
    const { container } = render(<Form start={50} settle={(v) => (v === 100 ? 99.99 : v)} />);
    fireEvent.click(label(container, "100%"));
    expect(effect(container)).not.toBeNull();
    expect(thumb(container).getAttribute("aria-valuenow")).toBe("99.99");
  });

  it("plays only for the user: a live value crossing max does not (②)", () => {
    const { container, rerender } = render(<Form start={50} outside={90} />);
    rerender(<Form start={50} outside={100} />);
    expect(effect(container)).toBeNull();
    expect(halo(container)).toBeNull();
  });

  it("does not come back when cut short by disabled at max (B192)", () => {
    const { container, rerender } = render(<Form start={75} />);
    fireEvent.keyDown(thumb(container), { key: "End" });
    expect(effect(container)).not.toBeNull();
    rerender(<Form start={75} disabled />);
    expect(effect(container)).toBeNull();
    rerender(<Form start={75} />);
    expect(effect(container)).toBeNull();
    expect(halo(container)).toBeNull();
  });

  it("is gone once played, and turning it off and on at max does not play it again (③ ④)", () => {
    const { container, rerender } = render(<Form start={75} />);
    fireEvent.keyDown(thumb(container), { key: "End" });
    const fx = effect(container) as HTMLElement;
    fireEvent.animationEnd(fx.querySelector(".slider-band") as HTMLElement, { animationName: "slider-band-x" });
    expect(effect(container)).not.toBeNull();
    fireEvent.animationEnd(fx.querySelector(".slider-glow-2") as HTMLElement, { animationName: "slider-glow-outer" });
    expect(effect(container)).toBeNull();
    expect(halo(container)).toBeNull();
    rerender(<Form start={75} disabled />);
    rerender(<Form start={75} />);
    expect(effect(container)).toBeNull();
  });

  it("lights the fill from above, in the tone", () => {
    const { container } = render(<Slider value={40} onValueChange={() => {}} tone="down" aria-label="p" />);
    const fill = container.querySelector(".slider-fill");
    expect(fill?.className).toContain("text-down");
  });

  it("does not pulse when turned off or disabled", () => {
    const { container } = render(<Form start={50} pulseAtMax={false} />);
    fireEvent.keyDown(thumb(container), { key: "End" });
    expect(halo(container)).toBeNull();
    expect(effect(container)).toBeNull();
    const off = render(<Form start={50} disabled />);
    fireEvent.click(label(off.container, "100%"));
    expect(effect(off.container)).toBeNull();
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
