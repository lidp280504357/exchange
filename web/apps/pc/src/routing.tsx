import { registerMessages, routes, selectRestoring, selectSignedIn, useSession } from "@exchange/core";
import { lazy, type ComponentType, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router";
import { PageSkeleton } from "./layout/PageSkeleton";

// Each area of the site lists its pages in pages/<area>/routes.tsx; App
// puts them under their shell. Pages are lazy (one chunk each), and so are
// their strings: lazyPage loads a page's chunk and its areas' messages
// together and registers the messages before the page renders.

type Messages = { default: { "zh-CN": Record<string, unknown>; en: Record<string, unknown> } };

/** lazyPage is React.lazy for a page whose strings live in apps/pc/src/i18n/<area>.ts. */
export function lazyPage<P extends object>(page: () => Promise<{ default: ComponentType<P> }>, ...messages: (() => Promise<Messages>)[]) {
  return lazy(async () => {
    const [mod, ...loaded] = await Promise.all([page(), ...messages.map((m) => m())]);
    for (const m of loaded) registerMessages(m.default);
    return mod;
  });
}

export type PageRoute = {
  /** The path (routes.* from @exchange/core); "/" is the index. */
  path: string;
  element: ReactNode;
  /** Needs a session: visitors go to the login page and come back after. */
  auth?: boolean;
};

/**
 * RequireAuth waits for the session to be restored at start-up, then
 * shows the page or sends the visitor to sign in (?next= brings them back).
 */
export function RequireAuth({ children }: { children: ReactNode }) {
  const signedIn = useSession(selectSignedIn);
  const restoring = useSession(selectRestoring);
  const location = useLocation();
  if (restoring) return <PageSkeleton />;
  if (!signedIn) {
    const next = encodeURIComponent(location.pathname + location.search);
    return <Navigate to={`${routes.login}?next=${next}`} replace />;
  }
  return children;
}

/** safeNext is the in-site path of a ?next= parameter, or the fallback. */
export function safeNext(next: string | null, fallback: string = routes.assets): string {
  return next && next.startsWith("/") && !next.startsWith("//") ? next : fallback;
}
