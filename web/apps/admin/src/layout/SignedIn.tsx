import type { Admin } from "@exchange/core/api/admin";
import { prefersReducedMotion } from "@exchange/ui";
import { MotionConfig } from "motion/react";
import { lazy } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router";
import { PageSkeleton } from "../kit/PageSkeleton";
import { registerPageMessages } from "../pageMessages";
import { nextPath } from "../next";
import { allowed, sections, subpages, useFlagged } from "../sections";
import { ConsoleShell } from "./ConsoleShell";

// The pages' strings arrive with this chunk (A40: not in the sign-in's).
registerPageMessages();

// Loaded only for an administrator who must change a generated password:
// its forms and the authenticator's QR code stay out of the shell's chunk
// (C6, the overview's Lighthouse score).
const MustChange = lazy(() => import("../pages/system/MustChange"));

/**
 * SignedIn is the console behind the sign-in, loaded once signed in (the
 * sign-in page stays small); an administrator whose password was
 * generated for them changes it first (C5.5 ⑪). Motion follows the
 * device's reduced-motion setting: no movement, and no fades or staggered
 * entrances either (skipAnimations, read once at start-up).
 */
export default function SignedIn({ admin }: { admin: Admin }) {
  return (
    <MotionConfig reducedMotion="user" skipAnimations={prefersReducedMotion()}>
      {admin.must_change_password ? <MustChange admin={admin} /> : <Console admin={admin} />}
    </MotionConfig>
  );
}

// A section behind a flag (kit/flags.ts) has no route while its flag is
// off, so its address leads to the overview; while the flags load it
// shows the skeleton, not the overview, for an address opened directly.
function Console({ admin }: { admin: Admin }) {
  const flagged = useFlagged();
  return (
    <Routes>
      <Route element={<ConsoleShell admin={admin} />}>
        {[...sections, ...subpages]
          .filter((s) => allowed(admin, s) && flagged(s) !== false)
          .map((s) => (
            <Route
              key={s.path}
              index={s.path === ""}
              path={s.path || undefined}
              element={flagged(s) === undefined ? <PageSkeleton /> : <s.page admin={admin} />}
            />
          ))}
        <Route path="/login" element={<Signed />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}

/** Signed sends a signed-in administrator from the sign-in page to where it was to lead. */
function Signed() {
  const { search } = useLocation();
  return <Navigate to={nextPath(search)} replace />;
}
