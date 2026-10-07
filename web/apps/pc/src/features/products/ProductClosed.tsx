import { routes } from "@exchange/core";
import type { ProductLine } from "@exchange/core/platform/products";
import { Button } from "@exchange/ui";
import { CirclePause } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { usePageTitle } from "../../pages/markets/hooks";

/**
 * ProductClosed is a closed product line's trading page (design
 * 2026-10-07, product line switches §1 #2): the product is paused, a way
 * home, and for a signed-in user the way to wind down what the paused
 * products still hold.
 */
export default function ProductClosed({ line, signedIn }: { line: ProductLine; signedIn: boolean }) {
  const { t } = useTranslation();
  const title = t("pcProducts.closed.title", { line: t(`pcProducts.lines.${line}`) });
  usePageTitle(title);
  return (
    <div className="mx-auto flex max-w-md flex-1 flex-col items-center justify-center px-6 py-16 text-center" data-testid="product-closed">
      <span className="grid size-20 place-items-center rounded-full bg-brand-soft text-brand">
        <CirclePause size={40} strokeWidth={1.5} aria-hidden />
      </span>
      <h1 className="mt-5 text-xl font-semibold text-fg-1">{title}</h1>
      <p className="mt-2 text-pretty text-sm leading-relaxed text-fg-3">{t("pcProducts.closed.desc")}</p>
      {signedIn && <p className="mt-1 text-pretty text-sm leading-relaxed text-fg-3">{t("pcProducts.closed.holding")}</p>}
      <div className="mt-6 flex gap-3">
        <Button asChild variant="secondary">
          <Link to={routes.home}>{t("pcProducts.closed.home")}</Link>
        </Button>
        {signedIn && (
          <Button asChild>
            <Link to={routes.closedProducts}>{t("pcProducts.closed.windDown")}</Link>
          </Button>
        )}
      </div>
    </div>
  );
}
