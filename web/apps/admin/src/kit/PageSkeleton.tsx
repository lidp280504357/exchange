import { Skeleton } from "@exchange/ui";
import { useTranslation } from "react-i18next";

/**
 * PageSkeleton holds a section's place while its chunk loads (A40): a
 * title, its line of help and a card of rows in the shape of the page to
 * come, instead of an empty content area.
 */
export function PageSkeleton() {
  const { t } = useTranslation();
  return (
    <div role="status" aria-busy="true" aria-label={t("common.loading")} data-testid="page-skeleton" className="flex flex-col gap-4">
      <div>
        <Skeleton className="h-7 w-48" />
        <Skeleton className="mt-2 h-4 w-96 max-w-full" />
      </div>
      <div className="card flex flex-col gap-3 p-4">
        {Array.from({ length: 8 }, (_, i) => (
          <Skeleton key={i} className="h-5" style={{ width: `${100 - (i % 3) * 12}%` }} />
        ))}
      </div>
    </div>
  );
}
