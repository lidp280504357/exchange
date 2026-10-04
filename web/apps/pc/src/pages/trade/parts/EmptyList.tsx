import { Button, EmptyState } from "@exchange/ui";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** The order panel's root carries this id, so an empty list can send the cursor there. */
export const ORDER_FORM_ID = "order-form";

/** focusOrderForm puts the cursor in the order form's first number field (the price, or the amount). */
export function focusOrderForm(): void {
  document.getElementById(ORDER_FORM_ID)?.querySelector<HTMLInputElement>('input[inputmode="decimal"]:not([disabled])')?.focus();
}

/**
 * EmptyList is an empty list under the terminal (design §4.3): what would
 * show there, and a way forward, by default the order form.
 */
export function EmptyList({ title, hint, action }: { title: ReactNode; hint: ReactNode; action?: ReactNode }) {
  const { t } = useTranslation();
  return (
    <EmptyState
      compact
      title={title}
      description={hint}
      action={
        action ?? (
          <Button size="sm" variant="secondary" onClick={focusOrderForm}>
            {t("pcTrade.placeOrder")}
          </Button>
        )
      }
    />
  );
}
