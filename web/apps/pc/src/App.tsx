import { DEFAULT_CONTRACT, DEFAULT_SYMBOL, routes, usePrivateSync } from "@exchange/core";
import type { QueryClient } from "@tanstack/react-query";
import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router";
import { AppShell } from "./layout/AppShell";
import { AuthShell } from "./layout/AuthShell";
import { PageSkeleton } from "./layout/PageSkeleton";

// Every page is its own chunk (design §4.4): the first screen carries no
// chart library, and moving between pages keeps the WebSocket open.
const Home = lazy(() => import("./pages/Home"));
const Soon = lazy(() => import("./pages/Soon"));
const NotFound = lazy(() => import("./pages/NotFound"));

/** The pages still to come in B2 show where they will be and link the previous site. */
const soon: { path: string; title: string; legacy: string }[] = [
  { path: routes.markets, title: "nav.markets", legacy: "/markets" },
  { path: routes.trade(), title: "nav.spot", legacy: "/trade/:symbol" },
  { path: routes.futures(), title: "nav.futures", legacy: "/futures/:symbol" },
  { path: routes.coin(), title: "nav.markets", legacy: "/markets" },
  { path: routes.assets, title: "nav.overview", legacy: "/" },
  { path: routes.deposit, title: "nav.deposit", legacy: "/deposit" },
  { path: routes.withdraw, title: "nav.withdraw", legacy: "/withdraw" },
  { path: routes.transfer, title: "nav.transfer", legacy: "/transfer" },
  { path: routes.history, title: "nav.history", legacy: "/" },
  { path: routes.security, title: "nav.security", legacy: "/account" },
  { path: routes.settings, title: "nav.settings", legacy: "/profile" },
  { path: routes.sessions, title: "nav.sessions", legacy: "/account" },
  { path: routes.notifications, title: "nav.notifications", legacy: "/notifications" },
  { path: routes.announcements, title: "nav.announcements", legacy: "/" },
  { path: routes.help, title: "nav.help", legacy: "/" },
];

export function App({ queryClient }: { queryClient: QueryClient }) {
  usePrivateSync(queryClient);
  return (
    <Suspense fallback={<PageSkeleton />}>
      <Routes>
        <Route element={<AppShell />}>
          <Route index element={<Home />} />
          <Route path="/trade" element={<Navigate to={routes.trade(DEFAULT_SYMBOL)} replace />} />
          <Route path="/futures" element={<Navigate to={routes.futures(DEFAULT_CONTRACT)} replace />} />
          {soon.map((p) => (
            <Route key={p.path} path={p.path} element={<Soon title={p.title} legacy={p.legacy} />} />
          ))}
          <Route path="*" element={<NotFound />} />
        </Route>
        <Route element={<AuthShell />}>
          <Route path={routes.login} element={<Soon title="nav.login" legacy="/login" bare />} />
          <Route path={routes.register} element={<Soon title="nav.register" legacy="/register" bare />} />
          <Route path={routes.reset} element={<Soon title="nav.login" legacy="/reset" bare />} />
        </Route>
      </Routes>
    </Suspense>
  );
}
