import { ApiError, errorText, i18n } from "@exchange/core";
import { ConfirmDialog, toast } from "@exchange/ui";
import { useQueryClient, type QueryKey } from "@tanstack/react-query";
import { useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

// Every dangerous operation goes through one confirmation (design §10.2,
// A3): the target, a reason of at least 10 characters for the audit log,
// and a confirmation word typed by hand; the outcome is a toast, an error
// with its trace ID to copy.

/** FormError is a check of a dialog's own fields failing before any call; its message shows as it is. */
export class FormError extends Error {}

/** errorToast shows a failed call in Chinese with its trace ID. */
export function errorToast(err: unknown, title?: ReactNode) {
  if (err instanceof FormError) {
    toast.error(err.message);
    return;
  }
  const trace = err instanceof ApiError && err.traceId ? err.traceId : undefined;
  // The key of an operation whose outcome is unknown, sent with another request (C5.5 ⑥).
  const text = err instanceof ApiError && err.code === "COMMON_IDEMPOTENCY_CONFLICT" ? i18n.t("admin.attempts.conflict") : errorText(err);
  toast.error(title ?? text, {
    description: title ? text : trace ? `trace ${trace}` : undefined,
    duration: 8000,
    action: trace ? { label: i18n.t("admin.common.copyTrace"), onClick: () => void navigator.clipboard?.writeText(trace) } : undefined,
  });
}

/** settled reports whether a failed call's outcome is known: an answer other than a server error or the key's own conflict. */
function settled(err: unknown): boolean {
  if (err instanceof FormError) return true;
  return err instanceof ApiError && err.status >= 400 && err.status < 500 && err.code !== "COMMON_IDEMPOTENCY_CONFLICT";
}

/**
 * useOperationKey keeps the Idempotency-Key of the operation a dialog
 * sends (C5.5 ⑥): every retry carries the same key until the outcome is
 * known, so the server never does it twice (a network failure, a server
 * error or the key's conflict keep it); once known, or the dialog closed,
 * the next submission is another operation with a new key.
 */
export function useOperationKey() {
  const key = useRef<string | null>(null);
  return useMemo(
    () => ({
      get: () => (key.current ??= crypto.randomUUID()),
      reset: () => {
        key.current = null;
      },
      failed: (err: unknown) => {
        if (settled(err)) key.current = null;
      },
    }),
    [],
  );
}

/** Notice is an outcome told without the success tone: the call worked, but not all of it (a force close filled in part). */
export type Notice = { info: ReactNode };

const isNotice = (v: unknown): v is Notice => typeof v === "object" && v !== null && "info" in v;

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
  /** The call, with the reason given and the operation's Idempotency-Key; its result goes to onDone. */
  run: (reason: string, key: string) => Promise<unknown>;
  /** The toast once it worked, or what to say of the call's result (a Notice when it worked only in part). */
  success: ReactNode | ((result: unknown) => ReactNode | Notice);
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
  const op = useOperationKey();
  const [inner, setInner] = useState(false);
  const open = controlled ?? inner;
  const setOpen = (o: boolean) => {
    if (!o) op.reset();
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
            const result = await run(reason, op.get());
            const said = typeof success === "function" ? success(result) : success;
            if (isNotice(said)) toast.info(said.info, { duration: 10000 });
            else toast.success(said);
            for (const key of invalidate ?? []) void qc.invalidateQueries({ queryKey: key });
            setOpen(false);
            onDone?.(result);
          } catch (err) {
            op.failed(err);
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
