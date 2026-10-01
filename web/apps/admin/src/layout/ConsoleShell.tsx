import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { Badge, Button, cn, Toaster } from "@exchange/ui";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { LogOut, PanelLeftClose, PanelLeftOpen } from "lucide-react";
import { Suspense, useState } from "react";
import { useTranslation } from "react-i18next";
import { NavLink, Outlet, useSearchParams } from "react-router";
import { sections } from "../App";
import { UserDrawer } from "../pages/users/UserDrawer";
import { GlobalSearch } from "./GlobalSearch";

/**
 * ConsoleShell (design §10.1): a collapsible sidebar with the sections the
 * administrator may use and the counts waiting for them (refreshed every
 * 15 seconds), a top bar with the environment, the search, the
 * administrator and sign-out, and the page. ?user=<id> on any page opens
 * that user's drawer.
 */
export function ConsoleShell({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem("admin.sidebar") === "collapsed");
  const toggle = () => {
    localStorage.setItem("admin.sidebar", collapsed ? "open" : "collapsed");
    setCollapsed(!collapsed);
  };
  const logout = async () => {
    await adminApi.POST("/admin/v1/logout");
    qc.setQueryData(["admin", "me"], null);
  };
  const counts = useCounts(admin);
  const userId = params.get("user");
  const closeUser = () =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("user");
        return next;
      },
      { replace: true },
    );
  return (
    <div className="flex min-h-dvh bg-bg-0 text-fg-1">
      <aside
        className={cn(
          "sticky top-0 flex h-dvh shrink-0 flex-col border-r border-line-1 bg-bg-1 transition-[width] duration-[var(--t-base)]",
          collapsed ? "w-16" : "w-56",
        )}
      >
        <div className="flex h-14 items-center gap-2 border-b border-line-1 px-4 font-semibold">
          <span className="grid size-7 shrink-0 place-items-center rounded-2 bg-brand text-brand-fg">A</span>
          {!collapsed && <span className="truncate">{t("admin.title")}</span>}
        </div>
        <nav className="flex-1 overflow-y-auto p-2 text-sm" aria-label={t("admin.title")}>
          {sections
            .filter((s) => can(admin, s.perm))
            .map((s) => (
              <NavLink
                key={s.key}
                to={`/${s.path}`}
                end={s.path === ""}
                title={collapsed ? t(`admin.nav.${s.key}`) : undefined}
                className={({ isActive }) =>
                  cn(
                    "relative flex h-9 items-center gap-2 rounded-2 px-3 transition-colors",
                    isActive ? "bg-brand-soft font-medium text-fg-1" : "text-fg-2 hover:bg-bg-2 hover:text-fg-1",
                  )
                }
              >
                <s.icon size={18} className="shrink-0" />
                {!collapsed && <span className="flex-1 truncate">{t(`admin.nav.${s.key}`)}</span>}
                {counts[s.key] ? (
                  <Badge tone="warn" variant="solid" className={cn(collapsed && "absolute right-1 top-1 scale-75")}>
                    {counts[s.key]}
                  </Badge>
                ) : null}
              </NavLink>
            ))}
        </nav>
        <button
          type="button"
          onClick={toggle}
          className="flex h-10 items-center justify-center border-t border-line-1 text-fg-3 hover:text-fg-1"
          aria-label="toggle sidebar"
        >
          {collapsed ? <PanelLeftOpen size={18} /> : <PanelLeftClose size={18} />}
        </button>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-[var(--z-sticky)] flex h-14 items-center gap-4 border-b border-line-1 bg-bg-1 px-6">
          <span className="shrink-0 rounded-1 bg-warn px-2 py-0.5 text-xs font-medium text-brand-fg">{t("admin.env")}</span>
          <GlobalSearch admin={admin} />
          <div className="ml-auto flex shrink-0 items-center gap-3 text-sm">
            <span className="text-fg-2">{admin.email}</span>
            <span className="rounded-1 bg-bg-2 px-2 py-0.5 text-xs text-fg-2">{t(`admin.roles.${admin.role}`)}</span>
            <Button size="sm" variant="ghost" icon={<LogOut size={14} />} onClick={() => void logout()}>
              {t("admin.logout")}
            </Button>
          </div>
        </header>
        <main className="min-w-0 flex-1 p-6">
          <Suspense fallback={null}>
            <Outlet />
          </Suspense>
        </main>
      </div>
      {userId && can(admin, "users.read") && <UserDrawer admin={admin} userId={userId} onClose={closeUser} />}
      <Toaster />
    </div>
  );
}

/** useCounts follows what waits for the administrator: withdrawals to review, approvals to decide. */
function useCounts(admin: Admin): Record<string, number> {
  const withdrawals = useQuery({
    queryKey: ["admin", "count", "withdrawals"],
    queryFn: async () =>
      adminData(await adminApi.GET("/admin/v1/withdrawals", { params: { query: { status: "PENDING_REVIEW", limit: 100 } } })).items.length,
    refetchInterval: 15_000,
    enabled: can(admin, "withdrawals.read"),
  });
  const approvals = useQuery({
    queryKey: ["admin", "count", "approvals"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/approvals", { params: { query: { status: "PENDING", limit: 100 } } })).items.length,
    refetchInterval: 15_000,
  });
  return { withdrawals: withdrawals.data ?? 0, ledger: approvals.data ?? 0 };
}
