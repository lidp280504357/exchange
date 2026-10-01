import { errorText } from "@exchange/core";
import { ErrorState, Spinner } from "@exchange/ui";
import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router";
import { useMe } from "./session";

const Login = lazy(() => import("./pages/Login"));
const SignedIn = lazy(() => import("./layout/SignedIn"));

export function App() {
  const me = useMe();
  if (me.isPending) {
    return (
      <div className="grid min-h-dvh place-items-center text-fg-3">
        <Spinner size={24} />
      </div>
    );
  }
  if (me.isError) return <ErrorState message={errorText(me.error)} onRetry={() => void me.refetch()} />;
  return (
    <Suspense fallback={null}>
      {me.data ? (
        <SignedIn admin={me.data} />
      ) : (
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="*" element={<Navigate to="/login" replace />} />
        </Routes>
      )}
    </Suspense>
  );
}
