import { X } from "lucide-react";
import { AnimatePresence, motion, useDragControls, useReducedMotion, type PanInfo } from "motion/react";
import { Dialog as RDialog } from "radix-ui";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";
import { durations, sheetSpring } from "../lib/motion";
import { useControllable } from "../lib/useControllable";

export type SheetProps = {
  open?: boolean;
  defaultOpen?: boolean;
  onOpenChange?: (open: boolean) => void;
  /** An element that opens the sheet (rendered asChild). */
  trigger?: ReactNode;
  /** Required for screen readers; hideTitle keeps it off screen. */
  title: ReactNode;
  hideTitle?: boolean;
  description?: ReactNode;
  children?: ReactNode;
  /** A fixed bottom row (the big submit button), above the safe area. */
  footer?: ReactNode;
  /** Show the × button in the header (the handle and a swipe also close). */
  closeButton?: boolean;
  className?: string;
  bodyClassName?: string;
};

/** Pull further than this, or flick faster, and the sheet closes. */
export const SHEET_CLOSE_DISTANCE = 96;
export const SHEET_CLOSE_VELOCITY = 500;

/** shouldClose decides at the end of a drag whether the sheet goes away. */
export function shouldClose(offsetY: number, velocityY: number): boolean {
  return offsetY > SHEET_CLOSE_DISTANCE || velocityY > SHEET_CLOSE_VELOCITY;
}

/**
 * Sheet is the mobile bottom sheet (design §7.1: orders, pair switch,
 * filters, confirmations, step-up). Radix Dialog traps the focus; motion
 * springs it in (stiffness 400, damping 40) and lets the handle drag it
 * down: past 96 px or a quick flick it closes, otherwise it springs back.
 * At most 90% of the screen tall, padded above the home indicator.
 */
export function Sheet({
  open, defaultOpen, onOpenChange, trigger, title, hideTitle, description, children, footer, closeButton, className, bodyClassName,
}: SheetProps) {
  const { t } = useTranslation();
  const [isOpen, setOpen] = useControllable(open, defaultOpen ?? false, onOpenChange);
  const reduced = useReducedMotion();
  const drag = useDragControls();
  const transition = reduced ? { duration: 0 } : sheetSpring;

  const dragEnd = (_: unknown, info: PanInfo) => {
    if (shouldClose(info.offset.y, info.velocity.y)) setOpen(false);
  };

  return (
    <RDialog.Root open={isOpen} onOpenChange={setOpen}>
      {trigger && <RDialog.Trigger asChild>{trigger}</RDialog.Trigger>}
      <AnimatePresence>
        {isOpen && (
          <RDialog.Portal forceMount>
            <RDialog.Overlay asChild forceMount>
              <motion.div
                className="fixed inset-0 z-[var(--z-sheet)] bg-overlay"
                initial={{ opacity: 0 }}
                animate={{ opacity: 1, transition: { duration: reduced ? 0 : durations.base } }}
                exit={{ opacity: 0, transition: { duration: reduced ? 0 : durations.base } }}
              />
            </RDialog.Overlay>
            <RDialog.Content asChild forceMount {...(description ? {} : { "aria-describedby": undefined })}>
              <motion.div
                initial={{ y: "100%" }}
                animate={{ y: 0 }}
                exit={{ y: "100%" }}
                transition={transition}
                drag="y"
                dragControls={drag}
                dragListener={false}
                dragConstraints={{ top: 0, bottom: 0 }}
                dragElastic={{ top: 0, bottom: 1 }}
                onDragEnd={dragEnd}
                className={cn(
                  "fixed inset-x-0 bottom-0 z-[var(--z-sheet)] mx-auto flex max-h-[90dvh] w-full max-w-[480px] flex-col rounded-t-3 border-t border-line-1 bg-bg-1 text-fg-1 shadow-pop outline-none",
                  className,
                )}
              >
                <div
                  onPointerDown={(e) => drag.start(e)}
                  className="shrink-0 cursor-grab touch-none select-none px-4 pb-2 pt-2 active:cursor-grabbing"
                >
                  <div aria-hidden title={t("ui.dragToClose")} className="mx-auto h-1 w-10 rounded-full bg-line-2" />
                  <div className={cn("flex items-center gap-2", !(hideTitle && !closeButton) && "mt-2")}>
                    <RDialog.Title className={cn("min-w-0 flex-1 truncate text-md font-semibold", hideTitle && "sr-only")}>{title}</RDialog.Title>
                    {closeButton && (
                      <RDialog.Close
                        aria-label={t("common.close")}
                        onPointerDown={(e) => e.stopPropagation()}
                        className="-mr-1 grid size-9 place-items-center rounded-2 text-fg-3 hover:bg-bg-2 hover:text-fg-1"
                      >
                        <X size={18} />
                      </RDialog.Close>
                    )}
                  </div>
                  {description && <RDialog.Description className="mt-1 text-sm text-fg-3">{description}</RDialog.Description>}
                </div>
                <div
                  className={cn("min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 pb-4", bodyClassName)}
                  style={footer ? undefined : { paddingBottom: "max(16px, env(safe-area-inset-bottom))" }}
                >
                  {children}
                </div>
                {footer && (
                  <div
                    className="shrink-0 border-t border-line-1 px-4 pt-3"
                    style={{ paddingBottom: "max(12px, env(safe-area-inset-bottom))" }}
                  >
                    {footer}
                  </div>
                )}
              </motion.div>
            </RDialog.Content>
          </RDialog.Portal>
        )}
      </AnimatePresence>
    </RDialog.Root>
  );
}
