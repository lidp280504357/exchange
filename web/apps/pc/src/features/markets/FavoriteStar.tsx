import { cn } from "@exchange/ui";
import { Star } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useEffect, useRef } from "react";

export type FavoriteStarProps = {
  active: boolean;
  onToggle: () => void;
  /** The accessible name (add / remove). */
  label: string;
  size?: number;
  disabled?: boolean;
  /** A bordered 40 px button (the coin page header) instead of the plain row icon. */
  boxed?: boolean;
  className?: string;
};

/**
 * FavoriteStar is the favourite toggle of the market rows and the coin
 * page: it pops and fills when switched on (only on a click, not when a
 * row first appears), with a ring bursting out; transform and opacity only.
 */
export function FavoriteStar({ active, onToggle, label, size = 16, disabled, boxed, className }: FavoriteStarProps) {
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
  }, []);
  const animate = mounted.current;
  return (
    <button
      type="button"
      aria-pressed={active}
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={(e) => {
        e.stopPropagation();
        onToggle();
      }}
      onKeyDown={(e) => e.stopPropagation()}
      className={cn(
        "relative grid shrink-0 place-items-center transition-[color,transform,border-color] duration-[var(--t-fast)] active:scale-90 disabled:cursor-wait disabled:opacity-50",
        boxed ? "size-10 rounded-2 border border-line-2 hover:border-brand" : "size-7 rounded-1",
        active ? "text-brand" : "text-fg-3 hover:text-fg-1",
        className,
      )}
    >
      <motion.span
        key={active ? "on" : "off"}
        className="grid place-items-center"
        initial={animate ? (active ? { scale: 0.3, rotate: -45 } : { scale: 0.8 }) : false}
        animate={{ scale: 1, rotate: 0 }}
        transition={{ type: "spring", stiffness: 520, damping: 16 }}
      >
        <Star size={size} strokeWidth={1.8} className={cn(active && "fill-current")} />
      </motion.span>
      <AnimatePresence>
        {active && animate && (
          <motion.span
            key="burst"
            aria-hidden
            className="pointer-events-none absolute inset-1 rounded-full border border-brand"
            initial={{ opacity: 0.7, scale: 0.5 }}
            animate={{ opacity: 0, scale: 1.7 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.45, ease: [0.2, 0.8, 0.2, 1] }}
          />
        )}
      </AnimatePresence>
    </button>
  );
}
