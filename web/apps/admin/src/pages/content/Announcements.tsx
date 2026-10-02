import type { Admin } from "@exchange/core/api/admin";
import { ArticlesPage } from "./articles";

/** Announcements (design 2026-10-02 §4.5). */
export default function Announcements({ admin }: { admin: Admin }) {
  return <ArticlesPage admin={admin} section="ANNOUNCEMENT" />;
}
