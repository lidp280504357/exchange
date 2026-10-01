import { errorText, routes } from "@exchange/core";
import { useArticles } from "@exchange/core/content/index";
import { Button, ChartCredit, EmptyState, ErrorState, Input, Skeleton, SkeletonLines, Tag } from "@exchange/ui";
import { BookOpen, CandlestickChart, CircleHelp, Layers, Search, ShieldCheck, Wallet, X } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";
import { PillBar } from "../../components/PillBar";
import { usePageHeader } from "../../layout/header";
import { groupByCategory, helpCategories, matchArticle } from "./logic";
import { ListLink, SimNotice } from "./parts";

// /help on the phone (design §7.2): a search over the titles and
// summaries, category pills, and the articles as lists grouped by
// category (one flat list once a category or a search narrows it).
// Search and category live in the address (q, cat), as on the PC site.

/** The pill of every category (no cat parameter). */
const ALL = "all";

const ICONS: Record<string, ReactNode> = {
  account: <ShieldCheck size={14} />,
  funds: <Wallet size={14} />,
  trading: <CandlestickChart size={14} />,
  futures: <Layers size={14} />,
  faq: <CircleHelp size={14} />,
};

export default function Help() {
  const { t } = useTranslation();
  usePageHeader({ title: t("mContent.help.title"), back: routes.home }, [t]);
  const [params, setParams] = useSearchParams();
  const q = useArticles("help");
  const list = useMemo(() => q.data ?? [], [q.data]);
  const [query, setQuery] = useState(() => params.get("q") ?? "");
  const categories = useMemo(() => helpCategories(list), [list]);
  const wanted = params.get("cat") ?? "";
  const category = categories.includes(wanted) ? wanted : "";

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
  const clearAll = () => {
    setQuery("");
    setParams(new URLSearchParams(), { replace: true });
  };

  const searching = query.trim() !== "";
  const matches = useMemo(() => list.filter((a) => (!category || a.category === category) && matchArticle(a, query)), [list, category, query]);
  const groups = useMemo(() => (searching || category ? [{ category: "", items: matches }] : groupByCategory(matches)), [searching, category, matches]);
  const categoryName = (c: string) => t(`mContent.categories.${c}`, { defaultValue: c });

  let body: ReactNode;
  if (q.isPending)
    body = (
      <div className="flex flex-col gap-3">
        {[0, 1, 2].map((i) => (
          <div key={i} className="rounded-3 border border-line-1 bg-bg-1 p-4">
            <Skeleton className="h-4 w-40" />
            <SkeletonLines lines={2} className="mt-3" />
          </div>
        ))}
      </div>
    );
  else if (q.isError)
    body = (
      <div className="rounded-3 border border-line-1 bg-bg-1">
        <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />
      </div>
    );
  else if (matches.length === 0)
    body = (
      <div className="rounded-3 border border-line-1 bg-bg-1">
        <EmptyState
          title={t("mContent.help.noResults")}
          description={t("mContent.help.noResultsHint")}
          action={
            <Button size="lg" variant="secondary" onClick={clearAll}>
              {t("mContent.help.clear")}
            </Button>
          }
        />
      </div>
    );
  else
    body = (
      <div className="flex flex-col gap-5">
        {groups.map((g) => (
          <section key={g.category || "all"}>
            {g.category && (
              <h2 className="mb-2 flex items-center gap-2 px-1 text-sm font-semibold text-fg-2">
                <span className="text-brand">{ICONS[g.category] ?? <BookOpen size={14} />}</span>
                {categoryName(g.category)}
              </h2>
            )}
            <ul className="divide-y divide-line-1 overflow-hidden rounded-3 border border-line-1 bg-bg-1">
              {g.items.map((a) => (
                <li key={a.slug}>
                  <ListLink
                    to={routes.helpArticle(a.slug)}
                    title={a.title}
                    summary={a.summary}
                    meta={searching && !category ? <Tag>{categoryName(a.category)}</Tag> : undefined}
                  />
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    );

  return (
    <div className="flex flex-col pb-8">
      <div className="px-4 pt-3">
        <h1 className="sr-only">{t("mContent.help.title")}</h1>
        <p className="mb-3 text-sm text-fg-3">{t("mContent.help.subtitle")}</p>
        <Input
          size="lg"
          value={query}
          onValueChange={changeQuery}
          onKeyDown={(e) => {
            if (e.key === "Enter") e.currentTarget.blur();
          }}
          inputMode="search"
          enterKeyHint="search"
          autoComplete="off"
          spellCheck={false}
          placeholder={t("mContent.help.search")}
          aria-label={t("mContent.help.search")}
          prefix={<Search size={18} className="text-fg-3" />}
          suffix={
            query ? (
              <button type="button" aria-label={t("ui.clear")} onClick={() => changeQuery("")} className="grid size-11 place-items-center rounded-2 text-fg-3 active:text-fg-1">
                <X size={16} />
              </button>
            ) : undefined
          }
        />
      </div>
      {categories.length > 0 && (
        <PillBar
          aria-label={t("mContent.help.categories")}
          items={[{ value: ALL, label: t("mContent.help.all") }, ...categories.map((c) => ({ value: c, label: categoryName(c), icon: ICONS[c] }))]}
          value={category || ALL}
          onValueChange={(v) => setParam("cat", v === ALL ? "" : v)}
          className="mt-1 px-2"
        />
      )}
      <div className="mt-2 px-4">
        {body}
        <SimNotice className="mt-6" />
        <ChartCredit className="mt-4 text-center" />
      </div>
    </div>
  );
}
