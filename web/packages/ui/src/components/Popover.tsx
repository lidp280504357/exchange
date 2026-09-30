import { Popover as RPopover } from "radix-ui";
import type { ReactNode } from "react";
import { cn } from "../lib/cn";

export type PopoverProps = {
  /** The element that opens it (rendered asChild). */
  trigger: ReactNode;
  children: ReactNode;
  open?: boolean;
  defaultOpen?: boolean;
  onOpenChange?: (open: boolean) => void;
  side?: "top" | "right" | "bottom" | "left";
  align?: "start" | "center" | "end";
  sideOffset?: number;
  /** Draw an arrow pointing at the trigger. */
  arrow?: boolean;
  /** Stay open while the page scrolls or the trigger moves out (default on). */
  modal?: boolean;
  className?: string;
  "aria-label"?: string;
};

/** PopoverClose closes the popover it sits in (for buttons inside). */
export const PopoverClose = RPopover.Close;

/**
 * Popover is floating content attached to a trigger (filters, settings,
 * the pair switcher): it closes on Esc and outside clicks and returns the
 * focus to the trigger.
 */
export function Popover({
  trigger, children, open, defaultOpen, onOpenChange, side = "bottom", align = "center", sideOffset = 6, arrow, modal, className,
  "aria-label": ariaLabel,
}: PopoverProps) {
  return (
    <RPopover.Root open={open} defaultOpen={defaultOpen} onOpenChange={onOpenChange} modal={modal}>
      <RPopover.Trigger asChild>{trigger}</RPopover.Trigger>
      <RPopover.Portal>
        <RPopover.Content
          side={side}
          align={align}
          sideOffset={sideOffset}
          collisionPadding={8}
          aria-label={ariaLabel}
          className={cn(
            "z-[var(--z-dropdown)] max-w-[calc(100vw-16px)] rounded-2 border border-line-1 bg-bg-2 p-3 text-sm text-fg-1 shadow-pop outline-none",
            "origin-(--radix-popover-content-transform-origin) data-[state=open]:animate-pop-in data-[state=closed]:animate-pop-out",
            className,
          )}
        >
          {children}
          {arrow && <RPopover.Arrow className="fill-bg-2" width={12} height={6} />}
        </RPopover.Content>
      </RPopover.Portal>
    </RPopover.Root>
  );
}
