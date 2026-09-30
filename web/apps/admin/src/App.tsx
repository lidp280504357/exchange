import { errorText } from "@exchange/core";
import { can, type Admin, type Permission } from "@exchange/core/api/admin";
import { ErrorState, Spinner } from "@exchange/ui";
import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router";
import { ConsoleShell } from "./layout/ConsoleShell";
import { useMe } from "./session";

const Login = lazy(() => import("./pages/Login"));
const Overview = lazy(() => import("./pages/Overview"));
const Soon = lazy(() => import("./pages/Soon"));

/** The console's sections (design §10.1); a section hides without its permission. */
export const sections: { path: string; key: string; perm: Permission; legacy: string }[] = [
  { path: "users", key: "users", perm: "users.read", legacy: "users" },
  { path: "orders", key: "orders", perm: "reports.read", legacy: "reports" },
  { path: "deposits", key: "deposits", perm: "withdrawals.read", legacy: "withdrawals" },
  { path: "withdrawals", key: "withdrawals", perm: "withdrawals.read", legacy: "withdrawals" },
  { path: "custody", key: "custody", perm: "withdrawals.read", legacy: "withdrawals" },
  { path: "instruments", key: "instruments", perm: "instruments.read", legacy: "instruments" },
  { path: "derivatives", key: "derivatives", perm: "derivatives.read", legacy: "derivatives" },
  { path: "risk", key: "risk", perm: "flags.read", legacy: "flags" },
  { path: "ledger", key: "ledger", perm: "audit.read", legacy: "ledger" },
  { path: "audit", key: "audit", perm: "audit.read", legacy: "audit" },
  { path: "reports", key: "reports", perm: "reports.read", legacy: "reports" },
];

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
      {me.data ? <SignedIn admin={me.data} /> : (
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="*" element={<Navigate to="/login" replace />} />
        </Routes>
      )}
    </Suspense>
  );
}

function SignedIn({ admin }: { admin: Admin }) {
  return (
    <Routes>
      <Route element={<ConsoleShell admin={admin} />}>
        <Route index element={<Overview />} />
        {sections
          .filter((s) => can(admin, s.perm))
          .map((s) => (
            <Route key={s.path} path={s.path} element={<Soon section={s.key} legacy={s.legacy} />} />
          ))}
        <Route path="/login" element={<Navigate to="/" replace />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
