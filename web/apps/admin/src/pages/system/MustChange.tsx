import { adminApi, type Admin } from "@exchange/core/api/admin";
import { Button } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { LogOut } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Wordmark } from "../../layout/Brand";
import { PasswordForm } from "./ownCredentials";

/**
 * MustChange holds an administrator whose password was generated for them
 * (exchangectl admin create) on changing it: the server answers nothing
 * else until then (ADMIN_PASSWORD_CHANGE_REQUIRED, C5.5 ⑪).
 */
export default function MustChange({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const logout = async () => {
    await adminApi.POST("/admin/v1/logout");
    qc.setQueryData(["admin", "me"], null);
  };
  return (
    <div className="grid min-h-dvh place-items-center bg-bg-0 p-6">
      <div className="card w-full max-w-md p-8" data-testid="must-change">
        <Wordmark className="mb-6 h-8" />
        <div className="mb-1 text-lg font-semibold">{t("admin.account.mustTitle")}</div>
        <p className="mb-6 text-sm text-fg-3">{t("admin.account.mustText", { email: admin.email })}</p>
        <PasswordForm />
        <Button variant="ghost" size="sm" icon={<LogOut size={14} />} className="mt-6" onClick={() => void logout()}>
          {t("admin.logout")}
        </Button>
      </div>
    </div>
  );
}
