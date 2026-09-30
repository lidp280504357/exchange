import { dec } from "@exchange/core";
import { useRef } from "react";
import { cn } from "./cn";

export type Flash = {
  /** Changes on every move: remounting FlashLayer with it restarts the CSS animation. */
  key: number;
  /** animate-flash-up / -down after a move, "" before the first one. */
  className: string;
};

/**
 * useFlash follows a decimal value and reports a flash class when it
 * rises or falls (the PriceText pattern): no timers, the animation runs
 * once per remount of the FlashLayer that carries `key`.
 */
export function useFlash(value: string | null | undefined, enabled = true): Flash {
  const prev = useRef(value);
  const gen = useRef(0);
  const dir = useRef<"up" | "down" | null>(null);
  if (value !== prev.current) {
    if (enabled && value && prev.current && dec.isDecimal(value) && dec.isDecimal(prev.current)) {
      const c = dec.cmp(value, prev.current);
      if (c !== 0) {
        dir.current = c > 0 ? "up" : "down";
        gen.current++;
      }
    }
    prev.current = value;
  }
  const className = gen.current === 0 ? "" : dir.current === "up" ? "animate-flash-up" : "animate-flash-down";
  return { key: gen.current, className };
}

/**
 * FlashLayer paints a flash behind its parent's text (the parent needs
 * `relative isolate`): the layer remounts on each move, the text stays the
 * same node, so a ticking number is neither DOM churn nor a new largest
 * paint.
 */
export function FlashLayer({ flash }: { flash: Flash }) {
  if (!flash.className) return null;
  return <span key={flash.key} aria-hidden className={cn("pointer-events-none absolute inset-0 -z-10 rounded-[inherit]", flash.className)} />;
}
