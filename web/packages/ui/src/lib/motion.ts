import type { Transition, Variants } from "motion/react";

// Motion presets (design §5.3). Only transform and opacity move, so no
// animation triggers layout; prefers-reduced-motion is honored by motion's
// MotionConfig reducedMotion="user" in each app.

export const ease = [0.2, 0.8, 0.2, 1] as const;

export const durations = { fast: 0.12, base: 0.2, slow: 0.32 } as const;

/** Page content: fade in and rise 8 px over 200 ms. */
export const page: Variants = {
  initial: { opacity: 0, y: 8 },
  animate: { opacity: 1, y: 0, transition: { duration: durations.base, ease } },
  exit: { opacity: 0, transition: { duration: durations.fast, ease } },
};

/** First visible rows: 20 ms apart, at most 12 of them. */
export function stagger(index: number): Transition {
  return { duration: durations.base, ease, delay: Math.min(index, 11) * 0.02 };
}

export const listItem: Variants = {
  initial: { opacity: 0, y: 6 },
  animate: (i: number) => ({ opacity: 1, y: 0, transition: stagger(i) }),
};

/** The mobile bottom sheet's spring (stiffness 400, damping 40). */
export const sheetSpring: Transition = { type: "spring", stiffness: 400, damping: 40 };

/** Dialogs and popovers: fade and scale from 0.96. */
export const pop: Variants = {
  initial: { opacity: 0, scale: 0.96 },
  animate: { opacity: 1, scale: 1, transition: { duration: durations.base, ease } },
  exit: { opacity: 0, scale: 0.96, transition: { duration: durations.fast, ease } },
};

/** Buttons press to 0.98. */
export const press = { scale: 0.98 } as const;
