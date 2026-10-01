import { routes } from "@exchange/core";
import { useArticles } from "@exchange/core/content/index";
import { Skeleton, listItem } from "@exchange/ui";
import { Megaphone } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { isNew } from "./logic";

const SLIDES = 3;
const ROTATE_MS = 5000;

/**
 * NewsStrip (design §7.3 ⑥): the newest announcements, one line each,
 * swiped sideways (native snap scrolling) and turning by themselves every
 * 5 s until a finger touches them; a red dot marks the last three days'.
 * Reduced motion turns nothing by itself.
 */
export function NewsStrip({ index }: { index: number }) {
  const { t } = useTranslation();
  const q = useArticles("announcements");
  const list = useMemo(() => (q.data ?? []).slice(0, SLIDES), [q.data]);
  const reduced = useReducedMotion();
  const scroller = useRef<HTMLDivElement>(null);
  const [at, setAt] = useState(0);
  const [held, setHeld] = useState(false);
  const [now] = useState(() => Date.now());

  useEffect(() => {
    if (reduced || held || list.length < 2) return;
    const id = setInterval(() => {
      const el = scroller.current;
      if (!el || el.clientWidth === 0 || document.visibilityState === "hidden") return;
      const next = (Math.round(el.scrollLeft / el.clientWidth) + 1) % list.length;
      el.scrollTo({ left: next * el.clientWidth, behavior: "smooth" });
    }, ROTATE_MS);
    return () => clearInterval(id);
  }, [reduced, held, list.length]);

  if (q.isPending) return <Skeleton className="h-12 w-full rounded-3" />;
  if (q.isError || list.length === 0) return null;
  const onScroll = () => {
    const el = scroller.current;
    if (el && el.clientWidth > 0) setAt(Math.round(el.scrollLeft / el.clientWidth));
  };

  return (
    <motion.section
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={index}
      aria-label={t("mAccount.me.news.title")}
      className="flex items-center gap-1 rounded-3 border border-line-1 bg-bg-1 pl-3"
    >
      <Megaphone size={16} className="shrink-0 text-brand" aria-hidden />
      <div
        ref={scroller}
        onScroll={onScroll}
        onPointerDown={() => setHeld(true)}
        className="relative flex min-w-0 flex-1 snap-x snap-mandatory overflow-x-auto overscroll-x-contain [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      >
        {list.map((a) => {
          const fresh = isNew(a.date, now);
          // The inset keeps the next slide's dot out of sight (slides are fractional pixels wide).
          // "New" is in the link's label, not in visually hidden text: that is absolutely
          // placed, and from a slide scrolled out of sight it would widen the page.
          return (
            <Link
              key={a.slug}
              to={routes.announcement(a.slug)}
              aria-label={fresh ? `${t("mAccount.me.news.new")} ${a.title}` : undefined}
              className="flex h-12 w-full shrink-0 snap-start items-center gap-2 pl-1 pr-2"
            >
              {fresh && <span aria-hidden className="size-1.5 shrink-0 rounded-full bg-danger" />}
              <span className="min-w-0 flex-1 truncate text-sm text-fg-1">{a.title}</span>
            </Link>
          );
        })}
      </div>
      {list.length > 1 && (
        <span aria-hidden className="shrink-0 pr-3 text-xs text-fg-3 tabular-nums">
          {Math.min(at, list.length - 1) + 1}/{list.length}
        </span>
      )}
    </motion.section>
  );
}
