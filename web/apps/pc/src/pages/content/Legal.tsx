import { errorText, routes, useSettings } from "@exchange/core";
import { LEGAL_SLUGS, useArticle } from "@exchange/core/content/index";
import { EmptyState, ErrorState, Skeleton, SkeletonLines, cn } from "@exchange/ui";
import { Languages } from "lucide-react";
import { useTranslation } from "react-i18next";
import { NavLink, useParams } from "react-router";
import { LEGAL_LABELS } from "../../layout/Footer";
import { usePageTitle } from "../markets/hooks";
import { Prose, Toc, useHashScroll } from "./parts";

/**
 * Legal (/legal/:slug, design 2026-10-04 §4.4): the terms, privacy, risk,
 * fees, about and contact pages; the console's version when it published
 * one, else the bundled draft. The six pages are listed on the left.
 */
export default function Legal() {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const { slug = "" } = useParams();
  const known = (LEGAL_SLUGS as readonly string[]).includes(slug);
  const article = useArticle("legal", slug);
  const a = known ? article.data : null;
  usePageTitle(a?.title);
  useHashScroll(Boolean(a));

  let body;
  if (known && article.isPending) {
    body = (
      <div className="max-w-3xl">
        <Skeleton className="h-8 w-2/3" />
        <SkeletonLines lines={10} className="mt-6" />
      </div>
    );
  } else if (known && article.isError) {
    body = <ErrorState message={errorText(article.error)} onRetry={() => void article.refetch()} />;
  } else if (!a) {
    body = <EmptyState title={t("pcContent.article.notFound")} description={t("pcContent.article.notFoundHint")} />;
  } else {
    body = (
      <article className="max-w-3xl">
        <h1 className="text-2xl font-semibold leading-tight text-fg-1">{a.title}</h1>
        {a.summary && <p className="mt-3 text-md leading-relaxed text-fg-2">{a.summary}</p>}
        {a.fallback && (
          <div role="note" className="mt-5 flex items-center gap-2 rounded-2 border border-info/30 bg-info/10 px-4 py-2.5 text-sm text-info">
            <Languages size={16} className="shrink-0" />
            {t(locale === "zh-TW" ? "pcContent.article.fallbackTraditional" : "pcContent.article.fallback")}
          </div>
        )}
        <Prose doc={a.doc} className="mt-6" />
      </article>
    );
  }

  return (
    <div className="mx-auto grid max-w-[1440px] grid-cols-[200px_minmax(0,1fr)] gap-8 px-6 py-8 xl:grid-cols-[220px_minmax(0,1fr)_220px] xl:gap-10">
      <nav aria-label={t("footer.legal")} className="sticky top-20 flex flex-col gap-1 self-start text-sm">
        <div className="mb-2 font-medium text-fg-1">{t("footer.legal")}</div>
        {LEGAL_SLUGS.map((s) => (
          <NavLink
            key={s}
            to={routes.legal(s)}
            className={({ isActive }) =>
              cn("rounded-2 px-3 py-1.5 transition-colors", isActive ? "bg-bg-2 font-medium text-fg-1" : "text-fg-2 hover:bg-bg-2 hover:text-fg-1")
            }
          >
            {t(LEGAL_LABELS[s])}
          </NavLink>
        ))}
      </nav>
      <div className="min-w-0">{body}</div>
      {a && (
        <aside className="sticky top-20 hidden self-start xl:block">
          <Toc items={a.doc.toc} />
        </aside>
      )}
    </div>
  );
}
