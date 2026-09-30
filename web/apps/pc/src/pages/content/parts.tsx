import { routes } from "@exchange/core";
import { Markdown, type ArticleMeta, type LinkProps, type MarkdownDoc, type TocItem } from "@exchange/core/content/index";
import { cn } from "@exchange/ui";
import { ExternalLink, FlaskConical } from "lucide-react";
import { useReducedMotion } from "motion/react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, NavLink } from "react-router";

// Pieces of the announcement and help pages: the Markdown typography,
// the table of contents that follows the reading position, the
// simulated-funds notice every content page shows, and the help sidebar.

// The Markdown renderer emits bare elements; the page styles them here.
const PROSE = cn(
  "min-w-0 text-base leading-7 text-fg-2",
  "[&_h1]:mb-4 [&_h1]:mt-8 [&_h1]:text-xl [&_h1]:font-semibold [&_h1]:text-fg-1",
  "[&_h2]:mb-3 [&_h2]:mt-10 [&_h2]:scroll-mt-20 [&_h2]:border-b [&_h2]:border-line-1 [&_h2]:pb-2 [&_h2]:text-lg [&_h2]:font-semibold [&_h2]:text-fg-1",
  "[&_h3]:mb-2 [&_h3]:mt-7 [&_h3]:scroll-mt-20 [&_h3]:text-md [&_h3]:font-semibold [&_h3]:text-fg-1",
  "[&_h4]:mt-5 [&_h4]:font-semibold [&_h4]:text-fg-1",
  "[&_p]:my-3",
  "[&_ul]:my-3 [&_ul]:list-disc [&_ul]:pl-6 [&_ol]:my-3 [&_ol]:list-decimal [&_ol]:pl-6 [&_li]:my-1.5 [&_li]:pl-1 [&_li::marker]:text-fg-3",
  "[&_a]:text-brand [&_a]:underline-offset-4 [&_a:hover]:underline",
  "[&_strong]:font-semibold [&_strong]:text-fg-1 [&_em]:italic [&_del]:text-fg-3",
  "[&_code]:rounded-1 [&_code]:bg-bg-3 [&_code]:px-1.5 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-sm [&_code]:text-fg-1",
  "[&_pre]:my-4 [&_pre]:overflow-x-auto [&_pre]:rounded-2 [&_pre]:border [&_pre]:border-line-1 [&_pre]:bg-bg-2 [&_pre]:p-4 [&_pre_code]:bg-transparent [&_pre_code]:p-0",
  "[&_blockquote]:my-4 [&_blockquote]:rounded-r-2 [&_blockquote]:border-l-2 [&_blockquote]:border-brand [&_blockquote]:bg-bg-2 [&_blockquote]:px-4 [&_blockquote]:py-1 [&_blockquote_p]:my-2",
  "[&_hr]:my-8 [&_hr]:border-line-1",
  "[&_[data-md=table]]:my-4 [&_[data-md=table]]:overflow-x-auto [&_[data-md=table]]:rounded-2 [&_[data-md=table]]:border [&_[data-md=table]]:border-line-1",
  "[&_table]:w-full [&_table]:border-collapse [&_table]:text-sm",
  "[&_th]:whitespace-nowrap [&_th]:border-b [&_th]:border-line-1 [&_th]:bg-bg-2 [&_th]:px-3 [&_th]:py-2 [&_th]:text-left [&_th]:font-medium [&_th]:text-fg-1",
  "[&_td]:border-b [&_td]:border-line-1 [&_td]:px-3 [&_td]:py-2 [&_td]:tabular-nums [&_tr:last-child_td]:border-0",
);

// Paths nginx serves outside this app (a router link would show not found).
const OUTSIDE_APP = /^\/(docs|storybook|admin)(\/|$)/;

function ContentLink({ href, title, external, children }: LinkProps) {
  const { t } = useTranslation();
  if (external) {
    const mail = href.startsWith("mailto:");
    return (
      <a href={href} title={title} target={mail ? undefined : "_blank"} rel="noopener noreferrer">
        {children}
        {!mail && (
          <>
            <ExternalLink aria-hidden size={12} className="ml-0.5 inline align-baseline" />
            <span className="sr-only">{t("pcContent.article.external")}</span>
          </>
        )}
      </a>
    );
  }
  if (href.startsWith("/") && !OUTSIDE_APP.test(href)) {
    return (
      <Link to={href} title={title}>
        {children}
      </Link>
    );
  }
  return (
    <a href={href} title={title}>
      {children}
    </a>
  );
}

// The renderer calls this for each link; ContentLink renders as a component (it uses hooks).
const renderLink = (p: LinkProps) => <ContentLink {...p} />;

/** Prose renders an article's Markdown with the site's typography and router links. */
export function Prose({ doc, className }: { doc: MarkdownDoc; className?: string }) {
  return (
    <div className={cn(PROSE, className)}>
      <Markdown doc={doc} link={renderLink} />
    </div>
  );
}

/** SimNotice: every content page says the funds are simulated. */
export function SimNotice({ className }: { className?: string }) {
  const { t } = useTranslation();
  return (
    <div role="note" className={cn("flex items-center gap-2 rounded-2 border border-warn/30 bg-warn/10 px-4 py-2.5 text-sm text-warn", className)}>
      <FlaskConical size={16} className="shrink-0" />
      {t("pcContent.simNotice")}
    </div>
  );
}

/** scrollToHeading brings a heading under the top bar and puts its anchor in the address. */
function scrollToHeading(id: string, smooth: boolean) {
  const el = document.getElementById(id);
  if (!el) return;
  el.scrollIntoView({ behavior: smooth ? "smooth" : "auto", block: "start" });
  window.history.replaceState(window.history.state, "", `#${encodeURIComponent(id)}`);
}

/**
 * Toc lists an article's sections and marks the one being read (one
 * IntersectionObserver over the headings).
 */
export function Toc({ items, className }: { items: readonly TocItem[]; className?: string }) {
  const { t } = useTranslation();
  const reduced = useReducedMotion();
  const [active, setActive] = useState<string | undefined>(items[0]?.id);
  useEffect(() => {
    setActive(items[0]?.id);
    if (typeof IntersectionObserver === "undefined") return;
    const els = items.map((i) => document.getElementById(i.id)).filter((el): el is HTMLElement => el !== null);
    if (els.length === 0) return;
    const visible = new Set<string>();
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) visible.add(e.target.id);
          else visible.delete(e.target.id);
        }
        const first = items.find((i) => visible.has(i.id));
        if (first) setActive(first.id);
      },
      { rootMargin: "-72px 0px -60% 0px" },
    );
    els.forEach((el) => io.observe(el));
    return () => io.disconnect();
  }, [items]);
  if (items.length === 0) return null;
  return (
    <nav aria-label={t("pcContent.article.toc")} className={cn("text-sm", className)}>
      <div className="mb-3 text-xs font-medium uppercase tracking-wide text-fg-3">{t("pcContent.article.toc")}</div>
      <ul className="flex flex-col border-l border-line-1">
        {items.map((i) => (
          <li key={i.id}>
            <a
              href={`#${encodeURIComponent(i.id)}`}
              onClick={(e) => {
                e.preventDefault();
                setActive(i.id);
                scrollToHeading(i.id, !reduced);
              }}
              aria-current={active === i.id ? "location" : undefined}
              className={cn(
                "-ml-px block border-l py-1.5 leading-snug transition-colors duration-[var(--t-fast)]",
                i.depth === 3 ? "pl-7 text-xs" : "pl-4",
                active === i.id ? "border-brand text-brand" : "border-transparent text-fg-3 hover:text-fg-1",
              )}
            >
              {i.text}
            </a>
          </li>
        ))}
      </ul>
    </nav>
  );
}

/** useHashScroll scrolls to the address's #anchor once the article is on the page. */
export function useHashScroll(ready: boolean): void {
  useEffect(() => {
    if (!ready || !window.location.hash) return;
    const id = decodeURIComponent(window.location.hash.slice(1));
    const t = requestAnimationFrame(() => document.getElementById(id)?.scrollIntoView({ block: "start" }));
    return () => cancelAnimationFrame(t);
  }, [ready]);
}

/** Breadcrumb: the section's list, then the page. */
export function Breadcrumb({ to, label, current }: { to: string; label: string; current?: string }) {
  const { t } = useTranslation();
  return (
    <nav aria-label={t("pcContent.breadcrumb")} className="flex min-w-0 items-center gap-1.5 text-sm text-fg-3">
      <Link to={to} className="shrink-0 transition-colors hover:text-fg-1">
        {label}
      </Link>
      {current && (
        <>
          <span aria-hidden>/</span>
          <span className="truncate text-fg-2">{current}</span>
        </>
      )}
    </nav>
  );
}

/** groupByCategory groups sorted help articles, keeping their order. */
export function groupByCategory<T extends ArticleMeta>(list: readonly T[]): { category: string; items: T[] }[] {
  const groups: { category: string; items: T[] }[] = [];
  for (const a of list) {
    const g = groups.find((x) => x.category === a.category);
    if (g) g.items.push(a);
    else groups.push({ category: a.category, items: [a] });
  }
  return groups;
}

/** HelpNav: the help centre's categories and their articles, the open one marked. */
export function HelpNav({ articles, current }: { articles: readonly ArticleMeta[]; current?: string }) {
  const { t } = useTranslation();
  return (
    <nav aria-label={t("pcContent.help.title")} className="flex flex-col gap-5 text-sm">
      {groupByCategory(articles).map((g) => (
        <div key={g.category}>
          <Link
            to={`${routes.help}?cat=${g.category}`}
            className="mb-1.5 block px-3 text-xs font-medium uppercase tracking-wide text-fg-3 transition-colors hover:text-fg-1"
          >
            {t(`pcContent.categories.${g.category}`, { defaultValue: g.category })}
          </Link>
          <ul className="flex flex-col gap-0.5">
            {g.items.map((a) => (
              <li key={a.slug}>
                <NavLink
                  to={routes.helpArticle(a.slug)}
                  aria-current={a.slug === current ? "page" : undefined}
                  className={cn(
                    "block truncate rounded-2 px-3 py-1.5 transition-colors duration-[var(--t-fast)]",
                    a.slug === current ? "bg-bg-2 font-medium text-fg-1" : "text-fg-2 hover:bg-bg-1 hover:text-fg-1",
                  )}
                >
                  {a.title}
                </NavLink>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );
}

/** Section header of the content pages: an icon, the title and a line under it. */
export function PageHeader({ icon, title, subtitle, extra }: { icon: ReactNode; title: string; subtitle: string; extra?: ReactNode }) {
  return (
    <header className="flex flex-wrap items-center gap-4">
      <span className="grid size-12 shrink-0 place-items-center rounded-3 bg-brand-soft text-brand">{icon}</span>
      <div className="min-w-0">
        <h1 className="text-xl font-semibold text-fg-1">{title}</h1>
        <p className="mt-1 text-sm text-fg-3">{subtitle}</p>
      </div>
      {extra && <div className="ml-auto">{extra}</div>}
    </header>
  );
}
