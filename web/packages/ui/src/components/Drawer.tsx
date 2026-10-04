import { X } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Dialog as RDialog } from "radix-ui";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";
import { durations } from "../lib/motion";

export type DrawerProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  /** A line under the title (an ID, a status). */
  description?: ReactNode;
  /** Actions in the header, left of the close button. */
  actions?: ReactNode;
  children?: ReactNode;
  /** Width in px (default 640). */
  width?: number;
  className?: string;
};

/**
 * Drawer is the console's detail panel (design §10.2): it slides in from
 * the right over a light overlay, on Radix Dialog (focus trap, Esc, focus
 * return), with a fixed header and a scrolling body. With reduced motion it
 * appears and goes at once: no slide, and no fade of the overlay either.
 */
export function Drawer({ open, onOpenChange, title, description, actions, children, width = 640, className }: DrawerProps) {
  const { t } = useTranslation();
  const reduced = useReducedMotion();
  const enter = reduced ? 0 : durations.base;
  const leave = reduced ? 0 : durations.fast;
  return (
    <RDialog.Root open={open} onOpenChange={onOpenChange}>
      <AnimatePresence>
        {open && (
          <RDialog.Portal forceMount>
            <RDialog.Overlay asChild forceMount>
              <motion.div
                className="fixed inset-0 z-[var(--z-dialog)] bg-overlay"
                initial={{ opacity: 0 }}
                animate={{ opacity: 1, transition: { duration: enter } }}
                exit={{ opacity: 0, transition: { duration: leave } }}
              />
            </RDialog.Overlay>
            <RDialog.Content asChild forceMount {...(description ? {} : { "aria-describedby": undefined })}>
              <motion.div
                initial={{ x: "100%" }}
                animate={{ x: 0, transition: { duration: enter, ease: [0.2, 0, 0, 1] } }}
                exit={{ x: "100%", transition: { duration: leave } }}
                style={{ width: `min(${width}px, 100vw)` }}
                className={cn(
                  "fixed inset-y-0 right-0 z-[var(--z-dialog)] flex flex-col border-l border-line-1 bg-bg-1 text-fg-1 shadow-pop outline-none",
                  className,
                )}
              >
                <div className="flex items-start gap-3 border-b border-line-1 px-5 py-4">
                  <div className="min-w-0 flex-1">
                    <RDialog.Title className="truncate text-md font-semibold">{title}</RDialog.Title>
                    {description && <RDialog.Description className="mt-1 text-sm text-fg-3">{description}</RDialog.Description>}
                  </div>
                  {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
                  <RDialog.Close
                    aria-label={t("common.close")}
                    className="-mr-1.5 grid size-8 shrink-0 place-items-center rounded-2 text-fg-3 transition-colors hover:bg-bg-2 hover:text-fg-1"
                  >
                    <X size={18} />
                  </RDialog.Close>
                </div>
                <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">{children}</div>
              </motion.div>
            </RDialog.Content>
          </RDialog.Portal>
        )}
      </AnimatePresence>
    </RDialog.Root>
  );
}
