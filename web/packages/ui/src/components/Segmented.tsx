import { motion, useReducedMotion } from "motion/react";
import { ToggleGroup as RToggleGroup } from "radix-ui";
import { useId, type ReactNode } from "react";
import { cn } from "../lib/cn";

export type SegmentedItem = {
  value: string;
  label: ReactNode;
  icon?: ReactNode;
  disabled?: boolean;
  /** The name when the label is only an icon. */
  "aria-label"?: string;
  /** Classes of the sliding thumb while this item is active (buy green, sell red). */
  thumbClassName?: string;
  /** Classes of the label while active (defaults to text-fg-1). */
  activeClassName?: string;
};

export type SegmentedSize = "xs" | "sm" | "md" | "lg";

export type SegmentedProps = {
  value: string;
  onValueChange: (value: string) => void;
  items: SegmentedItem[];
  size?: SegmentedSize;
  /** Stretch to the container, items share the width. */
  block?: boolean;
  /** Rounded-rectangle thumb instead of a pill. */
  square?: boolean;
  disabled?: boolean;
  className?: string;
  "aria-label"?: string;
};

// The large size is the 44 px touch target (h-11 is 38.5 px under the 14 px
// root); the medium one grows to it on touch screens.
const sizes: Record<SegmentedSize, string> = {
  xs: "h-6 px-2 text-xs gap-1",
  sm: "h-7 px-3 text-sm gap-1.5",
  md: "h-9 px-4 text-base gap-2 pointer-coarse:min-h-tap",
  lg: "h-tap px-5 text-md gap-2",
};

export const thumbSpring = { type: "spring", stiffness: 520, damping: 42 } as const;

/**
 * Segmented is a single choice among a few (limit/market, buy/sell, chart
 * views) on Radix ToggleGroup: a pill track with a thumb that slides to the
 * active item (motion layoutId, measured only when the choice changes). A
 * choice cannot be cleared.
 */
export function Segmented({ value, onValueChange, items, size = "sm", block, square, disabled, className, "aria-label": ariaLabel }: SegmentedProps) {
  const id = useId();
  const reduced = useReducedMotion();
  return (
    <RToggleGroup.Root
      type="single"
      value={value}
      onValueChange={(v) => v && onValueChange(v)}
      disabled={disabled}
      aria-label={ariaLabel}
      className={cn("inline-flex items-center bg-bg-2 p-0.5", square ? "rounded-2" : "rounded-full", block && "flex w-full", className)}
    >
      {items.map((it) => {
        const active = it.value === value;
        return (
          <RToggleGroup.Item
            key={it.value}
            value={it.value}
            disabled={it.disabled}
            aria-label={it["aria-label"]}
            className={cn(
              "relative isolate inline-flex select-none items-center justify-center whitespace-nowrap font-medium outline-none transition-colors duration-[var(--t-fast)]",
              "focus-visible:ring-1 focus-visible:ring-brand disabled:cursor-not-allowed disabled:opacity-50",
              square ? "rounded-1" : "rounded-full",
              sizes[size],
              block && "flex-1",
              active ? (it.activeClassName ?? "text-fg-1") : "text-fg-3 hover:text-fg-1",
            )}
          >
            {active && (
              <motion.span
                layoutId={`${id}-thumb`}
                // Slides only when the choice changes; when the track moves
                // (something put in above it), the thumb moves with it (F40).
                layoutDependency={value}
                aria-hidden
                transition={reduced ? { duration: 0 } : thumbSpring}
                className={cn("pointer-events-none absolute inset-0 -z-10 bg-bg-3 shadow-pop", square ? "rounded-1" : "rounded-full", it.thumbClassName)}
              />
            )}
            {it.icon}
            {it.label}
          </RToggleGroup.Item>
        );
      })}
    </RToggleGroup.Root>
  );
}
