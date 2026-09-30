import { cn } from "../lib/cn";

export type SpinnerProps = { size?: number; className?: string; label?: string };

/** Spinner is a small rotating ring for work in progress. */
export function Spinner({ size = 16, className, label }: SpinnerProps) {
  return (
    <span
      role="status"
      aria-label={label}
      className={cn("inline-block animate-spin rounded-full border-2 border-current border-t-transparent align-[-0.125em]", className)}
      style={{ width: size, height: size }}
    />
  );
}
