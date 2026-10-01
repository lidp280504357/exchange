import { can, type Admin } from "@exchange/core/api/admin";
import { lazy, Suspense, useState } from "react";
import { Outlet, useLocation, useSearchParams } from "react-router";
import { useTodoStream } from "../live";
import { Sidebar } from "./Sidebar";
import { Toasts } from "./Toasts";
import { Topbar } from "./Topbar";

const UserDrawer = lazy(() => import("../pages/users/UserDrawer"));

/**
 * ConsoleShell (design 2026-10-02 §3, §6): the dark sidebar with its
 * groups and the counts waiting (pushed by the event stream), the top bar,
 * and the page, which rises in on every change of section. ?user=<id> on
 * any page opens that user's drawer.
 */
export function ConsoleShell({ admin }: { admin: Admin }) {
  const { pathname } = useLocation();
  const [params, setParams] = useSearchParams();
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem("admin.sidebar") === "collapsed");
  useTodoStream();
  const toggle = () => {
    localStorage.setItem("admin.sidebar", collapsed ? "open" : "collapsed");
    setCollapsed(!collapsed);
  };
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
      <Sidebar admin={admin} collapsed={collapsed} onToggle={toggle} />
      <div className="flex min-w-0 flex-1 flex-col">
        <Topbar admin={admin} />
        <main className="min-w-0 flex-1 p-6">
          <div key={pathname} className="animate-rise">
            <Suspense fallback={null}>
              <Outlet />
            </Suspense>
          </div>
        </main>
      </div>
      {userId && can(admin, "users.read") && (
        <Suspense fallback={null}>
          <UserDrawer admin={admin} userId={userId} onClose={closeUser} />
        </Suspense>
      )}
      <Toasts />
    </div>
  );
}
