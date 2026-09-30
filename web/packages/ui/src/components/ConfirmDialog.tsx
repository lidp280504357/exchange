import { TriangleAlert } from "lucide-react";
import { useEffect, useId, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";
import { Button } from "./Button";
import { Dialog } from "./Dialog";
import { Input } from "./Input";

export type ConfirmDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  /** What the action touches: a user, an order, a switch (shown in a box). */
  target: ReactNode;
  /** The word to type before confirming, e.g. the last 4 characters of an ID. */
  confirmWord: string;
  /** Minimum reason length in characters (default 10, design §10.2). */
  minReasonLength?: number;
  confirmText?: ReactNode;
  /** Red confirm button (default on: these are dangerous operations). */
  danger?: boolean;
  /** Runs with the trimmed reason; a promise keeps the button busy until it settles. */
  onConfirm: (reason: string) => void | Promise<void>;
  loading?: boolean;
  /** Extra content under the target (an approval timeline, a warning). */
  children?: ReactNode;
};

/** reasonLength counts characters as people see them (CJK and emoji count once). */
export function reasonLength(reason: string): number {
  return [...reason.trim()].length;
}

/**
 * ConfirmDialog guards dangerous admin operations (freeze, reject, disable,
 * switch changes, adjustments): it shows the target, asks for a reason of
 * at least 10 characters for the audit log, and enables the button only
 * after the confirmation word is typed.
 */
export function ConfirmDialog({
  open, onOpenChange, title, description, target, confirmWord, minReasonLength = 10, confirmText, danger = true, onConfirm, loading, children,
}: ConfirmDialogProps) {
  const { t } = useTranslation();
  const id = useId();
  const [reason, setReason] = useState("");
  const [word, setWord] = useState("");
  const [busy, setBusy] = useState(false);

  // Every opening starts blank: a reason is never reused by accident.
  useEffect(() => {
    if (!open) {
      setReason("");
      setWord("");
      setBusy(false);
    }
  }, [open]);

  const len = reasonLength(reason);
  const reasonOk = len >= minReasonLength;
  const wordOk = word.trim() === confirmWord;
  const ready = reasonOk && wordOk && !busy && !loading;
  // The word sits inside the sentence in any language: split around it.
  const [wordBefore = "", wordAfter = ""] = t("ui.confirm.typeWord", { word: "\u0000" }).split("\u0000");

  const confirm = async () => {
    if (!ready) return;
    const r = onConfirm(reason.trim());
    if (r instanceof Promise) {
      setBusy(true);
      try {
        await r;
      } finally {
        setBusy(false);
      }
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description={description}
      persistent
      footer={
        <>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button variant={danger ? "danger" : "primary"} disabled={!ready} loading={busy || loading} onClick={confirm}>
            {confirmText ?? t("common.confirm")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <div className={cn("flex gap-3 rounded-2 border p-3", danger ? "border-danger/40 bg-danger/10" : "border-line-1 bg-bg-2")}>
          <TriangleAlert size={18} className={cn("mt-0.5 shrink-0", danger ? "text-danger" : "text-warn")} />
          <div className="min-w-0 text-sm">
            <div className="text-xs text-fg-3">{t("ui.confirm.target")}</div>
            <div className="mt-0.5 break-all font-medium text-fg-1">{target}</div>
          </div>
        </div>
        {children}
        <div>
          <label htmlFor={`${id}-reason`} className="mb-1.5 flex items-center justify-between text-sm text-fg-2">
            <span>{t("ui.confirm.reason")}</span>
            <span className={cn("text-xs tabular-nums", reasonOk ? "text-success" : "text-fg-3")}>
              {len}/{minReasonLength}
            </span>
          </label>
          <textarea
            id={`${id}-reason`}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            rows={3}
            placeholder={t("ui.confirm.reasonPlaceholder", { n: minReasonLength })}
            aria-invalid={(reason.length > 0 && !reasonOk) || undefined}
            aria-describedby={`${id}-reason-hint`}
            className={cn(
              "w-full resize-none rounded-2 border bg-bg-2 px-3 py-2 text-sm text-fg-1 outline-none placeholder:text-fg-3",
              "focus-visible:border-brand focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-brand",
              reason.length > 0 && !reasonOk ? "border-danger" : "border-line-1",
            )}
          />
          <p id={`${id}-reason-hint`} className={cn("mt-1 text-xs", reason.length > 0 && !reasonOk ? "text-danger" : "text-fg-3")}>
            {t("ui.confirm.reasonShort", { n: minReasonLength })}
          </p>
        </div>
        <div>
          <label htmlFor={`${id}-word`} className="mb-1.5 block text-sm text-fg-2">
            {wordBefore}
            <code className="mx-1 rounded-1 bg-bg-3 px-1.5 py-0.5 font-mono text-fg-1">{confirmWord}</code>
            {wordAfter}
          </label>
          <Input
            id={`${id}-word`}
            value={word}
            onValueChange={setWord}
            autoComplete="off"
            spellCheck={false}
            error={word.length > 0 && !wordOk ? t("ui.confirm.wordMismatch") : undefined}
            onKeyDown={(e) => {
              if (e.key === "Enter") void confirm();
            }}
          />
        </div>
      </div>
    </Dialog>
  );
}
