import { routes } from "@exchange/core";
import { useTerms } from "@exchange/core/auth/register";
import { useBrandText, useBranding, useTestMode } from "@exchange/core/platform/index";
import { ChartCredit, Sheet, Skeleton } from "@exchange/ui";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

/** The build's commit (the deploy sets VITE_APP_VERSION); "dev" elsewhere. */
const VERSION = (import.meta.env.VITE_APP_VERSION as string | undefined) || "dev";

/**
 * AboutSheet (design §7.3 ⑦): a line on the test mode while it is on
 * (design 2026-10-04 §4.3), the build, the versions of the terms and the
 * risk disclosure in force with their pages, and the chart library's credit.
 */
export function AboutSheet({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation();
  const terms = useTerms();
  const testMode = useTestMode().enabled;
  const copyright = useBrandText((p) => p.footer.copyright);
  const { contact, social } = useBranding();
  const version = (v: string | undefined) => (terms.isPending ? <Skeleton className="h-4 w-20" /> : (v ?? "—"));
  return (
    <Sheet open={open} onOpenChange={onOpenChange} title={t("mAccount.me.about.title")} closeButton>
      {testMode && <p className="mb-4 text-sm leading-relaxed text-fg-2">{t("mAccount.me.about.testMode")}</p>}
      <dl className="divide-y divide-line-1 overflow-hidden rounded-3 bg-bg-2">
        <Row label={t("mAccount.me.about.version")} value={VERSION} />
        <Row label={<Link to={routes.legal("terms")} className="text-brand">{t("mAccount.me.about.terms")}</Link>} value={version(terms.data?.terms_version)} />
        <Row label={<Link to={routes.legal("risk")} className="text-brand">{t("mAccount.me.about.risk")}</Link>} value={version(terms.data?.risk_disclosure_version)} />
      </dl>
      {(contact.email || contact.support_url || social.length > 0) && (
        <dl className="mt-3 divide-y divide-line-1 overflow-hidden rounded-3 bg-bg-2">
          {contact.email && <Row label={t("footer.contact")} value={<a href={`mailto:${contact.email}`} className="text-brand">{contact.email}</a>} />}
          {contact.support_url && (
            <Row label={t("nav.help")} value={<a href={contact.support_url} target="_blank" rel="noopener noreferrer" className="text-brand">↗</a>} />
          )}
          {social.map((s) => (
            <Row key={s.kind} label={<span className="capitalize">{s.kind}</span>} value={<a href={s.url} target="_blank" rel="noopener noreferrer" className="text-brand">↗</a>} />
          ))}
        </dl>
      )}
      <ChartCredit className="mt-4 text-center" />
      <p className="mt-1 pb-2 text-center text-xs text-fg-3">{copyright || t("footer.copyright")}</p>
    </Sheet>
  );
}

function Row({ label, value }: { label: ReactNode; value: ReactNode }) {
  return (
    <div className="flex min-h-12 items-center justify-between gap-3 px-4">
      <dt className="text-sm text-fg-3">{label}</dt>
      <dd className="text-sm text-fg-1 tabular-nums">{value}</dd>
    </div>
  );
}
