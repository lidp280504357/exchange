import { DEFAULT_CONTRACT, DEFAULT_SYMBOL, routes, usePrivateSync } from "@exchange/core";
import type { QueryClient } from "@tanstack/react-query";
import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router";
import { MobileShell } from "./layout/MobileShell";

// The same paths as the PC site (design §4.1): shared links open the same
// page on either site. Each page is its own chunk.
const Home = lazy(() => import("./pages/Home"));
const Soon = lazy(() => import("./pages/Soon"));

const soon: { path: string; title: string; legacy: string }[] = [
  { path: routes.markets, title: "nav.markets", legacy: "/markets" },
  { path: routes.trade(), title: "nav.spot", legacy: "/trade/:symbol" },
  { path: routes.futures(), title: "nav.futures", legacy: "/futures/:symbol" },
  { path: routes.coin(), title: "nav.markets", legacy: "/markets" },
  { path: routes.assets, title: "nav.assets", legacy: "/" },
  { path: routes.deposit, title: "nav.deposit", legacy: "/deposit" },
  { path: routes.withdraw, title: "nav.withdraw", legacy: "/withdraw" },
  { path: routes.transfer, title: "nav.transfer", legacy: "/transfer" },
  { path: routes.history, title: "nav.history", legacy: "/" },
  { path: routes.me, title: "nav.me", legacy: "/account" },
  { path: routes.security, title: "nav.security", legacy: "/account" },
  { path: routes.settings, title: "nav.settings", legacy: "/profile" },
  { path: routes.sessions, title: "nav.sessions", legacy: "/account" },
  { path: routes.notifications, title: "nav.notifications", legacy: "/notifications" },
  { path: routes.announcements, title: "nav.announcements", legacy: "/" },
  { path: routes.help, title: "nav.help", legacy: "/" },
  { path: routes.login, title: "nav.login", legacy: "/login" },
  { path: routes.register, title: "nav.register", legacy: "/register" },
  { path: routes.reset, title: "nav.login", legacy: "/reset" },
];

export function App({ queryClient }: { queryClient: QueryClient }) {
  usePrivateSync(queryClient);
  return (
    <Suspense fallback={null}>
      <Routes>
        <Route element={<MobileShell />}>
          <Route index element={<Home />} />
          <Route path="/trade" element={<Navigate to={routes.trade(DEFAULT_SYMBOL)} replace />} />
          <Route path="/futures" element={<Navigate to={routes.futures(DEFAULT_CONTRACT)} replace />} />
          {soon.map((p) => (
            <Route key={p.path} path={p.path} element={<Soon title={p.title} legacy={p.legacy} />} />
          ))}
          <Route path="*" element={<Navigate to={routes.home} replace />} />
        </Route>
      </Routes>
    </Suspense>
  );
}
