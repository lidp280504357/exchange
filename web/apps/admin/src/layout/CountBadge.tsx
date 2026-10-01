import { cn } from "@exchange/ui";
import { useEffect, useRef, useState } from "react";

/** CountBadge is a count waiting for the administrator; it bounces when the count grows. */
export function CountBadge({ value, className }: { value: number; className?: string }) {
  const last = useRef(value);
  const [bounce, setBounce] = useState(0);
  useEffect(() => {
    if (value > last.current) setBounce((n) => n + 1);
    last.current = value;
  }, [value]);
  return (
    <span
      key={bounce}
      className={cn(
        "inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-warn px-1.5 text-xs font-semibold tabular-nums text-black",
        bounce > 0 && "animate-badge-pop",
        className,
      )}
    >
      {value > 99 ? "99+" : value}
    </span>
  );
}
