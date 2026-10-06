import { DEFAULT_CONTRACT, DEFAULT_SYMBOL, routes, useAssetProfiles, usePrivateSync } from "@exchange/core";
import { useBrandingEffects } from "@exchange/core/platform/index";
import type { QueryClient } from "@tanstack/react-query";
import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router";
import { AuthShell } from "./layout/AuthShell";
import { HeaderProvider } from "./layout/header";
import { MobileShell } from "./layout/MobileShell";
import { PageShell } from "./layout/PageShell";
import { PageSkeleton } from "./layout/PageSkeleton";
import { accountRoutes } from "./pages/account/routes";
import { assetRoutes } from "./pages/assets/routes";
import { authRoutes } from "./pages/auth/routes";
import { contentRoutes } from "./pages/content/routes";
import { futuresRoutes } from "./pages/futures/routes";
import { homeRoutes } from "./pages/home/routes";
import { tradeRoutes } from "./pages/trade/routes";
import { RequireAuth, type PageRoute } from "./routing";

// The same paths as the PC site (design §4.1): shared links open the same
// page on either site. Each page is its own chunk; each area lists its
// pages in pages/<area>/routes.tsx with the shell it sits in.
const all = [...homeRoutes, ...tradeRoutes, ...futuresRoutes, ...assetRoutes, ...accountRoutes, ...contentRoutes, ...authRoutes];
// The toaster (with the animation library) loads after the first screen.
const Toaster = lazy(() => import("@exchange/ui/components/Toast").then((m) => ({ default: m.Toaster })));

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
    <HeaderProvider>
      <Suspense fallback={<PageSkeleton />}>
        <Routes>
          <Route element={<MobileShell />}>
            {all.filter((r) => r.shell === "tabs").map(route)}
            <Route path="/trade" element={<Navigate to={routes.trade(DEFAULT_SYMBOL)} replace />} />
            <Route path="/futures" element={<Navigate to={routes.futures(DEFAULT_CONTRACT)} replace />} />
          </Route>
          <Route element={<PageShell />}>
            {all.filter((r) => r.shell === "page").map(route)}
            <Route path="*" element={<Navigate to={routes.home} replace />} />
          </Route>
          <Route element={<AuthShell />}>{all.filter((r) => r.shell === "auth").map(route)}</Route>
        </Routes>
        <Suspense fallback={null}>
          <Toaster position="top" />
        </Suspense>
      </Suspense>
    </HeaderProvider>
  );
}
