import { errorText, routes, useSettings } from "@exchange/core";
import { LEGAL_SLUGS, useArticle, type LegalSlug } from "@exchange/core/content/index";
import { EmptyState, ErrorState, Skeleton, SkeletonLines, cn } from "@exchange/ui";
import { Languages } from "lucide-react";
import { useTranslation } from "react-i18next";
import { NavLink, useParams } from "react-router";
import { usePageHeader } from "../../layout/header";
import { Prose, Toc, useHashScroll } from "./parts";

/** The legal pages' labels (shared footer.* strings). */
const LABELS: Record<LegalSlug, string> = {
  terms: "footer.terms",
  privacy: "footer.privacy",
  risk: "footer.riskDisclosure",
  fees: "footer.fees",
  about: "footer.aboutUs",
  contact: "footer.contact",
};

/**
 * Legal (/legal/:slug, design 2026-10-04 §4.4) on the phone: the terms,
 * privacy, risk, fees, about and contact pages, the console's version or
 * the bundled draft, with the six pages as chips on top.
 */
export default function Legal() {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const { slug = "" } = useParams();
  const known = (LEGAL_SLUGS as readonly string[]).includes(slug);
  const article = useArticle("legal", slug);
  const a = known ? article.data : null;
  usePageHeader({ title: known ? t(LABELS[slug as LegalSlug]) : t("footer.legal"), back: routes.me }, [slug, t]);
  useHashScroll(Boolean(a));

  let body;
  if (known && article.isPending) {
    body = (
      <div>
        <Skeleton className="h-7 w-2/3" />
        <SkeletonLines lines={10} className="mt-5" />
      </div>
    );
  } else if (known && article.isError) {
    body = <ErrorState message={errorText(article.error)} onRetry={() => void article.refetch()} />;
  } else if (!a) {
    body = <EmptyState title={t("mContent.article.notFound")} description={t("mContent.article.notFoundHint")} />;
  } else {
    body = (
      <article key={`${a.slug}.${a.locale}`} className="animate-fade-in">
        <h1 className="text-xl font-semibold leading-snug text-fg-1">{a.title}</h1>
        {a.summary && <p className="mt-2 text-sm leading-relaxed text-fg-2">{a.summary}</p>}
        {a.fallback && (
          <div role="note" className="mt-4 flex items-start gap-2 rounded-2 border border-info/30 bg-info/10 px-3 py-2.5 text-xs text-info">
            <Languages size={14} className="mt-0.5 shrink-0" />
            {t(locale === "zh-TW" ? "mContent.article.fallbackTraditional" : "mContent.article.fallback")}
          </div>
        )}
        {a.doc.toc.length > 1 && <Toc items={a.doc.toc} className="mt-4" />}
        <Prose doc={a.doc} className="mt-5" />
      </article>
    );
  }

  return (
    <div className="px-4 pb-10 pt-3">
      <nav aria-label={t("footer.legal")} className="-mx-4 mb-4 flex gap-2 overflow-x-auto px-4 pb-1">
        {LEGAL_SLUGS.map((s) => (
          <NavLink
            key={s}
            to={routes.legal(s)}
            replace
            className={({ isActive }) =>
              cn("shrink-0 rounded-full border px-3 py-1.5 text-xs", isActive ? "border-brand text-brand" : "border-line-2 text-fg-2")
            }
          >
            {t(LABELS[s])}
          </NavLink>
        ))}
      </nav>
      {body}
    </div>
  );
}
