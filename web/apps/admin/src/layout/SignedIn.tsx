import { can, type Admin } from "@exchange/core/api/admin";
import { Navigate, Route, Routes } from "react-router";
import { sections } from "../sections";
import { ConsoleShell } from "./ConsoleShell";

/** SignedIn is the console behind the sign-in, loaded once signed in (the sign-in page stays light). */
export default function SignedIn({ admin }: { admin: Admin }) {
  return (
    <Routes>
      <Route element={<ConsoleShell admin={admin} />}>
        {sections
          .filter((s) => can(admin, s.perm))
          .map((s) => (
            <Route key={s.key} index={s.path === ""} path={s.path || undefined} element={<s.page admin={admin} />} />
          ))}
        <Route path="/login" element={<Navigate to="/" replace />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
