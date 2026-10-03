import { ApiError, i18n } from "@exchange/core";
import type { AdminSchemas } from "@exchange/core/api/admin";
import { ConfirmDialog, toast } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { settingsKey, todoKey } from "../live";
import { errorToast, useOperationKey } from "./actions";

// Fund operations (manual adjustments, insurance fund contributions,
// deposit backfills, deposits of nobody credited to a user) end
// three ways: booked at once (single-person mode within the limits),
// waiting for a second administrator, or refused by the ledger. The
// administrator is told which, with the journal or the reason.

export type Approval = AdminSchemas["Approval"];

/** announce tells what came of a fund operation. */
export function announce(a: Approval) {
  const t = i18n.t.bind(i18n);
  if (a.status === "EXECUTED") {
    // A backfill books a deposit (the ledger follows shortly), not a journal.
    if (a.kind === "DEPOSIT_BACKFILL") {
      toast.success(t("admin.backfill.booked"), { description: t("admin.backfill.done", { id: a.result.replace(/^deposit /, "") }), duration: 6000 });
      return;
    }
    toast.success(t("admin.funds.executed"), { description: t("admin.funds.journal", { id: a.journal_id ?? "—" }), duration: 6000 });
  } else if (a.status === "FAILED") {
    toast.error(t("admin.funds.failed"), { description: a.result, duration: 10000 });
  } else if (a.status === "REJECTED") {
    toast.success(t("admin.funds.rejected"), { description: a.result });
  } else {
    toast.info(t("admin.funds.pending"), { description: t(`admin.funds.escalation.${a.escalation || "REQUESTED"}`), duration: 8000 });
  }
}

/** fundError reports a failed call; an unknown outcome names the operation left pending. */
export function fundError(err: unknown) {
  const id = err instanceof ApiError ? (err.details?.approval_id as string | undefined) : undefined;
  if (id) {
    toast.error(i18n.t("admin.funds.unknown"), { description: i18n.t("admin.funds.unknownHint", { id: id.slice(-8) }), duration: 12000 });
    return;
  }
  errorToast(err);
}

export type FundActionProps = {
  trigger: (open: () => void) => ReactNode;
  title: ReactNode;
  description?: ReactNode;
  target: ReactNode;
  confirmWord: string;
  confirmText?: ReactNode;
  danger?: boolean;
  /** The call, with the reason given and the operation's Idempotency-Key (the same for each retry until the outcome is known). */
  run: (reason: string, key: string) => Promise<Approval>;
  onDone?: (a: Approval) => void;
  /** Keeps the confirm button off while what the dialog asks for is incomplete (review ㉕). */
  disabled?: boolean;
  children?: ReactNode;
};

/**
 * NO_WORD is the confirmation word of a disabled action: ConfirmDialog
 * compares what is typed trimmed, so a blank word never matches.
 */
const NO_WORD = " ";

/** FundAction confirms a fund operation (reason and confirmation word) and announces its outcome. */
export function FundAction({
  trigger, title, description, target, confirmWord, confirmText, danger, run, onDone, disabled, children,
}: FundActionProps) {
  const qc = useQueryClient();
  const op = useOperationKey();
  const [open, setOpenState] = useState(false);
  const setOpen = (o: boolean) => {
    if (!o) op.reset();
    setOpenState(o);
  };
  return (
    <>
      {trigger(() => setOpen(true))}
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={title}
        description={description}
        target={target}
        confirmWord={disabled ? NO_WORD : confirmWord}
        confirmText={confirmText}
        danger={danger}
        onConfirm={async (reason) => {
          if (disabled) return;
          try {
            const a = await run(reason, op.get());
            announce(a);
            setOpen(false);
            onDone?.(a);
          } catch (err) {
            op.failed(err);
            fundError(err);
          } finally {
            for (const key of [["admin", "approvals"], todoKey, settingsKey, ["admin", "user"], ["admin", "derivatives"], ["admin", "deposits"]]) {
              void qc.invalidateQueries({ queryKey: key });
            }
          }
        }}
      >
        {children}
      </ConfirmDialog>
    </>
  );
}
