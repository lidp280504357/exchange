import { cn } from "@exchange/ui";
import { Star } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useEffect, useRef } from "react";

export type FavoriteStarProps = {
  active: boolean;
  onToggle: () => void;
  /** The accessible name (add / remove). */
  label: string;
  /** The icon's size (default 18). */
  size?: number;
  disabled?: boolean;
  /** A bordered button (the coin page header) instead of the plain row icon. */
  boxed?: boolean;
  className?: string;
};

/**
 * FavoriteStar is the favourite toggle of the market rows and the coin
 * page (the PC site's, sized for touch: at least 44 px): it pops and fills
 * when switched on (only on a tap, not when a row first appears), with a
 * ring bursting out; transform and opacity only.
 */
export function FavoriteStar({ active, onToggle, label, size = 18, disabled, boxed, className }: FavoriteStarProps) {
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
        e.preventDefault();
        e.stopPropagation();
        onToggle();
      }}
      className={cn(
        "relative grid min-h-tap min-w-tap shrink-0 place-items-center transition-[color,transform,border-color] duration-[var(--t-fast)] active:scale-90 disabled:opacity-50",
        boxed && "size-tap rounded-2 border border-line-2",
        active ? "text-brand" : "text-fg-3",
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
            className="pointer-events-none absolute left-1/2 top-1/2 -ml-4 -mt-4 size-8 rounded-full border border-brand"
            initial={{ opacity: 0.7, scale: 0.5 }}
            animate={{ opacity: 0, scale: 1.6 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.45, ease: [0.2, 0.8, 0.2, 1] }}
          />
        )}
      </AnimatePresence>
    </button>
  );
}
