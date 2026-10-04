import { errorText } from "@exchange/core";
import { ErrorState, Spinner } from "@exchange/ui";
import { lazy, Suspense } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router";
import Login from "./pages/Login";
import { useMe } from "./session";

// The sign-in page is in the entry's chunk (A40: one file for a visitor,
// and what it shares with the pages stays out of their "kit"); the setup
// page and the signed-in console load when needed.
const Setup = lazy(() => import("./pages/Setup"));
const SignedIn = lazy(() => import("./layout/SignedIn"));

export function App() {
  const { pathname } = useLocation();
  // A one-time setup link opens without a session, whoever is signed in
  // in this browser (C5.5 ⑪).
  if (pathname === "/setup") {
    return (
      <Suspense fallback={<Loading />}>
        <Setup />
      </Suspense>
    );
  }
  return <Console />;
}

/** ToLogin sends a visitor to sign in, keeping where they were going (?next=) for after. */
function ToLogin() {
  const { pathname, search } = useLocation();
  const next = pathname === "/" && !search ? "" : `?next=${encodeURIComponent(pathname + search)}`;
  return <Navigate to={`/login${next}`} replace />;
}

/** Loading fills the window while the session is asked for and the signed-in console's chunks arrive. */
function Loading() {
  return (
    <div className="grid min-h-dvh place-items-center text-fg-3">
      <Spinner size={24} />
    </div>
  );
}

function Console() {
  const me = useMe();
  if (me.isPending) return <Loading />;
  if (me.isError) return <ErrorState message={errorText(me.error)} onRetry={() => void me.refetch()} />;
  return (
    <Suspense fallback={<Loading />}>
      {me.data ? (
        <SignedIn admin={me.data} />
      ) : (
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="*" element={<ToLogin />} />
        </Routes>
      )}
    </Suspense>
  );
}
