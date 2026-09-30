import { cn, thumbSpring } from "@exchange/ui";
import { motion, useReducedMotion } from "motion/react";
import { useEffect, useId, useRef, type ReactNode } from "react";

export type PillItem = { value: string; label: ReactNode; icon?: ReactNode };

export type PillBarProps = {
  items: PillItem[];
  value: string;
  onValueChange: (value: string) => void;
  /** The pills share the bar's width (a two-way switch) instead of scrolling. */
  block?: boolean;
  className?: string;
  "aria-label"?: string;
};

/**
 * PillBar is the mobile row of pills (market and help categories, chart
 * intervals, gainers and losers): each pill sits in a 44 px touch target,
 * the chosen one marked by a thumb that slides over (transform only). A
 * long row scrolls sideways inside itself and brings the chosen pill into
 * view; the page never scrolls sideways.
 */
export function PillBar({ items, value, onValueChange, block, className, "aria-label": ariaLabel }: PillBarProps) {
  const id = useId();
  const reduced = useReducedMotion();
  const bar = useRef<HTMLDivElement>(null);
  const first = useRef(true);

  // Keep the chosen pill in view: centred in the bar, without moving the page.
  useEffect(() => {
    const el = bar.current;
    const on = el?.querySelector<HTMLElement>("[aria-pressed=true]");
    if (!el || !on || block) return;
    const left = on.offsetLeft - (el.clientWidth - on.offsetWidth) / 2;
    const smooth = !first.current && !reduced;
    first.current = false;
    if (typeof el.scrollTo === "function") el.scrollTo({ left: Math.max(0, left), behavior: smooth ? "smooth" : "auto" });
  }, [value, block, reduced]);

  return (
    <motion.div
      ref={bar}
      role="group"
      aria-label={ariaLabel}
      // The row scrolls sideways: the sliding thumb measures with its scroll.
      layoutScroll={!block}
      className={cn("relative flex items-center", block ? "gap-1" : "gap-0.5 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden", className)}
    >
      {items.map((it) => {
        const on = it.value === value;
        return (
          <button
            key={it.value}
            type="button"
            aria-pressed={on}
            onClick={() => onValueChange(it.value)}
            className={cn("group relative flex h-11 shrink-0 items-center justify-center outline-none", block && "flex-1")}
          >
            <span
              className={cn(
                "relative isolate inline-flex h-8 items-center justify-center gap-1 whitespace-nowrap rounded-full px-3.5 text-sm font-medium transition-[color,transform] duration-[var(--t-fast)] group-active:scale-95 group-focus-visible:ring-1 group-focus-visible:ring-brand",
                block && "w-full",
                on ? "text-brand" : "text-fg-2",
              )}
            >
              {on && (
                <motion.span
                  layoutId={`${id}-thumb`}
                  aria-hidden
                  transition={reduced ? { duration: 0 } : thumbSpring}
                  className="absolute inset-0 -z-10 rounded-full bg-brand-soft"
                />
              )}
              {it.icon}
              {it.label}
            </span>
          </button>
        );
      })}
    </motion.div>
  );
}
