import type { Admin } from "@exchange/core/api/admin";
import { Badge } from "@exchange/ui";
import { useTranslation } from "react-i18next";
import { stagger } from "../../kit/motion";
import { Card, Page } from "../../kit/Page";
import { PasswordForm, RemoveTotp, TotpForm } from "./ownCredentials";

/**
 * Account (C5.5 ⑪): the signed-in administrator's own password and
 * authenticator. Nobody else sets them: an ADMIN's reset only hands over
 * a one-time link whose holder sets them. Whether the authenticator is
 * bound, and removing it while sign-in does not ask for its code (N1).
 */
export default function Account({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  return (
    <Page title={t("admin.nav.account")} help={t("admin.account.help")}>
      <Card className="stagger">
        <div className="flex flex-wrap gap-x-8 gap-y-2 text-sm">
          <span>
            <span className="text-fg-3">{t("admin.admins.email")} </span>
            <span className="font-medium">{admin.email}</span>
          </span>
          <span>
            <span className="text-fg-3">{t("admin.admins.name")} </span>
            {admin.name || "—"}
          </span>
          <span>
            <span className="text-fg-3">{t("admin.admins.role")} </span>
            <span title={admin.role}>{t(`admin.roles.${admin.role}`)}</span>
          </span>
        </div>
      </Card>
      <Card title={t("admin.account.password")} className="stagger" style={stagger(1)}>
        <PasswordForm />
      </Card>
      <Card title={t("admin.account.totp")} className="stagger" style={stagger(2)}>
        <div className="mb-4 flex flex-wrap items-center gap-2 text-sm" data-testid="own-totp-state" data-bound={admin.totp_bound}>
          <span className="text-fg-3">{t("admin.account.totpState")}</span>
          <Badge tone={admin.totp_bound ? "success" : "warn"}>{t(admin.totp_bound ? "admin.account.totpBound" : "admin.account.totpUnbound")}</Badge>
          {!admin.totp_bound && <span className="text-xs text-fg-3">{t("admin.account.totpUnboundHint")}</span>}
        </div>
        <TotpForm />
      </Card>
      {admin.totp_bound && (
        <Card title={t("admin.account.remove")} className="stagger" style={stagger(3)}>
          <RemoveTotp />
        </Card>
      )}
    </Page>
  );
}
