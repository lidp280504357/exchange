import { cn, listItem } from "@exchange/ui";
import { ChevronRight } from "lucide-react";
import { motion } from "motion/react";
import type { ReactNode } from "react";
import { Link, type To } from "react-router";
import { entrance } from "./logic";

/** Section is a titled block of an account page (a group of cards or rows). */
export function Section({
  title, extra, id, className, children,
}: {
  title?: ReactNode;
  extra?: ReactNode;
  id?: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section id={id} className={cn("flex scroll-mt-16 flex-col gap-2", className)}>
      {(title || extra) && (
        <div className="flex min-h-6 items-center justify-between gap-3 px-1">
          {title && <h2 className="text-sm font-medium text-fg-2">{title}</h2>}
          {extra}
        </div>
      )}
      {children}
    </section>
  );
}

/** Group is a card of rows divided by lines; `index` places it in the first-screen stagger. */
export function Group({ index, className, children }: { index: number; className?: string; children: ReactNode }) {
  return (
    <motion.div
      variants={listItem}
      initial={entrance(index)}
      animate="animate"
      custom={index}
      className={cn("flex flex-col divide-y divide-line-1 overflow-hidden rounded-3 bg-bg-1", className)}
    >
      {children}
    </motion.div>
  );
}

export type NavRowProps = {
  icon: ReactNode;
  label: ReactNode;
  /** A value or badge before the chevron (the unread count). */
  trailing?: ReactNode;
  to?: To;
  onClick?: () => void;
  tone?: "default" | "danger";
  /** Show the chevron (default: yes). */
  chevron?: boolean;
};

/**
 * NavRow is a large row of the "me" tab: an icon, a label, an optional
 * value and a chevron, 56 px tall; a press darkens it and nudges the
 * chevron 2 px right. A link with `to`,
 * a button with `onClick`.
 */
export function NavRow({ icon, label, trailing, to, onClick, tone = "default", chevron = true }: NavRowProps) {
  const className = "group flex min-h-14 w-full items-center gap-3 px-4 text-left transition-colors active:bg-bg-2";
  const body = (
    <>
      <span
        aria-hidden
        className={cn("grid size-9 shrink-0 place-items-center rounded-2", tone === "danger" ? "bg-danger/10 text-danger" : "bg-bg-2 text-fg-2")}
      >
        {icon}
      </span>
      <span className={cn("min-w-0 flex-1 truncate text-base", tone === "danger" ? "text-danger" : "text-fg-1")}>{label}</span>
      {trailing}
      {chevron && (
        <ChevronRight size={18} className="shrink-0 text-fg-3 transition-transform duration-[var(--t-fast)] group-active:translate-x-0.5" aria-hidden />
      )}
    </>
  );
  if (to !== undefined) {
    return (
      <Link to={to} className={className}>
        {body}
      </Link>
    );
  }
  return (
    <button type="button" onClick={onClick} className={className}>
      {body}
    </button>
  );
}
