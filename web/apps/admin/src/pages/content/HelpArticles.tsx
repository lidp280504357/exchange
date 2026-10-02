import type { Admin } from "@exchange/core/api/admin";
import { ArticlesPage } from "./articles";

/** Help articles (design 2026-10-02 §4.5). */
export default function HelpArticles({ admin }: { admin: Admin }) {
  return <ArticlesPage admin={admin} section="HELP" />;
}
