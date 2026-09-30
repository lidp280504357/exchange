import type { ReactNode } from "react";
import { cn } from "../lib/cn";

export type MarqueeProps = {
  children: ReactNode;
  /** Seconds per loop (default 40); longer strips want more. */
  duration?: number;
  /** Space between items and between the two copies, in px. */
  gap?: number;
  /** Pause while hovered or focused (default on). */
  pauseOnHover?: boolean;
  /** Fade the strip's edges (default on). */
  fade?: boolean;
  className?: string;
  "aria-label"?: string;
};

/**
 * Marquee scrolls a strip of items sideways forever (the home page ticker
 * of the top 10 coins): the content is duplicated and moved by half its
 * width with the animate-marquee keyframe, so the loop has no seam. The
 * copy is inert and hidden from screen readers; reduced motion stops it.
 */
export function Marquee({ children, duration = 40, gap = 32, pauseOnHover = true, fade = true, className, "aria-label": ariaLabel }: MarqueeProps) {
  const lane = "flex shrink-0 items-center";
  return (
    <div role={ariaLabel ? "region" : undefined} aria-label={ariaLabel} className={cn("group relative overflow-hidden", fade && "mask-x-from-92%", className)}>
      <div
        className={cn(
          "flex w-max animate-marquee",
          pauseOnHover && "group-hover:[animation-play-state:paused] group-focus-within:[animation-play-state:paused]",
        )}
        style={{ animationDuration: `${duration}s` }}
      >
        <div className={lane} style={{ gap, paddingRight: gap }}>
          {children}
        </div>
        <div className={lane} style={{ gap, paddingRight: gap }} aria-hidden inert>
          {children}
        </div>
      </div>
    </div>
  );
}
