import type { CSSProperties } from "react";
import { cn } from "../lib/cn";

export type SkeletonProps = { className?: string; style?: CSSProperties; /** A circle (avatars, icons). */ round?: boolean };

/** Skeleton stands in for content while it loads; a shimmer sweeps it every 1.2 s. */
export function Skeleton({ className, style, round }: SkeletonProps) {
  return (
    <span
      aria-hidden
      style={style}
      className={cn("relative block overflow-hidden bg-bg-3", round ? "rounded-full" : "rounded-1", className)}
    >
      <span className="absolute inset-0 animate-shimmer bg-gradient-to-r from-transparent via-bg-2 to-transparent opacity-60" />
    </span>
  );
}

/** SkeletonLines shows n text lines, the last one shorter. */
export function SkeletonLines({ lines = 3, className }: { lines?: number; className?: string }) {
  return (
    <div className={cn("flex flex-col gap-2", className)}>
      {Array.from({ length: lines }, (_, i) => (
        <Skeleton key={i} className={cn("h-3.5", i === lines - 1 ? "w-3/5" : "w-full")} />
      ))}
    </div>
  );
}
