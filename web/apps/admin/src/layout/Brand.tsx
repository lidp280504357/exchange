import { cn } from "@exchange/ui";

// The console's mark: a four-pointed star (astra) in the brand colour,
// and the wordmark beside it.

const STAR = "M16 2.5 19.6 12.4 29.5 16 19.6 19.6 16 29.5 12.4 19.6 2.5 16 12.4 12.4Z";

/** Mark is the star alone, filled. */
export function Mark({ size = 28, className }: { size?: number; className?: string }) {
  return (
    <svg viewBox="0 0 32 32" width={size} height={size} aria-hidden className={cn("shrink-0", className)}>
      <rect width="32" height="32" rx="8" className="fill-brand-soft" />
      <path d={STAR} className="fill-brand" transform="translate(16 16) scale(0.72) translate(-16 -16)" />
    </svg>
  );
}

/**
 * Wordmark draws the star and the name. Animated, their outlines are drawn
 * first (1.2 s) and the fill follows (the sign-in page).
 */
export function Wordmark({ animated, className }: { animated?: boolean; className?: string }) {
  const stroke = animated ? "animate-draw" : undefined;
  const fill = animated ? "opacity-0 animate-[fade-in_600ms_var(--ease)_1s_forwards]" : undefined;
  return (
    <svg viewBox="0 0 220 40" className={cn("h-10 w-auto", className)} role="img" aria-label="Astras">
      <path
        d={STAR}
        transform="translate(4 4)"
        pathLength={1}
        strokeDasharray={animated ? 1 : undefined}
        strokeDashoffset={animated ? 1 : undefined}
        className={cn("fill-none stroke-brand", stroke)}
        strokeWidth={1.6}
        strokeLinejoin="round"
      />
      <path d={STAR} transform="translate(4 4)" className={cn("fill-brand", fill)} />
      <text
        x="48"
        y="29"
        fontSize="26"
        fontWeight="700"
        letterSpacing="3"
        strokeDasharray={animated ? 360 : undefined}
        strokeDashoffset={animated ? 360 : undefined}
        className={cn("fill-none stroke-fg-1", stroke)}
        strokeWidth={0.8}
        style={{ fontFamily: "var(--font-sans)" }}
      >
        ASTRAS
      </text>
      <text x="48" y="29" fontSize="26" fontWeight="700" letterSpacing="3" className={cn("fill-fg-1", fill)} style={{ fontFamily: "var(--font-sans)" }}>
        ASTRAS
      </text>
    </svg>
  );
}
