import { cn } from "@exchange/ui";

/** Check is a success mark whose tick is drawn as it appears. */
export function Check({ size = 18, className }: { size?: number; className?: string }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden className={cn("shrink-0 text-success-strong", className)}>
      <circle cx="12" cy="12" r="10" fill="currentColor" opacity="0.15" />
      <path
        d="m7.5 12.5 3 3 6-6.5"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.2"
        strokeLinecap="round"
        strokeLinejoin="round"
        pathLength={1}
        strokeDasharray={1}
        strokeDashoffset={1}
        className="animate-check"
      />
    </svg>
  );
}
