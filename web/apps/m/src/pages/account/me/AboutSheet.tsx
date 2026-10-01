import { useTerms } from "@exchange/core/auth/register";
import { ChartCredit, Sheet, Skeleton } from "@exchange/ui";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** The build's commit (the deploy sets VITE_APP_VERSION); "dev" elsewhere. */
const VERSION = (import.meta.env.VITE_APP_VERSION as string | undefined) || "dev";

/**
 * AboutSheet (design §7.3 ⑦ 关于 Astras): what the site is, the build, the
 * versions of the terms and the risk disclosure in force, and the chart
 * library's credit.
 */
export function AboutSheet({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation();
  const terms = useTerms();
  const version = (v: string | undefined) => (terms.isPending ? <Skeleton className="h-4 w-20" /> : (v ?? "—"));
  return (
    <Sheet open={open} onOpenChange={onOpenChange} title={t("mAccount.me.about.title")} closeButton>
      <p className="text-sm leading-relaxed text-fg-2">{t("mAccount.me.about.learning")}</p>
      <dl className="mt-4 divide-y divide-line-1 overflow-hidden rounded-3 bg-bg-2">
        <Row label={t("mAccount.me.about.version")} value={VERSION} />
        <Row label={t("mAccount.me.about.terms")} value={version(terms.data?.terms_version)} />
        <Row label={t("mAccount.me.about.risk")} value={version(terms.data?.risk_disclosure_version)} />
      </dl>
      <ChartCredit className="mt-4 text-center" />
      <p className="mt-1 pb-2 text-center text-xs text-fg-3">{t("footer.copyright")}</p>
    </Sheet>
  );
}

function Row({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex min-h-12 items-center justify-between gap-3 px-4">
      <dt className="text-sm text-fg-3">{label}</dt>
      <dd className="text-sm text-fg-1 tabular-nums">{value}</dd>
    </div>
  );
}
