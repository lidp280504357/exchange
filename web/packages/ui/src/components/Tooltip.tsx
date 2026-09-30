import { Tooltip as RTooltip } from "radix-ui";
import type { ReactNode } from "react";
import { cn } from "../lib/cn";

export type TooltipProps = {
  /** The tip; nothing renders a bare trigger. */
  content: ReactNode;
  /** The trigger: one element that can take a ref (a button, a span). */
  children: ReactNode;
  side?: "top" | "right" | "bottom" | "left";
  align?: "start" | "center" | "end";
  /** Delay before showing, in ms (default 300). */
  delay?: number;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  className?: string;
};

/** TooltipProvider shares the delay of the tooltips under it (optional). */
export const TooltipProvider = RTooltip.Provider;

/**
 * Tooltip explains an icon, a truncated text or an enum's raw code (design
 * §10.2). It brings its own provider, so it works anywhere.
 */
export function Tooltip({ content, children, side = "top", align = "center", delay = 300, open, onOpenChange, className }: TooltipProps) {
  if (content === null || content === undefined || content === false || content === "") return <>{children}</>;
  return (
    <RTooltip.Provider delayDuration={delay} skipDelayDuration={200}>
      <RTooltip.Root open={open} onOpenChange={onOpenChange}>
        <RTooltip.Trigger asChild>{children}</RTooltip.Trigger>
        <RTooltip.Portal>
          <RTooltip.Content
            side={side}
            align={align}
            sideOffset={6}
            collisionPadding={8}
            className={cn(
              "z-[var(--z-toast)] max-w-xs rounded-1 border border-line-2 bg-bg-3 px-2 py-1 text-xs leading-relaxed text-fg-1 shadow-pop",
              "origin-(--radix-tooltip-content-transform-origin) data-[state=closed]:animate-pop-out data-[state=delayed-open]:animate-pop-in data-[state=instant-open]:animate-pop-in",
              className,
            )}
          >
            {content}
            <RTooltip.Arrow className="fill-bg-3" width={10} height={5} />
          </RTooltip.Content>
        </RTooltip.Portal>
      </RTooltip.Root>
    </RTooltip.Provider>
  );
}
