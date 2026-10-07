import { routes } from "@exchange/core";
import type { ProductLine } from "@exchange/core/platform/products";
import { Button } from "@exchange/ui";
import { CirclePause } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

/**
 * ProductClosed is a closed product line's trading page on the phone
 * (design 2026-10-07, product line switches §1 #2): the product is paused,
 * a way home, and for a signed-in user the way to wind down what the
 * paused products still hold.
 */
export default function ProductClosed({ line, signedIn }: { line: ProductLine; signedIn: boolean }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center px-6 pb-10 pt-16 text-center" data-testid="product-closed">
      <span className="grid size-20 place-items-center rounded-full bg-brand-soft text-brand">
        <CirclePause size={40} strokeWidth={1.5} aria-hidden />
      </span>
      <h1 className="mt-5 text-lg font-semibold text-fg-1">{t("mProducts.closed.title", { line: t(`mProducts.lines.${line}`) })}</h1>
      <p className="mt-2 max-w-xs text-pretty text-sm leading-relaxed text-fg-3">{t("mProducts.closed.desc")}</p>
      {signedIn && <p className="mt-2 max-w-xs text-pretty text-sm leading-relaxed text-fg-3">{t("mProducts.closed.holding")}</p>}
      <div className="mt-6 flex w-full max-w-xs flex-col gap-2">
        {signedIn && (
          <Button asChild size="lg" block>
            <Link to={routes.closedProducts}>{t("mProducts.closed.windDown")}</Link>
          </Button>
        )}
        <Button asChild size="lg" block variant="secondary">
          <Link to={routes.home}>{t("mProducts.closed.home")}</Link>
        </Button>
      </div>
    </div>
  );
}
