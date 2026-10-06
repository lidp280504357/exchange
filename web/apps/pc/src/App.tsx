import { DEFAULT_CONTRACT, DEFAULT_SYMBOL, routes, useAssetProfiles, usePrivateSync } from "@exchange/core";
import { useBrandingEffects } from "@exchange/core/platform/index";
import type { QueryClient } from "@tanstack/react-query";
import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router";
import { AppShell } from "./layout/AppShell";
import { AuthShell } from "./layout/AuthShell";
import { PageSkeleton } from "./layout/PageSkeleton";
import { TerminalShell } from "./layout/TerminalShell";
import { accountRoutes } from "./pages/account/routes";
import { assetRoutes } from "./pages/assets/routes";
import { authRoutes } from "./pages/auth/routes";
import { contentRoutes } from "./pages/content/routes";
import { futuresRoutes } from "./pages/futures/routes";
import { marketRoutes } from "./pages/markets/routes";
import { tradeRoutes } from "./pages/trade/routes";
import { RequireAuth, type PageRoute } from "./routing";

// Every page is its own chunk (design §4.4): the first screen carries no
// chart library, and moving between pages keeps the WebSocket open. The
// areas list their pages in pages/<area>/routes.tsx.
const NotFound = lazy(() => import("./pages/NotFound"));
// The toaster (and the animation library it uses) loads after the first
// screen; a toast raised before it arrives waits in the store.
const Toaster = lazy(() => import("@exchange/ui/components/Toast").then((m) => ({ default: m.Toaster })));

const shellRoutes = [...marketRoutes, ...futuresRoutes, ...assetRoutes, ...accountRoutes, ...contentRoutes];

function route(r: PageRoute) {
  const element = r.auth ? <RequireAuth>{r.element}</RequireAuth> : r.element;
  return r.path === routes.home ? <Route key="index" index element={element} /> : <Route key={r.path} path={r.path} element={element} />;
}

export function App({ queryClient }: { queryClient: QueryClient }) {
  usePrivateSync(queryClient);
  useAssetProfiles();
  // The platform profile's name, icons and colours (design 2026-10-04 §4.1).
  useBrandingEffects();
  return (
    <Suspense fallback={<PageSkeleton />}>
      <Routes>
        <Route element={<AppShell />}>
          {shellRoutes.map(route)}
          <Route path="/trade" element={<Navigate to={routes.trade(DEFAULT_SYMBOL)} replace />} />
          <Route path="/futures" element={<Navigate to={routes.futures(DEFAULT_CONTRACT)} replace />} />
          {/* The mobile site's "me" tab: a switch or device redirect from it lands on the account pages. */}
          <Route path={routes.me} element={<Navigate to={routes.security} replace />} />
          <Route path="*" element={<NotFound />} />
        </Route>
        <Route element={<TerminalShell />}>{tradeRoutes.map(route)}</Route>
        <Route element={<AuthShell />}>{authRoutes.map(route)}</Route>
      </Routes>
      <Suspense fallback={null}>
        <Toaster />
      </Suspense>
    </Suspense>
  );
}
