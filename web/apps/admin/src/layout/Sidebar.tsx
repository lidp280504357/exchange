import type { Admin } from "@exchange/core/api/admin";
import { cn } from "@exchange/ui";
import { ChevronDown, PanelLeftClose, PanelLeftOpen } from "lucide-react";
import { motion } from "motion/react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { NavLink, useLocation } from "react-router";
import { useTodo } from "../live";
import { prefetchPage, prefetchPages } from "../preload";
import { allowed, groups, sections, type GroupKey, type Section } from "../sections";
import { Mark } from "./Brand";
import { CountBadge } from "./CountBadge";

const CLOSED_KEY = "admin.sidebar.closed";

function loadClosed(): Set<GroupKey> {
  try {
    return new Set(JSON.parse(localStorage.getItem(CLOSED_KEY) ?? "[]") as GroupKey[]);
  } catch {
    return new Set();
  }
}

/**
 * Sidebar (design 2026-10-02 §3, §6): dark, the sections in their groups
 * (a group folds like an accordion and remembers it), the active one
 * marked in the brand colour, and the counts waiting on the review queues.
 * It slides in once when the console opens; collapsed it keeps the icons.
 * A section's page loads before it is opened: when its link is pointed
 * at or focused, and all of them while the browser is idle (A40).
 */
export function Sidebar({ admin, collapsed, onToggle }: { admin: Admin; collapsed: boolean; onToggle: () => void }) {
  const { t } = useTranslation();
  const { pathname } = useLocation();
  const todo = useTodo();
  const [closed, setClosed] = useState(loadClosed);
  const visible = sections.filter((s) => allowed(admin, s));
  const paths = visible.map((s) => s.path).join(" ");
  useEffect(() => prefetchPages(paths.split(" ")), [paths]);
  const counts: Record<string, number> = {
    withdrawals: todo?.withdrawals ?? 0, approvals: todo?.approvals ?? 0, identityRequests: todo?.identity_requests ?? 0,
    deposits: todo?.deposits ?? 0,
  };
  // A section is active on its page and the pages under it, unless another
  // section owns that page (/sim/control is not the overview at /sim).
  const isActive = (s: Section) => {
    if (s.path === "") return pathname === "/";
    if (pathname === `/${s.path}`) return true;
    return pathname.startsWith(`/${s.path}/`) && !sections.some((o) => o !== s && pathname.startsWith(`/${o.path}`) && o.path.startsWith(`${s.path}/`));
  };
  const toggleGroup = (g: GroupKey) => {
    const next = new Set(closed);
    if (next.has(g)) next.delete(g);
    else next.add(g);
    setClosed(next);
    localStorage.setItem(CLOSED_KEY, JSON.stringify([...next]));
  };
  return (
    <aside
      data-theme="dark"
      className={cn(
        "sticky top-0 z-[var(--z-sticky)] flex h-dvh shrink-0 flex-col bg-bg-1 text-fg-2 animate-sidebar-in transition-[width] duration-200 ease-out",
        collapsed ? "w-[68px]" : "w-60",
      )}
    >
      <div className="flex h-14 shrink-0 items-center gap-2.5 overflow-hidden px-[18px]">
        <Mark size={32} />
        <div className={cn("min-w-0 whitespace-nowrap transition-opacity duration-200", collapsed && "opacity-0")}>
          <div className="text-sm font-semibold tracking-wide text-fg-1">ASTRAS</div>
          <div className="text-xs text-fg-3">{t("admin.console")}</div>
        </div>
      </div>
      <nav className="flex-1 overflow-y-auto overflow-x-hidden px-2.5 pb-4" aria-label={t("admin.title")}>
        {groups.map((g) => {
          const items = visible.filter((s) => s.group === g);
          if (items.length === 0) return null;
          if (g === "overview" || g === "users") {
            return items.map((s) => <Item key={s.key} s={s} collapsed={collapsed} count={counts[s.key]} active={isActive(s)} />);
          }
          const open = !closed.has(g) || items.some(isActive);
          return (
            <div key={g} className="mt-4">
              {collapsed ? (
                <div className="mx-3 mb-2 h-px bg-line-1" />
              ) : (
                <button
                  type="button"
                  onClick={() => toggleGroup(g)}
                  aria-expanded={open}
                  className="flex h-7 w-full items-center gap-1 whitespace-nowrap px-3 text-xs font-medium uppercase tracking-wider text-fg-3 hover:text-fg-2"
                >
                  <span className="flex-1 text-left">{t(`admin.groups.${g}`)}</span>
                  <ChevronDown size={14} className={cn("transition-transform duration-200", !open && "-rotate-90")} />
                </button>
              )}
              {(open || collapsed) && (
                <div className="flex flex-col gap-0.5">
                  {items.map((s, i) => (
                    <Item key={s.key} s={s} collapsed={collapsed} count={counts[s.key]} active={isActive(s)} index={i} />
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </nav>
      <button
        type="button"
        onClick={onToggle}
        className="flex h-11 shrink-0 items-center gap-2 border-t border-line-1 px-[22px] text-fg-3 hover:text-fg-1"
        aria-label={t(collapsed ? "admin.shell.expand" : "admin.shell.collapse")}
        title={t(collapsed ? "admin.shell.expand" : "admin.shell.collapse")}
      >
        {collapsed ? <PanelLeftOpen size={18} /> : <PanelLeftClose size={18} />}
      </button>
    </aside>
  );
}

function Item({ s, collapsed, count, active, index = 0 }: { s: Section; collapsed: boolean; count?: number; active: boolean; index?: number }) {
  const { t } = useTranslation();
  const label = t(`admin.nav.${s.key}`);
  return (
    <NavLink
      to={`/${s.path}`}
      end={s.path === ""}
      title={collapsed ? label : undefined}
      onPointerEnter={() => prefetchPage(s.path)}
      onFocus={() => prefetchPage(s.path)}
      style={{ "--i": index } as React.CSSProperties}
      className={cn(
        "stagger relative flex h-9 items-center gap-3 rounded-2 px-3 text-sm transition-colors",
        active ? "bg-brand-soft font-medium text-fg-1" : "hover:bg-bg-2 hover:text-fg-1",
      )}
    >
      {active && <motion.span layoutId="nav-active" className="absolute inset-y-2 left-0 w-[3px] rounded-full bg-brand" transition={{ duration: 0.2 }} />}
      <s.icon size={18} className={cn("shrink-0", active && "text-brand")} />
      <span className={cn("min-w-0 flex-1 truncate whitespace-nowrap transition-opacity duration-200", collapsed && "opacity-0")}>{label}</span>
      {count ? <CountBadge value={count} className={cn(collapsed && "absolute right-1 top-0.5 scale-75")} /> : null}
    </NavLink>
  );
}
