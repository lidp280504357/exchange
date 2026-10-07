import { routes } from "@exchange/core";
import { allOpen, PRODUCT_LINES, useOpenProducts, type OpenProducts } from "@exchange/core/platform/products";
import { hasWindDown, useWindDown } from "@exchange/core/platform/windDown";
import { Button } from "@exchange/ui";
import { TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

/**
 * WindDownNotice heads the assets overview while a closed product line
 * still holds something of the user's (design 2026-10-07, product line
 * switches §1 #2): what is left, and the way to wind it down. With every
 * line open it is nothing and asks for nothing.
 */
export function WindDownNotice() {
  const open = useOpenProducts();
  return allOpen(open) ? null : <Notice open={open} />;
}

function Notice({ open }: { open: OpenProducts }) {
  const { t } = useTranslation();
  const held = useWindDown(open);
  if (held.loading || !hasWindDown(held)) return null;
  const sep = t("pcProducts.notice.sep");
  const lines = PRODUCT_LINES.filter((l) => !open[l])
    .map((l) => t(`pcProducts.lines.${l}`))
    .join(sep);
  const orders = held.orders.length + held.spotOrders.length;
  const what = [
    held.positions.length > 0 && t("pcProducts.notice.positions", { count: held.positions.length }),
    orders > 0 && t("pcProducts.notice.orders", { count: orders }),
    held.balances.length > 0 && t("pcProducts.notice.balances"),
    held.margin.length > 0 && t("pcProducts.notice.margin"),
  ]
    .filter(Boolean)
    .join(sep);
  return (
    <div role="status" data-testid="wind-down-notice" className="mb-5 flex items-center gap-3 rounded-3 border border-warn/40 bg-warn/10 px-4 py-3 text-sm">
      <TriangleAlert size={18} aria-hidden className="shrink-0 text-warn" />
      <p className="min-w-0 flex-1 text-fg-1">
        <span className="font-medium">{t("pcProducts.notice.title")}</span> · {t("pcProducts.notice.text", { lines, what })}
      </p>
      <Button asChild size="sm">
        <Link to={routes.closedProducts}>{t("pcProducts.notice.go")}</Link>
      </Button>
    </div>
  );
}
