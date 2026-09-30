import { Tabs } from "@exchange/ui";
import { useRef, type ReactNode, type TouchEvent } from "react";

export type SwipeTab = { value: string; label: ReactNode; count?: number; content: ReactNode };

/**
 * SwipeTabs (design §7.1): an underline tab bar whose panels also change
 * with a horizontal swipe. Every panel stays mounted (hidden when not
 * shown), so the chart and the book keep their state between tabs.
 */
export function SwipeTabs({ tabs, value, onValueChange, className }: { tabs: SwipeTab[]; value: string; onValueChange: (v: string) => void; className?: string }) {
  const start = useRef<{ x: number; y: number } | null>(null);
  const at = tabs.findIndex((t) => t.value === value);
  const down = (e: TouchEvent) => {
    const p = e.touches[0]!;
    start.current = { x: p.clientX, y: p.clientY };
  };
  const up = (e: TouchEvent) => {
    const s = start.current;
    start.current = null;
    if (!s) return;
    const p = e.changedTouches[0]!;
    const dx = p.clientX - s.x;
    const dy = p.clientY - s.y;
    if (Math.abs(dx) < 60 || Math.abs(dx) < Math.abs(dy) * 1.5) return;
    const next = tabs[at + (dx < 0 ? 1 : -1)];
    if (next) onValueChange(next.value);
  };
  return (
    <div className={className}>
      <Tabs
        value={value}
        onValueChange={onValueChange}
        size="sm"
        listClassName="px-4"
        items={tabs.map((t) => ({ value: t.value, label: t.label, count: t.count }))}
      />
      <div onTouchStart={down} onTouchEnd={up}>
        {tabs.map((t) => (
          <div key={t.value} role="tabpanel" hidden={t.value !== value}>
            {t.content}
          </div>
        ))}
      </div>
    </div>
  );
}
