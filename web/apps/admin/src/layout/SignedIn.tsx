import type { Admin } from "@exchange/core/api/admin";
import { lazy } from "react";
import { Navigate, Route, Routes } from "react-router";
import { allowed, sections, subpages } from "../sections";
import { ConsoleShell } from "./ConsoleShell";

// Loaded only for an administrator who must change a generated password:
// its forms and the authenticator's QR code stay out of the shell's chunk
// (C6, the overview's Lighthouse score).
const MustChange = lazy(() => import("../pages/system/MustChange"));

/**
 * SignedIn is the console behind the sign-in, loaded once signed in (the
 * sign-in page stays small); an administrator whose password was
 * generated for them changes it first (C5.5 ⑪).
 */
export default function SignedIn({ admin }: { admin: Admin }) {
  if (admin.must_change_password) return <MustChange admin={admin} />;
  return (
    <Routes>
      <Route element={<ConsoleShell admin={admin} />}>
        {[...sections, ...subpages]
          .filter((s) => allowed(admin, s))
          .map((s) => (
            <Route key={s.path} index={s.path === ""} path={s.path || undefined} element={<s.page admin={admin} />} />
          ))}
        <Route path="/login" element={<Navigate to="/" replace />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
