import { Spinner, cn } from "@exchange/ui";
import { ArrowDown } from "lucide-react";
import { useRef, useState, type ReactNode, type TouchEvent } from "react";
import { useTranslation } from "react-i18next";

const TRIGGER = 64; // px of pull that refreshes
const MAX = 96;

/**
 * PullToRefresh (design §7.1): pulling down at the top of the page shows
 * an arrow, past 64 px a release refreshes (onRefresh's promise keeps the
 * spinner until it settles). Only transform moves; the page scrolls
 * normally when it is not at the top.
 */
export function PullToRefresh({ onRefresh, children, className }: { onRefresh: () => Promise<unknown>; children: ReactNode; className?: string }) {
  const { t } = useTranslation();
  const start = useRef<{ x: number; y: number } | null>(null);
  // The gesture's direction, decided on its first few pixels: a sideways
  // swipe (tabs, carousels) never pulls the page.
  const axis = useRef<"x" | "y" | null>(null);
  const [pull, setPull] = useState(0);
  const [busy, setBusy] = useState(false);

  const down = (e: TouchEvent) => {
    // Sheets are portaled out of this element but stay its children in
    // React's tree, so their touches bubble up here: dragging a sheet down
    // must not pull the page behind it.
    if (!e.currentTarget.contains(e.target as Node)) return;
    if (busy || window.scrollY > 0) return;
    start.current = { x: e.touches[0]!.clientX, y: e.touches[0]!.clientY };
    axis.current = null;
  };
  const move = (e: TouchEvent) => {
    if (start.current === null) return;
    const dx = e.touches[0]!.clientX - start.current.x;
    const dy = e.touches[0]!.clientY - start.current.y;
    if (axis.current === null) {
      if (Math.abs(dx) < 8 && Math.abs(dy) < 8) return;
      axis.current = Math.abs(dx) > Math.abs(dy) ? "x" : "y";
    }
    if (axis.current === "x") return;
    if (dy <= 0) return setPull(0);
    // Resistance: the content follows at half the finger's distance.
    setPull(Math.min(MAX, dy / 2));
  };
  const up = () => {
    if (start.current === null) return;
    start.current = null;
    axis.current = null;
    if (pull >= TRIGGER / 2 + 16) {
      setBusy(true);
      setPull(TRIGGER / 2 + 8);
      void onRefresh().finally(() => {
        setBusy(false);
        setPull(0);
      });
    } else setPull(0);
  };

  const ready = pull >= TRIGGER / 2 + 16;
  return (
    <div onTouchStart={down} onTouchMove={move} onTouchEnd={up} onTouchCancel={up} className={cn("relative", className)}>
      <div
        aria-live="polite"
        className="pointer-events-none absolute inset-x-0 top-0 flex h-12 items-center justify-center text-xs text-fg-3"
        style={{ opacity: pull > 4 || busy ? 1 : 0, transform: `translateY(${pull - 48}px)` }}
      >
        {busy ? (
          <Spinner size={16} />
        ) : (
          <span className="flex items-center gap-1">
            <ArrowDown size={14} className={cn("transition-transform duration-[var(--t-fast)]", ready && "rotate-180")} />
            {ready ? t("m.releaseToRefresh") : t("m.pullToRefresh")}
          </span>
        )}
      </div>
      <div
        style={{ transform: pull ? `translateY(${pull}px)` : undefined }}
        className={cn(start.current === null && "transition-transform duration-[var(--t-base)] ease-out")}
      >
        {children}
      </div>
    </div>
  );
}
