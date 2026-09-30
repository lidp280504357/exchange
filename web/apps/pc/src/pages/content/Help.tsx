import { errorText, routes } from "@exchange/core";
import { HELP_CATEGORIES, useArticles, type ArticleMeta } from "@exchange/core/content/index";
import { Button, EmptyState, ErrorState, Input, Skeleton, SkeletonLines, Tag, cn, listItem } from "@exchange/ui";
import { ArrowRight, BookOpen, CandlestickChart, CircleHelp, Layers, LifeBuoy, Search, ShieldCheck, Wallet } from "lucide-react";
import { motion } from "motion/react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useSearchParams } from "react-router";
import { usePageTitle } from "../markets/hooks";
import { groupByCategory, SimNotice } from "./parts";

// /help (design §6.2): a search over the article titles, the categories
// in a sidebar, and the articles as cards grouped by category.

const ICONS: Record<string, ReactNode> = {
  account: <ShieldCheck size={18} />,
  funds: <Wallet size={18} />,
  trading: <CandlestickChart size={18} />,
  futures: <Layers size={18} />,
  faq: <CircleHelp size={18} />,
};

/** matchTitle: every word of the query appears in the title (case-insensitive). */
function matchTitle(title: string, query: string): boolean {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  const t = title.toLowerCase();
  return words.every((w) => t.includes(w));
}

export default function Help() {
  const { t } = useTranslation();
  usePageTitle(t("pcContent.help.title"));
  const [params, setParams] = useSearchParams();
  const q = useArticles("help");
  const [query, setQuery] = useState(() => params.get("q") ?? "");
  const category = params.get("cat") ?? "";
  const list = useMemo(() => q.data ?? [], [q.data]);

  const setParam = (key: string, value: string) =>
    setParams(
      (p) => {
        const next = new URLSearchParams(p);
        if (value) next.set(key, value);
        else next.delete(key);
        return next;
      },
      { replace: true },
    );
  const changeQuery = (v: string) => {
    setQuery(v);
    setParam("q", v.trim());
  };

  const groups = useMemo(() => groupByCategory(list), [list]);
  const searching = query.trim() !== "";
  const matches = useMemo(
    () => list.filter((a) => (!category || a.category === category) && (!searching || matchTitle(a.title, query))),
    [list, category, searching, query],
  );
  const shown = searching || category ? [{ category: category || "", items: matches }] : groups;

  return (
    <div>
      <section className="relative overflow-hidden border-b border-line-1">
        <div
          aria-hidden
          className="pointer-events-none absolute inset-0 bg-[linear-gradient(to_right,var(--line-1)_1px,transparent_1px),linear-gradient(to_bottom,var(--line-1)_1px,transparent_1px)] bg-[size:40px_40px] opacity-40 mask-b-from-10%"
        />
        <div aria-hidden className="pointer-events-none absolute -top-40 left-1/2 size-[520px] -translate-x-1/2 animate-float rounded-full bg-brand-soft blur-3xl" />
        <div className="relative mx-auto flex max-w-[1440px] flex-col items-center gap-4 px-6 py-14 text-center">
          <span className="grid size-12 place-items-center rounded-3 bg-brand-soft text-brand">
            <LifeBuoy size={24} />
          </span>
          <h1 className="text-2xl font-semibold text-fg-1">{t("pcContent.help.title")}</h1>
          <p className="text-fg-2">{t("pcContent.help.subtitle")}</p>
          <Input
            size="lg"
            value={query}
            onValueChange={changeQuery}
            clearable
            onClear={() => changeQuery("")}
            placeholder={t("pcContent.help.search")}
            aria-label={t("pcContent.help.search")}
            prefix={<Search size={18} className="text-fg-3" />}
            containerClassName="mt-2 max-w-xl"
          />
        </div>
      </section>

      <div className="mx-auto max-w-[1440px] px-6 py-8">
        <SimNotice />
        <div className="mt-6 grid grid-cols-[210px_minmax(0,1fr)] gap-8 xl:grid-cols-[230px_minmax(0,1fr)]">
          <nav aria-label={t("pcContent.help.title")} className="sticky top-20 flex h-max flex-col gap-0.5 self-start">
            <CategoryButton active={!category} onClick={() => setParam("cat", "")} icon={<BookOpen size={18} />} label={t("pcContent.help.all")} count={q.data ? list.length : undefined} />
            {HELP_CATEGORIES.filter((c) => groups.some((g) => g.category === c)).map((c) => (
              <CategoryButton
                key={c}
                active={category === c}
                onClick={() => setParam("cat", c)}
                icon={ICONS[c]}
                label={t(`pcContent.categories.${c}`)}
                count={groups.find((g) => g.category === c)?.items.length}
              />
            ))}
          </nav>

          <div className="min-w-0">
            {q.isPending ? (
              <div className="grid grid-cols-2 gap-4">
                {[0, 1, 2, 3].map((i) => (
                  <div key={i} className="rounded-3 border border-line-1 bg-bg-1 p-5">
                    <Skeleton className="h-5 w-40" />
                    <SkeletonLines lines={2} className="mt-3" />
                  </div>
                ))}
              </div>
            ) : q.isError ? (
              <div className="rounded-3 border border-line-1 bg-bg-1">
                <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />
              </div>
            ) : matches.length === 0 ? (
              <div className="rounded-3 border border-line-1 bg-bg-1">
                <EmptyState
                  title={t("pcContent.help.noResults")}
                  description={t("pcContent.help.noResultsHint")}
                  action={
                    <Button
                      size="sm"
                      variant="secondary"
                      onClick={() => {
                        changeQuery("");
                        setParam("cat", "");
                      }}
                    >
                      {t("pcContent.help.clear")}
                    </Button>
                  }
                />
              </div>
            ) : (
              <div className="flex flex-col gap-10">
                {shown.map((g) => (
                  <section key={g.category || "all"}>
                    {g.category && (
                      <h2 className="mb-4 flex items-center gap-2 text-md font-semibold text-fg-1">
                        <span className="text-brand">{ICONS[g.category] ?? <BookOpen size={18} />}</span>
                        {t(`pcContent.categories.${g.category}`, { defaultValue: g.category })}
                      </h2>
                    )}
                    <ul className="grid grid-cols-2 gap-4">
                      {g.items.map((a, i) => (
                        <ArticleCard key={a.slug} article={a} index={i} showCategory={searching && !category} />
                      ))}
                    </ul>
                  </section>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function CategoryButton({ active, onClick, icon, label, count }: { active: boolean; onClick: () => void; icon: ReactNode; label: string; count?: number }) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "flex h-10 w-full items-center gap-2.5 rounded-2 px-3 text-left text-sm transition-colors duration-[var(--t-fast)]",
        active ? "bg-bg-2 font-medium text-fg-1" : "text-fg-2 hover:bg-bg-1 hover:text-fg-1",
      )}
    >
      <span className={active ? "text-brand" : "text-fg-3"}>{icon}</span>
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {count !== undefined && <span className="text-xs text-fg-3 tabular-nums">{count}</span>}
    </button>
  );
}

function ArticleCard({ article: a, index, showCategory }: { article: ArticleMeta; index: number; showCategory: boolean }) {
  const { t } = useTranslation();
  return (
    <motion.li variants={listItem} initial="initial" animate="animate" custom={index} className="min-w-0">
      <Link
        to={routes.helpArticle(a.slug)}
        className="group flex h-full flex-col rounded-3 border border-line-1 bg-bg-1 p-5 transition-[transform,border-color,box-shadow] duration-[var(--t-base)] hover:-translate-y-0.5 hover:border-line-2 hover:shadow-pop"
      >
        <div className="flex items-start justify-between gap-3">
          <h3 className="text-base font-semibold text-fg-1 transition-colors group-hover:text-brand">{a.title}</h3>
          {showCategory && <Tag>{t(`pcContent.categories.${a.category}`, { defaultValue: a.category })}</Tag>}
        </div>
        <p className="mt-2 line-clamp-2 text-sm leading-relaxed text-fg-3">{a.summary}</p>
        <span className="mt-auto flex justify-end pt-3 text-fg-3 transition-colors group-hover:text-brand">
          <ArrowRight size={16} className="transition-transform group-hover:translate-x-0.5" />
        </span>
      </Link>
    </motion.li>
  );
}
