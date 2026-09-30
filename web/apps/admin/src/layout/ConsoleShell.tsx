import { adminApi, can, type Admin } from "@exchange/core/api/admin";
import { Button, cn } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { LayoutDashboard, LogOut, PanelLeftClose, PanelLeftOpen } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { NavLink, Outlet } from "react-router";
import { sections } from "../App";

/**
 * ConsoleShell (design §10.1): a collapsible sidebar with the sections the
 * administrator may use, a top bar with the environment, the administrator
 * and sign-out, and the page.
 */
export function ConsoleShell({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem("admin.sidebar") === "collapsed");
  const toggle = () => {
    localStorage.setItem("admin.sidebar", collapsed ? "open" : "collapsed");
    setCollapsed(!collapsed);
  };
  const logout = async () => {
    await adminApi.POST("/admin/v1/logout");
    qc.setQueryData(["admin", "me"], null);
  };
  return (
    <div className="flex min-h-dvh bg-bg-0 text-fg-1">
      <aside className={cn("sticky top-0 flex h-dvh flex-col border-r border-line-1 bg-bg-1 transition-[width] duration-[var(--t-base)]", collapsed ? "w-16" : "w-56")}>
        <div className="flex h-14 items-center gap-2 border-b border-line-1 px-4 font-semibold">
          <span className="grid size-7 place-items-center rounded-2 bg-brand text-brand-fg">A</span>
          {!collapsed && <span className="truncate">{t("admin.title")}</span>}
        </div>
        <nav className="flex-1 overflow-y-auto p-2 text-sm">
          <Item to="/" end label={t("admin.nav.overview")} collapsed={collapsed} icon={<LayoutDashboard size={18} />} />
          {sections
            .filter((s) => can(admin, s.perm))
            .map((s) => (
              <Item key={s.path} to={`/${s.path}`} label={t(`admin.nav.${s.key}`)} collapsed={collapsed} />
            ))}
        </nav>
        <button type="button" onClick={toggle} className="flex h-10 items-center justify-center border-t border-line-1 text-fg-3 hover:text-fg-1" aria-label="toggle sidebar">
          {collapsed ? <PanelLeftOpen size={18} /> : <PanelLeftClose size={18} />}
        </button>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-[var(--z-sticky)] flex h-14 items-center gap-3 border-b border-line-1 bg-bg-1 px-6">
          <span className="rounded-1 bg-warn px-2 py-0.5 text-xs font-medium text-brand-fg">{t("admin.env")}</span>
          <div className="ml-auto flex items-center gap-3 text-sm">
            <span className="text-fg-2">{admin.email}</span>
            <span className="rounded-1 bg-bg-2 px-2 py-0.5 text-xs text-fg-2">{t(`admin.roles.${admin.role}`)}</span>
            <Button size="sm" variant="ghost" icon={<LogOut size={14} />} onClick={() => void logout()}>
              {t("admin.logout")}
            </Button>
          </div>
        </header>
        <main className="flex-1 p-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

function Item({ to, label, collapsed, icon, end }: { to: string; label: string; collapsed: boolean; icon?: React.ReactNode; end?: boolean }) {
  return (
    <NavLink
      to={to}
      end={end}
      title={collapsed ? label : undefined}
      className={({ isActive }) =>
        cn(
          "flex h-9 items-center gap-2 rounded-2 px-3 transition-colors",
          isActive ? "bg-brand-soft font-medium text-fg-1" : "text-fg-2 hover:bg-bg-2 hover:text-fg-1",
        )
      }
    >
      {icon ?? <span className="size-[18px] text-center text-xs leading-[18px]">{label.slice(0, 1)}</span>}
      {!collapsed && <span className="truncate">{label}</span>}
    </NavLink>
  );
}
