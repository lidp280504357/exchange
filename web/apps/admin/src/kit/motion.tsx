import { cn } from "@exchange/ui";
import type { CSSProperties, ReactNode } from "react";

// The console's motion helpers (design 2026-10-02 §6): transform and
// opacity only; the stylesheet stops them for prefers-reduced-motion.

/** stagger places the i-th card of a group 40 ms after the one before (use with the stagger class). */
export const stagger = (i: number): CSSProperties => ({ "--i": i }) as CSSProperties;

/**
 * Reveal draws a chart from left to right over 600 ms: a cover of the
 * card's colour shrinks away. Give it a key to draw again (another range).
 */
export function Reveal({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cn("relative", className)}>
      {children}
      <div aria-hidden className="pointer-events-none absolute inset-0 origin-right bg-bg-1 animate-wipe" />
    </div>
  );
}

/** Pulse is a status dot; a bad one breathes to draw the eye. */
export function Pulse({ ok, className }: { ok: boolean; className?: string }) {
  return (
    <span className={cn("relative inline-flex size-2 shrink-0", className)}>
      {!ok && <span aria-hidden className="absolute inset-0 rounded-full bg-danger animate-breathe" />}
      <span className={cn("relative inline-flex size-2 rounded-full", ok ? "bg-success" : "bg-danger")} />
    </span>
  );
}
