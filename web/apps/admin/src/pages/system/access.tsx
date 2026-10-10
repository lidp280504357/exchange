import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { Badge, ErrorState, Skeleton, Switch } from "@exchange/ui";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { DangerAction } from "../../kit/actions";
import { Fields } from "../../kit/fields";
import { TimeText } from "../../kit/format";

// The console's access switches (design 2026-10-02, N1) on the settings
// page: whether sign-in asks for the authenticator code.

export const accessKey = ["admin", "settings", "access"] as const;

/** useConsoleAccess reads the console's access switches. */
export function useConsoleAccess() {
  return useQuery({ queryKey: accessKey, queryFn: async () => adminData(await adminApi.GET("/admin/v1/settings/access")) });
}

/**
 * SignInCode is the sign-in code switch (admin.require_totp): on, the
 * password and the authenticator's code sign in. ADMIN switches it, on
 * only once their own authenticator and an active ADMIN's are bound; the
 * administrators without one are named (counted for those who do not
 * manage administrators), as they cannot sign in while it is on.
 */
export function SignInCode({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const q = useConsoleAccess();
  if (q.isError) return <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />;
  const v = q.data;
  if (!v) return <Skeleton className="h-32 w-full" />;
  const editable = can(admin, "settings.write");
  const blocked = !v.require_totp && (!v.you_bound || v.bound_admins === 0);
  const put = async (reason: string) => {
    const res = adminData(await adminApi.PUT("/admin/v1/settings/access/totp", { body: { enabled: !v.require_totp, reason } }));
    qc.setQueryData(accessKey, res);
    await qc.invalidateQueries({ queryKey: ["admin", "login-options"] });
    return res;
  };
  const bound = (yes: boolean) => <Badge tone={yes ? "success" : "warn"}>{t(yes ? "admin.access.bound" : "admin.access.unbound")}</Badge>;
  return (
    <div className="flex flex-col gap-4" data-testid="access-totp" data-on={v.require_totp}>
      <div className="flex flex-wrap items-start gap-4">
        <p className="min-w-0 flex-1 text-sm text-fg-3">{t("admin.access.totpHelp")}</p>
        <DangerAction
          trigger={(open) => (
            <Switch
              checked={v.require_totp}
              disabled={!editable || blocked}
              onCheckedChange={open}
              label={<span className="text-sm">{t(v.require_totp ? "admin.access.on" : "admin.access.off")}</span>}
            />
          )}
          danger
          title={t(v.require_totp ? "admin.access.turnOffTitle" : "admin.access.turnOnTitle")}
          description={t(v.require_totp ? "admin.access.turnOffDesc" : "admin.access.turnOnDesc")}
          target={<span className="font-mono">admin.require_totp</span>}
          confirmWord={v.require_totp ? "off" : "on"}
          run={put}
          success={t("admin.access.saved")}
        />
      </div>
      {blocked && editable && (
        <p className="text-sm text-warn-strong" data-testid="access-totp-blocked">
          {t("admin.access.needBound")}
        </p>
      )}
      <Fields
        label={t("admin.access.totp")}
        items={[
          {
            label: t("admin.access.you"),
            value: (
              <span className="inline-flex flex-wrap items-center gap-2" data-testid="access-you" data-bound={v.you_bound}>
                {bound(v.you_bound)}
                {!v.you_bound && (
                  <Link to="/account" className="text-sm text-info-strong hover:underline">
                    {t("admin.access.bindFirst")}
                  </Link>
                )}
              </span>
            ),
          },
          { label: t("admin.access.boundAdmins"), value: <span data-testid="access-bound-admins">{v.bound_admins}</span> },
          {
            label: t("admin.access.unbound"),
            value:
              v.unbound_count === 0 ? (
                t("admin.access.noneUnbound")
              ) : v.unbound.length > 0 ? (
                <span className="flex flex-col gap-0.5">
                  <span className="text-xs text-fg-3">{t("admin.access.unboundList")}</span>
                  {v.unbound.map((a) => (
                    <span key={a.id}>
                      {a.email} <span className="text-fg-3">({t(`admin.roles.${a.role}`)})</span>
                    </span>
                  ))}
                </span>
              ) : (
                t("admin.access.unboundCount", { n: v.unbound_count })
              ),
          },
          {
            label: t("admin.access.state"),
            value: v.updated_by ? (
              <span>
                {t("admin.access.updated", { who: v.updated_by })} <TimeText value={v.updated_at} />
              </span>
            ) : (
              "—"
            ),
          },
        ]}
      />
      <p className="text-xs text-fg-3">{t("admin.access.emergency")}</p>
    </div>
  );
}
