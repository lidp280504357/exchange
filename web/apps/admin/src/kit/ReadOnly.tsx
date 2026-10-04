import { can, type Admin, type Permission } from "@exchange/core/api/admin";
import { Lock } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * ReadOnly says why a page's controls are off for this administrator: the
 * permission they need, which the role lacks (ui-checklist A3). Pages
 * that hide their actions need none; pages that keep the controls visible
 * but disabled show it above them. Nothing without the need.
 */
export function ReadOnly({ admin, perm }: { admin: Admin; perm: Permission }) {
  const { t } = useTranslation();
  if (can(admin, perm)) return null;
  return (
    <p data-testid="read-only" className="flex items-center gap-2 rounded-2 border border-line-1 bg-bg-2 px-3 py-2 text-sm text-fg-2">
      <Lock size={14} className="shrink-0 text-fg-3" />
      <span>
        {t("admin.readOnly", { role: t(`admin.roles.${admin.role}`) })} <code className="font-mono text-xs text-fg-1">{perm}</code>
      </span>
    </p>
  );
}
