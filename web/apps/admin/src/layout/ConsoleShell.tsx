import type { Admin } from "@exchange/core/api/admin";
import { Suspense, useState } from "react";
import { Navigate, Outlet, useLocation, useSearchParams } from "react-router";
import { useTodoStream } from "../live";
import { Offline } from "./Offline";
import { Sidebar } from "./Sidebar";
import { Toasts } from "./Toasts";
import { Topbar } from "./Topbar";

/**
 * ConsoleShell (design 2026-10-02 §3, §6): the dark sidebar with its
 * groups and the counts waiting (pushed by the event stream), the top bar,
 * the strip that says the network is gone, and the page, which rises in on every change of section. The address
 * ?user=<id> of the former user drawer leads to the user's page.
 */
export function ConsoleShell({ admin }: { admin: Admin }) {
  const { pathname } = useLocation();
  const [params] = useSearchParams();
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem("admin.sidebar") === "collapsed");
  useTodoStream();
  const toggle = () => {
    localStorage.setItem("admin.sidebar", collapsed ? "open" : "collapsed");
    setCollapsed(!collapsed);
  };
  const user = params.get("user");
  if (user) return <Navigate to={`/users/${encodeURIComponent(user)}`} replace />;
  return (
    <div className="flex min-h-dvh bg-bg-0 text-fg-1">
      <Sidebar admin={admin} collapsed={collapsed} onToggle={toggle} />
      <div className="flex min-w-0 flex-1 flex-col">
        <Topbar admin={admin} />
        <Offline />
        <main className="min-w-0 flex-1 p-6">
          <div key={pathname} className="animate-rise">
            <Suspense fallback={null}>
              <Outlet />
            </Suspense>
          </div>
        </main>
      </div>
      <Toasts />
    </div>
  );
}
