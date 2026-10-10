import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, ErrorState, Skeleton, Switch } from "@exchange/ui";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { DangerAction } from "../../kit/actions";
import { Fields } from "../../kit/fields";
import { TimeText } from "../../kit/format";
import { entries, holds } from "./allowlist";

// The console's access switches (design 2026-10-02, N1) on the settings
// page: whether sign-in asks for the authenticator code, and the addresses
// the console's API answers.

type ConsoleAccess = AdminSchemas["ConsoleAccess"];

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

/**
 * AccessRestriction is the access restriction (admin.access_restriction):
 * on, the console's API answers only the list's addresses. The list is
 * edited here (the caller's address offered to add, an IPv6 /64 prefix
 * advised for dual-stack networks); switching it on or saving the list
 * while it is on warns when the caller's address is not in it, as the
 * server then refuses (ADMIN_ACCESS_SELF_LOCKOUT).
 */
export function AccessRestriction({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const q = useConsoleAccess();
  const [draft, setDraft] = useState("");
  const stored = q.data?.access_allowlist.join("\n") ?? "";
  useEffect(() => setDraft(stored), [stored]);
  if (q.isError) return <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />;
  const v = q.data;
  if (!v) return <Skeleton className="h-40 w-full" />;
  const editable = can(admin, "settings.write");
  const list = entries(draft);
  const changed = list.join("\n") !== v.access_allowlist.join("\n");
  const yours = holds(list, v.your_ip);
  const put = async (on: boolean, reason: string) => {
    const res: ConsoleAccess = adminData(
      await adminApi.PUT("/admin/v1/settings/access/restriction", { body: { enabled: on, allowlist: list, reason } }),
    );
    qc.setQueryData(accessKey, res);
    return res;
  };
  const listTarget = (
    <span className="flex flex-col gap-0.5 font-mono text-xs">
      {list.length ? list.map((e) => <span key={e}>{e}</span>) : <span>{t("admin.access.empty")}</span>}
    </span>
  );
  return (
    <div className="flex flex-col gap-4" data-testid="access-restriction" data-on={v.access_restriction}>
      <div className="flex flex-wrap items-start gap-4">
        <p className="min-w-0 flex-1 text-sm text-fg-3">{t("admin.access.restrictionHelp")}</p>
        <DangerAction
          trigger={(open) => (
            <Switch
              checked={v.access_restriction}
              disabled={!editable || (!v.access_restriction && list.length === 0)}
              onCheckedChange={open}
              label={<span className="text-sm">{t(v.access_restriction ? "admin.access.restrictionOn" : "admin.access.restrictionOff")}</span>}
            />
          )}
          danger
          title={t(v.access_restriction ? "admin.access.turnOffRestriction" : "admin.access.turnOnRestriction")}
          description={
            <span className="flex flex-col gap-1">
              <span>{t(v.access_restriction ? "admin.access.turnOffRestrictionDesc" : "admin.access.turnOnRestrictionDesc")}</span>
              {!v.access_restriction && !yours && <span className="text-danger-strong">{t("admin.access.notYours", { ip: v.your_ip })}</span>}
            </span>
          }
          target={listTarget}
          confirmWord={v.access_restriction ? "off" : "on"}
          run={(reason) => put(!v.access_restriction, reason)}
          success={t("admin.access.saved")}
        />
      </div>
      <Fields
        label={t("admin.access.restriction")}
        items={[
          {
            label: t("admin.access.yourIp"),
            value: (
              <span className="inline-flex flex-wrap items-center gap-2">
                <span className="font-mono" data-testid="access-your-ip">
                  {v.your_ip}
                </span>
                {editable && !yours && (
                  <Button size="sm" variant="secondary" onClick={() => setDraft((d) => (d.trim() ? `${d.trim()}\n${v.your_ip}` : v.your_ip))}>
                    {t("admin.access.addYours")}
                  </Button>
                )}
              </span>
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
      <label className="flex flex-col gap-1.5 text-sm text-fg-2">
        <span>
          {t("admin.access.list")} <span className="text-xs text-fg-3">{t("admin.access.listCount", { n: list.length })}</span>
        </span>
        <textarea
          value={draft}
          disabled={!editable}
          rows={5}
          placeholder={t("admin.access.listPlaceholder")}
          onChange={(e) => setDraft(e.target.value)}
          className="w-full max-w-md rounded-1 border border-line-1 bg-bg-1 px-2.5 py-1.5 font-mono text-sm text-fg-1 disabled:opacity-60"
          aria-label={t("admin.access.list")}
          data-testid="access-list"
        />
        <span className="text-xs text-fg-3">{t("admin.access.ipv6Hint")}</span>
      </label>
      {v.access_restriction && changed && !yours && (
        <p className="text-sm text-danger-strong" data-testid="access-not-yours">
          {t("admin.access.notYours", { ip: v.your_ip })}
        </p>
      )}
      <p className="text-sm text-warn-strong" data-testid="access-guard">
        {t("admin.access.guard")}
      </p>
      {editable && (
        <div>
          <DangerAction
            trigger={(open) => (
              <Button size="sm" disabled={!changed || (v.access_restriction && list.length === 0)} onClick={open}>
                {t("admin.access.saveList")}
              </Button>
            )}
            danger={v.access_restriction}
            title={t("admin.access.saveListTitle")}
            description={t("admin.access.saveListDesc")}
            target={listTarget}
            confirmWord="save"
            run={(reason) => put(v.access_restriction, reason)}
            success={t("admin.access.saved")}
          />
        </div>
      )}
      <p className="text-xs text-fg-3">{t("admin.access.restrictionEmergency")}</p>
    </div>
  );
}
