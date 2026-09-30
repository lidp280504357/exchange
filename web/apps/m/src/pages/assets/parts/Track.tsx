import type { StepState } from "@exchange/core/wallet/timeline";
import { cn } from "@exchange/ui";

export type TrackStep = { key: string; label: string; state: StepState };

const dot: Record<StepState, string> = {
  done: "bg-brand",
  current: "bg-brand ring-4 ring-brand-soft",
  upcoming: "border border-line-2 bg-bg-3",
  error: "bg-danger ring-4 ring-danger/20",
};

const text: Record<StepState, string> = {
  done: "text-fg-2",
  current: "font-medium text-brand",
  upcoming: "text-fg-3",
  error: "font-medium text-danger",
};

/**
 * Track is the compact status timeline of a record card on a phone: a dot
 * per step joined by lines, the step names under them (3 to 6 steps fit
 * 360 px). It is drawn from spans, so it can sit inside the card's button;
 * the card says the current step in words, and the record's sheet shows
 * the full timeline with its times.
 */
export function Track({ steps, className }: { steps: TrackStep[]; className?: string }) {
  return (
    <span aria-hidden className={cn("flex w-full items-start", className)}>
      {steps.map((s, i) => (
        <span key={s.key} className="relative flex min-w-0 flex-1 flex-col items-center">
          {i < steps.length - 1 && (
            <span className={cn("absolute left-1/2 top-[5px] h-0.5 w-full", s.state === "done" ? "bg-brand" : "bg-line-2")} />
          )}
          <span className={cn("relative size-3 rounded-full", dot[s.state])} />
          <span className={cn("mt-1.5 max-w-full truncate px-0.5 text-xs", text[s.state])}>{s.label}</span>
        </span>
      ))}
    </span>
  );
}
