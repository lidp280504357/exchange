import type { Admin } from "@exchange/core/api/admin";
import { Navigate, Route, Routes } from "react-router";
import MustChange from "../pages/system/MustChange";
import { allowed, sections, subpages } from "../sections";
import { ConsoleShell } from "./ConsoleShell";

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
