import { X } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { Dialog as RDialog } from "radix-ui";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";
import { durations, pop } from "../lib/motion";
import { useControllable } from "../lib/useControllable";
import { Button, type ButtonVariant } from "./Button";

export type DialogSize = "sm" | "md" | "lg" | "xl";

export type DialogProps = {
  open?: boolean;
  defaultOpen?: boolean;
  onOpenChange?: (open: boolean) => void;
  /** An element that opens the dialog (rendered asChild). */
  trigger?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  /** The action row; replaces the default cancel/confirm buttons. */
  footer?: ReactNode;
  /** With onConfirm and no footer, a cancel and a confirm button appear. */
  onConfirm?: () => void;
  confirmText?: ReactNode;
  cancelText?: ReactNode;
  confirmVariant?: ButtonVariant;
  confirmDisabled?: boolean;
  confirmLoading?: boolean;
  size?: DialogSize;
  /** Hide the × button (the footer must offer a way out). */
  hideClose?: boolean;
  /** Keep it open on outside clicks (forms with unsaved input). */
  persistent?: boolean;
  className?: string;
  bodyClassName?: string;
};

const sizes: Record<DialogSize, string> = { sm: "max-w-sm", md: "max-w-md", lg: "max-w-lg", xl: "max-w-2xl" };

/** DialogClose closes the dialog it sits in (wrap a custom button). */
export const DialogClose = RDialog.Close;

/**
 * Dialog is a modal on Radix Dialog (focus trap, Esc, scroll lock, focus
 * return) that pops in with the motion preset: a title, an optional
 * description, the content and a footer of actions.
 */
export function Dialog({
  open, defaultOpen, onOpenChange, trigger, title, description, children, footer, onConfirm, confirmText, cancelText,
  confirmVariant = "primary", confirmDisabled, confirmLoading, size = "md", hideClose, persistent, className, bodyClassName,
}: DialogProps) {
  const { t } = useTranslation();
  const [isOpen, setOpen] = useControllable(open, defaultOpen ?? false, onOpenChange);
  const actions =
    footer ??
    (onConfirm ? (
      <>
        <Button variant="secondary" onClick={() => setOpen(false)}>
          {cancelText ?? t("common.cancel")}
        </Button>
        <Button variant={confirmVariant} onClick={onConfirm} disabled={confirmDisabled} loading={confirmLoading}>
          {confirmText ?? t("common.confirm")}
        </Button>
      </>
    ) : null);
  return (
    <RDialog.Root open={isOpen} onOpenChange={setOpen}>
      {trigger && <RDialog.Trigger asChild>{trigger}</RDialog.Trigger>}
      <AnimatePresence>
        {isOpen && (
          <RDialog.Portal forceMount>
            <RDialog.Overlay asChild forceMount>
              <motion.div
                className="fixed inset-0 z-[var(--z-dialog)] bg-overlay"
                initial={{ opacity: 0 }}
                animate={{ opacity: 1, transition: { duration: durations.base } }}
                exit={{ opacity: 0, transition: { duration: durations.fast } }}
              />
            </RDialog.Overlay>
            <div className="pointer-events-none fixed inset-0 z-[var(--z-dialog)] flex items-center justify-center overflow-y-auto p-4">
              <RDialog.Content
                asChild
                forceMount
                // Without a description, say so (Radix otherwise warns).
                {...(description ? {} : { "aria-describedby": undefined })}
                onPointerDownOutside={persistent ? (e) => e.preventDefault() : undefined}
                onInteractOutside={persistent ? (e) => e.preventDefault() : undefined}
              >
                <motion.div
                  variants={pop}
                  initial="initial"
                  animate="animate"
                  exit="exit"
                  className={cn(
                    "pointer-events-auto relative flex max-h-[calc(100dvh-32px)] w-full flex-col rounded-3 border border-line-1 bg-bg-1 text-fg-1 shadow-pop outline-none",
                    sizes[size],
                    className,
                  )}
                >
                  <div className="flex items-start gap-3 px-5 pt-5">
                    <div className="min-w-0 flex-1">
                      <RDialog.Title className="text-md font-semibold text-fg-1">{title}</RDialog.Title>
                      {description && <RDialog.Description className="mt-1 text-sm text-fg-3">{description}</RDialog.Description>}
                    </div>
                    {!hideClose && (
                      <RDialog.Close
                        aria-label={t("common.close")}
                        className="-mr-1.5 -mt-1 grid size-8 shrink-0 place-items-center rounded-2 text-fg-3 transition-colors hover:bg-bg-2 hover:text-fg-1"
                      >
                        <X size={18} />
                      </RDialog.Close>
                    )}
                  </div>
                  {children !== undefined && <div className={cn("min-h-0 flex-1 overflow-y-auto px-5 py-4", bodyClassName)}>{children}</div>}
                  {actions && <div className={cn("flex justify-end gap-2 px-5 pb-5", children === undefined && "pt-4")}>{actions}</div>}
                </motion.div>
              </RDialog.Content>
            </div>
          </RDialog.Portal>
        )}
      </AnimatePresence>
    </RDialog.Root>
  );
}
