import { cn, durations, ease, listItem } from "@exchange/ui";
import { motion } from "motion/react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

export type QuickItem = {
  key: string;
  icon: ReactNode;
  label: string;
  to?: string;
  onClick?: () => void;
};

/**
 * QuickGrid (design §7.3 ④): shortcuts as round icons with a caption, four
 * to a row; the cells fade in 30 ms apart and an icon dips under the
 * finger and springs back.
 */
export function QuickGrid({ items, index }: { items: readonly QuickItem[]; index: number }) {
  const { t } = useTranslation();
  return (
    <motion.nav
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={index}
      aria-label={t("mAccount.me.quick.title")}
      data-testid="me-quick"
      className={cn("grid rounded-3 border border-line-1 bg-bg-1 px-1 py-1.5", items.length % 4 === 0 ? "grid-cols-4" : "grid-cols-3")}
    >
      {items.map((it, i) => (
        <motion.div
          key={it.key}
          initial={{ opacity: 0, y: 6 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: durations.base, ease, delay: 0.08 + i * 0.03 }}
        >
          <Cell item={it} />
        </motion.div>
      ))}
    </motion.nav>
  );
}

function Cell({ item }: { item: QuickItem }) {
  const className = "group flex h-[76px] w-full flex-col items-center justify-center gap-1.5 rounded-2";
  const body = (
    <>
      <span className="grid size-10 place-items-center rounded-full bg-bg-2 text-fg-1 transition-transform duration-[var(--t-base)] ease-[cubic-bezier(0.34,1.56,0.64,1)] group-active:scale-[0.86]">
        {item.icon}
      </span>
      <span className="max-w-full truncate px-1 text-xs text-fg-2">{item.label}</span>
    </>
  );
  if (item.to !== undefined)
    return (
      <Link to={item.to} className={className}>
        {body}
      </Link>
    );
  return (
    <button type="button" onClick={item.onClick} className={className}>
      {body}
    </button>
  );
}
