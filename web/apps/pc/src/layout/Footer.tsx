import { routes, setLocale, switchSite, useSettings } from "@exchange/core";
import { LEGAL_SLUGS, type LegalSlug } from "@exchange/core/content/index";
import { useBrandText, useBranding } from "@exchange/core/platform/index";
import { ChartCredit } from "@exchange/ui";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { Logo } from "./Logo";

/** The legal pages' labels in the footer (shared footer.* strings). */
export const LEGAL_LABELS: Record<LegalSlug, string> = {
  terms: "footer.terms",
  privacy: "footer.privacy",
  risk: "footer.riskDisclosure",
  fees: "footer.fees",
  about: "footer.aboutUs",
  contact: "footer.contact",
};

/**
 * Footer: product, support and legal links, the risk note, the switch to
 * the mobile site, the chart library's credit. The copyright, compliance
 * lines, contact and social links come from the platform profile (design
 * 2026-10-04 §4.1); the legal pages are the console's or the bundled drafts.
 */
export function Footer() {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const p = useBranding();
  const copyright = useBrandText((x) => x.footer.copyright);
  const compliance = useBrandText((x) => x.footer.compliance);
  return (
    <footer className="border-t border-line-1 bg-bg-1">
      <div className="mx-auto grid max-w-[1440px] grid-cols-[2fr_1fr_1fr_1fr_1fr] gap-8 px-6 py-10 text-sm">
        <div className="flex flex-col gap-3">
          <Logo />
          <p className="max-w-sm text-fg-3">{t("footer.risk")}</p>
          {p.social.length > 0 && (
            <div className="flex flex-wrap gap-x-3 gap-y-1 text-fg-2" aria-label={t("footer.follow")}>
              {p.social.map((s) => (
                <a key={s.kind} href={s.url} target="_blank" rel="noopener noreferrer" className="capitalize hover:text-fg-1">
                  {s.kind}
                </a>
              ))}
            </div>
          )}
        </div>
        <Column title={t("footer.products")}>
          <Link to={routes.markets}>{t("nav.markets")}</Link>
          <Link to={routes.trade("BTC-USDT")}>{t("nav.spot")}</Link>
          <Link to={routes.futures("BTC-USDT-PERP")}>{t("nav.futures")}</Link>
        </Column>
        <Column title={t("footer.support")}>
          <Link to={routes.help}>{t("nav.help")}</Link>
          <Link to={routes.announcements}>{t("nav.announcements")}</Link>
          <a href="/docs/">API</a>
          {p.contact.support_url && (
            <a href={p.contact.support_url} target="_blank" rel="noopener noreferrer">
              {t("footer.contact")}
            </a>
          )}
          {p.contact.email && <a href={`mailto:${p.contact.email}`}>{p.contact.email}</a>}
        </Column>
        <Column title={t("footer.legal")}>
          {LEGAL_SLUGS.map((slug) => (
            <Link key={slug} to={routes.legal(slug)}>
              {t(LEGAL_LABELS[slug])}
            </Link>
          ))}
        </Column>
        <Column title={t("footer.about")}>
          <button type="button" className="text-left" onClick={() => switchSite("m")}>
            {t("footer.toMobile")}
          </button>
          <button type="button" className="text-left" onClick={() => setLocale(locale === "zh-CN" ? "en" : "zh-CN")}>
            {locale === "zh-CN" ? "English" : "中文"}
          </button>
        </Column>
      </div>
      <div className="flex flex-col items-center gap-1 border-t border-line-1 py-4 text-center text-xs text-fg-3">
        <span>{copyright || t("footer.copyright")}</span>
        {compliance && <span className="whitespace-pre-line">{compliance}</span>}
        <ChartCredit />
      </div>
    </footer>
  );
}

function Column({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2 [&_a]:text-fg-2 [&_a:hover]:text-fg-1 [&_button]:text-fg-2 [&_button:hover]:text-fg-1">
      <div className="mb-1 font-medium text-fg-1">{title}</div>
      {children}
    </div>
  );
}
