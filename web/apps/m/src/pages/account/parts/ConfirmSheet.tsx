import { Button, Sheet } from "@exchange/ui";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

export type ConfirmSheetProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  confirmText: ReactNode;
  cancelText?: ReactNode;
  /** danger for signing out, revoking and removing. */
  tone?: "primary" | "danger";
  /** The action runs: the buttons wait and the sheet stays. */
  loading?: boolean;
  onConfirm: () => void;
  children?: ReactNode;
};

/**
 * ConfirmSheet asks before an action that cannot be undone from here
 * (design §7.1: confirmations are sheets): the question, what happens,
 * then confirm and cancel as full-width buttons above the safe area
 * (stacked: long labels such as "退出其他所有设备" fit a 360 px screen).
 */
export function ConfirmSheet({
  open, onOpenChange, title, description, confirmText, cancelText, tone = "primary", loading, onConfirm, children,
}: ConfirmSheetProps) {
  const { t } = useTranslation();
  return (
    <Sheet
      open={open}
      onOpenChange={(o) => !loading && onOpenChange(o)}
      title={title}
      description={description}
      footer={
        <div className="flex flex-col gap-2">
          <Button variant={tone} size="lg" block loading={loading} onClick={onConfirm}>
            {confirmText}
          </Button>
          <Button variant="secondary" size="lg" block disabled={loading} onClick={() => onOpenChange(false)}>
            {cancelText ?? t("common.cancel")}
          </Button>
        </div>
      }
    >
      {children}
    </Sheet>
  );
}
