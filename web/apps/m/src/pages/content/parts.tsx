import { siteURL } from "@exchange/core";
import { Markdown, type LinkProps, type MarkdownDoc, type TocItem } from "@exchange/core/content/index";
import { cn } from "@exchange/ui";
import { ChevronDown, ChevronRight, ExternalLink, FlaskConical } from "lucide-react";
import { useReducedMotion } from "motion/react";
import { useEffect, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

// Pieces of the mobile announcement and help pages: the Markdown
// typography for a phone (long words wrap, tables and code scroll inside
// their own box), the simulated-funds notice, and the table of contents
// that folds away above the article.

// The core renderer emits bare elements; the page styles them here.
const PROSE = cn(
  "min-w-0 break-words text-md leading-7 text-fg-2",
  "[&_h1]:mb-3 [&_h1]:mt-6 [&_h1]:text-lg [&_h1]:font-semibold [&_h1]:text-fg-1",
  "[&_h2]:mb-2 [&_h2]:mt-8 [&_h2]:scroll-mt-[calc(56px+env(safe-area-inset-top))] [&_h2]:border-b [&_h2]:border-line-1 [&_h2]:pb-2 [&_h2]:text-lg [&_h2]:font-semibold [&_h2]:leading-snug [&_h2]:text-fg-1",
  "[&_h3]:mb-2 [&_h3]:mt-6 [&_h3]:scroll-mt-[calc(56px+env(safe-area-inset-top))] [&_h3]:text-md [&_h3]:font-semibold [&_h3]:text-fg-1",
  "[&_h4]:mt-5 [&_h4]:font-semibold [&_h4]:text-fg-1",
  "[&_p]:my-3",
  "[&_ul]:my-3 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:my-3 [&_ol]:list-decimal [&_ol]:pl-5 [&_li]:my-1.5 [&_li]:pl-1 [&_li::marker]:text-fg-3",
  "[&_a]:text-brand [&_a]:underline [&_a]:underline-offset-4",
  "[&_strong]:font-semibold [&_strong]:text-fg-1 [&_em]:italic [&_del]:text-fg-3",
  "[&_code]:rounded-1 [&_code]:bg-bg-3 [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-sm [&_code]:text-fg-1",
  "[&_pre]:my-4 [&_pre]:overflow-x-auto [&_pre]:rounded-2 [&_pre]:border [&_pre]:border-line-1 [&_pre]:bg-bg-2 [&_pre]:p-3 [&_pre]:text-sm [&_pre]:leading-6 [&_pre_code]:bg-transparent [&_pre_code]:p-0",
  "[&_blockquote]:my-4 [&_blockquote]:rounded-r-2 [&_blockquote]:border-l-2 [&_blockquote]:border-brand [&_blockquote]:bg-bg-1 [&_blockquote]:px-3 [&_blockquote]:py-1 [&_blockquote_p]:my-2",
  "[&_hr]:my-6 [&_hr]:border-line-1",
  "[&_[data-md=table]]:my-4 [&_[data-md=table]]:overflow-x-auto [&_[data-md=table]]:rounded-2 [&_[data-md=table]]:border [&_[data-md=table]]:border-line-1",
  "[&_table]:w-full [&_table]:border-collapse [&_table]:text-sm [&_table]:leading-6",
  "[&_th]:whitespace-nowrap [&_th]:border-b [&_th]:border-line-1 [&_th]:bg-bg-2 [&_th]:px-3 [&_th]:py-2 [&_th]:text-left [&_th]:font-medium [&_th]:text-fg-1",
  "[&_td]:border-b [&_td]:border-line-1 [&_td]:px-3 [&_td]:py-2 [&_td]:tabular-nums [&_tr:last-child_td]:border-0",
);

// Paths served by the PC site's host only (the API reference, the old
// sites): on the mobile site they would fall back to its home page.
const PC_ONLY = /^\/(docs|storybook|admin)(\/|$)/;

function ContentLink({ href, title, external, children }: LinkProps) {
  const { t } = useTranslation();
  const pcOnly = !external && PC_ONLY.test(href);
  if (external || pcOnly) {
    const mail = href.startsWith("mailto:");
    return (
      <a href={pcOnly ? siteURL("pc") + href : href} title={title} target={mail ? undefined : "_blank"} rel="noopener noreferrer">
        {children}
        {!mail && (
          <>
            <ExternalLink aria-hidden size={12} className="ml-0.5 inline align-baseline" />
            <span className="sr-only">{t("mContent.article.external")}</span>
          </>
        )}
      </a>
    );
  }
  if (href.startsWith("/")) {
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

/** Prose renders an article's Markdown with the phone's typography and router links. */
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
    <div role="note" className={cn("flex items-start gap-2 rounded-2 border border-warn/30 bg-warn/10 px-3 py-2.5 text-xs leading-relaxed text-warn", className)}>
      <FlaskConical size={14} className="mt-0.5 shrink-0" />
      {t("mContent.simNotice")}
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

/** Toc is an article's sections in a fold above the text; a tap scrolls to one. */
export function Toc({ items, className }: { items: readonly TocItem[]; className?: string }) {
  const { t } = useTranslation();
  const reduced = useReducedMotion();
  if (items.length < 2) return null;
  return (
    <details className={cn("group rounded-3 border border-line-1 bg-bg-1", className)}>
      <summary className="flex h-12 cursor-pointer list-none items-center justify-between px-4 text-sm font-medium text-fg-1 [&::-webkit-details-marker]:hidden">
        {t("mContent.article.toc")}
        <ChevronDown size={16} className="text-fg-3 transition-transform duration-[var(--t-fast)] group-open:rotate-180" />
      </summary>
      <nav aria-label={t("mContent.article.toc")} className="border-t border-line-1 pb-1">
        <ul>
          {items.map((i) => (
            <li key={i.id}>
              <a
                href={`#${encodeURIComponent(i.id)}`}
                onClick={(e) => {
                  e.preventDefault();
                  scrollToHeading(i.id, !reduced);
                }}
                className={cn("flex min-h-11 items-center pr-4 text-sm leading-snug transition-colors active:text-brand", i.depth === 3 ? "pl-8 text-fg-3" : "pl-4 text-fg-2")}
              >
                {i.text}
              </a>
            </li>
          ))}
        </ul>
      </nav>
    </details>
  );
}

/** useHashScroll scrolls to the address's #anchor once the article is on the page. */
export function useHashScroll(ready: boolean): void {
  useEffect(() => {
    if (!ready || !window.location.hash) return;
    const id = decodeURIComponent(window.location.hash.slice(1));
    const frame = requestAnimationFrame(() => document.getElementById(id)?.scrollIntoView({ block: "start" }));
    return () => cancelAnimationFrame(frame);
  }, [ready]);
}

/** ListLink is a row of the content lists: a 44 px+ target with a title, a line under it and a chevron. */
export function ListLink({ to, title, meta, summary }: { to: string; title: ReactNode; meta?: ReactNode; summary?: ReactNode }) {
  return (
    <Link to={to} className="flex min-h-14 items-center gap-3 px-4 py-3 transition-colors active:bg-bg-2">
      <span className="min-w-0 flex-1">
        {meta && <span className="mb-1 flex min-w-0 items-center gap-1.5 text-xs text-fg-3">{meta}</span>}
        <span className="block text-base font-medium leading-snug text-fg-1">{title}</span>
        {summary && <span className="mt-1 line-clamp-2 text-sm leading-relaxed text-fg-3">{summary}</span>}
      </span>
      <ChevronRight aria-hidden size={16} className="shrink-0 text-fg-3" />
    </Link>
  );
}

