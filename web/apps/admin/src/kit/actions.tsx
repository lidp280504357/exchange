import { ApiError, errorText, i18n } from "@exchange/core";
import { ConfirmDialog, toast } from "@exchange/ui";
import { useQueryClient, type QueryKey } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

// Every dangerous operation goes through one confirmation (design §10.2,
// A3): the target, a reason of at least 10 characters for the audit log,
// and a confirmation word typed by hand; the outcome is a toast, an error
// with its trace ID to copy.

/** errorToast shows a failed call in Chinese with its trace ID. */
export function errorToast(err: unknown, title?: ReactNode) {
  const trace = err instanceof ApiError && err.traceId ? err.traceId : undefined;
  toast.error(title ?? errorText(err), {
    description: title ? errorText(err) : trace ? `trace ${trace}` : undefined,
    duration: 8000,
    action: trace ? { label: i18n.t("admin.common.copyTrace"), onClick: () => void navigator.clipboard?.writeText(trace) } : undefined,
  });
}

export type DangerActionProps = {
  /** Renders the button that opens the confirmation; leave it out to control open yourself. */
  trigger?: (open: () => void) => ReactNode;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  target: ReactNode;
  /** The word to type: the last 4 characters of the target's ID by convention. */
  confirmWord: string;
  confirmText?: ReactNode;
  danger?: boolean;
  /** The call, with the reason given; its result goes to onDone. */
  run: (reason: string) => Promise<unknown>;
  success: ReactNode;
  /** Lists to reload after it worked. */
  invalidate?: QueryKey[];
  onDone?: (result: unknown) => void;
  children?: ReactNode;
};

export function DangerAction({
  trigger, open: controlled, onOpenChange, title, description, target, confirmWord, confirmText, danger = true, run, success, invalidate, onDone,
  children,
}: DangerActionProps) {
  const qc = useQueryClient();
  const [inner, setInner] = useState(false);
  const open = controlled ?? inner;
  const setOpen = (o: boolean) => {
    if (controlled === undefined) setInner(o);
    onOpenChange?.(o);
  };
  return (
    <>
      {trigger?.(() => setOpen(true))}
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={title}
        description={description}
        target={target}
        confirmWord={confirmWord}
        confirmText={confirmText}
        danger={danger}
        onConfirm={async (reason) => {
          try {
            const result = await run(reason);
            toast.success(success);
            for (const key of invalidate ?? []) void qc.invalidateQueries({ queryKey: key });
            setOpen(false);
            onDone?.(result);
          } catch (err) {
            errorToast(err);
          }
        }}
      >
        {children}
      </ConfirmDialog>
    </>
  );
}

/** lastFour is the confirmation word of an ID. */
export const lastFour = (id: string) => id.replace(/-/g, "").slice(-4);

/** useT is the console's translator with its namespace. */
export function useAdminT() {
  const { t } = useTranslation();
  return (key: string, opts?: Record<string, unknown>) => t(`admin.${key}`, opts);
}
