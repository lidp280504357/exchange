import { Button, EmptyState } from "@exchange/ui";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

/**
 * EmptyList is an empty list under the terminal (design §4.3): what would
 * show there, and a way forward, by default the order sheet (onTrade).
 */
export function EmptyList({ title, hint, onTrade, action }: { title: ReactNode; hint: ReactNode; onTrade?: () => void; action?: ReactNode }) {
  const { t } = useTranslation();
  return (
    <EmptyState
      compact
      title={title}
      description={hint}
      action={
        action ??
        (onTrade && (
          <Button size="sm" variant="secondary" className="hit-area" onClick={onTrade}>
            {t("mTrade.placeOrder")}
          </Button>
        ))
      }
    />
  );
}
