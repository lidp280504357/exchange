import { ApiError, i18n } from "@exchange/core";
import type { AdminSchemas } from "@exchange/core/api/admin";
import { ConfirmDialog, toast } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { settingsKey, todoKey } from "../live";
import { errorToast } from "./actions";

// Fund operations (manual adjustments, insurance fund contributions) end
// three ways: booked at once (single-person mode within the limits),
// waiting for a second administrator, or refused by the ledger. The
// administrator is told which, with the journal or the reason.

export type Approval = AdminSchemas["Approval"];

/** announce tells what came of a fund operation. */
export function announce(a: Approval) {
  const t = i18n.t.bind(i18n);
  if (a.status === "EXECUTED") {
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
  run: (reason: string) => Promise<Approval>;
  onDone?: (a: Approval) => void;
  children?: ReactNode;
};

/** FundAction confirms a fund operation (reason and confirmation word) and announces its outcome. */
export function FundAction({ trigger, title, description, target, confirmWord, confirmText, danger, run, onDone, children }: FundActionProps) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  return (
    <>
      {trigger(() => setOpen(true))}
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
            const a = await run(reason);
            announce(a);
            setOpen(false);
            onDone?.(a);
          } catch (err) {
            fundError(err);
          } finally {
            for (const key of [["admin", "approvals"], todoKey, settingsKey, ["admin", "user"], ["admin", "derivatives"]]) {
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
