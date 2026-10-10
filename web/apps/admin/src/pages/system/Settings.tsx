import { dec, setLocale, useSettings, type Locale } from "@exchange/core";
import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { Button, ErrorState, Input, Segmented, Skeleton, Switch } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction } from "../../kit/actions";
import { Num, TimeText } from "../../kit/format";
import { PAGE_SIZES, pageSize, setPageSize } from "../../kit/lists";
import { stagger } from "../../kit/motion";
import { Card, Page } from "../../kit/Page";
import { settingsKey, useConsoleSettings } from "../../live";
import { setTheme, useTheme } from "../../theme";
import { ReadOnly } from "../../kit/ReadOnly";
import { SignInCode } from "./access";

const LIMITS = ["single_max_usdt", "daily_max_usdt", "withdrawal_max_usdt"] as const;
type Limit = (typeof LIMITS)[number];

/**
 * Settings (design 2026-10-02 §2, §4.6): whether fund operations need a
 * second administrator and the single-person limits (ADMIN changes them,
 * with a reason; every administrator reads them), whether sign-in asks for
 * the authenticator code (N1), and this browser's theme and language.
 */
export default function Settings({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const q = useConsoleSettings();
  const editable = can(admin, "settings.write");
  return (
    <Page title={t("admin.nav.settings")} help={t("admin.settings.help")}>
      <ReadOnly admin={admin} perm="settings.write" />
      {q.isError ? (
        <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />
      ) : (
        <Card title={t("admin.settings.approvals")} className="stagger">
          {q.data ? <Approvals settings={q.data} editable={editable} /> : <Skeleton className="h-40 w-full" />}
        </Card>
      )}
      <Card title={t("admin.access.totp")} className="stagger" style={stagger(1)}>
        <SignInCode admin={admin} />
      </Card>
      <Card title={t("admin.settings.appearance")} className="stagger" style={stagger(2)}>
        <Appearance />
      </Card>
    </Page>
  );
}

function Approvals({ settings, editable }: { settings: NonNullable<ReturnType<typeof useConsoleSettings>["data"]>; editable: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [draft, setDraft] = useState<Record<Limit, string>>(() => pick(settings));
  const [delay, setDelay] = useState(() => String(settings.change_delay_seconds));
  useEffect(() => {
    setDraft(pick(settings));
    setDelay(String(settings.change_delay_seconds));
  }, [settings]);
  const floor = settings.change_delay_floor_seconds;
  const delayOK = /^\d+$/.test(delay.trim()) && Number(delay) >= floor && Number(delay) <= 86400;
  const delayChanged = delay.trim() !== String(settings.change_delay_seconds);
  const changed = LIMITS.filter((k) => draft[k].trim() !== settings[k]);
  const valid = LIMITS.every((k) => dec.isDecimal(draft[k].trim()) && dec.gt(draft[k].trim(), "0")) && delayOK;
  const put = async (body: Record<string, unknown>) => {
    const res = adminData(await adminApi.PUT("/admin/v1/settings", { body: body as never }));
    qc.setQueryData(settingsKey, res);
    return res;
  };
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-start gap-4">
        <div className="min-w-0 flex-1">
          <div className="font-medium">{t("admin.settings.twoPerson")}</div>
          <p className="mt-1 text-sm text-fg-3">{t("admin.settings.twoPersonHelp")}</p>
        </div>
        <DangerAction
          trigger={(open) => (
            <Switch
              checked={settings.two_person_approval}
              disabled={!editable}
              onCheckedChange={open}
              label={<span className="text-sm">{t(settings.two_person_approval ? "admin.settings.on" : "admin.settings.off")}</span>}
            />
          )}
          danger={settings.two_person_approval}
          title={t(settings.two_person_approval ? "admin.settings.turnOffTitle" : "admin.settings.turnOnTitle")}
          description={t(settings.two_person_approval ? "admin.settings.turnOffHint" : "admin.settings.turnOnHint")}
          target={<span className="font-mono">admin.two_person_approval</span>}
          confirmWord={settings.two_person_approval ? "off" : "on"}
          run={(reason) => put({ two_person_approval: !settings.two_person_approval, reason })}
          success={t("admin.settings.saved")}
        />
      </div>
      <div>
        <div className="mb-3 font-medium">{t("admin.settings.limits")}</div>
        <div className="grid gap-4 md:grid-cols-3">
          {LIMITS.map((k) => (
            <label key={k} className="flex flex-col gap-1.5 text-sm text-fg-2">
              {t(`admin.settings.${k}`)}
              <Input
                value={draft[k]}
                onValueChange={(v) => setDraft({ ...draft, [k]: v })}
                inputMode="decimal"
                unit="USDT"
                disabled={!editable}
                error={dec.isDecimal(draft[k].trim()) && dec.gt(draft[k].trim(), "0") ? undefined : t("admin.funds.invalidAmount")}
              />
              <span className="text-xs text-fg-3">{t(`admin.settings.${k}_hint`)}</span>
            </label>
          ))}
          <label className="flex flex-col gap-1.5 text-sm text-fg-2">
            {t("admin.changes.delay")}
            <Input
              id="change-delay"
              value={delay}
              onValueChange={setDelay}
              inputMode="numeric"
              unit="s"
              disabled={!editable}
              error={delayOK ? undefined : t("admin.changes.delayHint", { floor })}
            />
            <span className="text-xs text-fg-3">{t("admin.changes.delayHint", { floor })}</span>
          </label>
        </div>
        <div className="mt-4 flex flex-wrap items-center gap-3 text-sm text-fg-3">
          {editable && (
            <DangerAction
              trigger={(open) => (
                <Button size="sm" disabled={!valid || (changed.length === 0 && !delayChanged)} onClick={open}>
                  {t("admin.common.save")}
                </Button>
              )}
              danger={false}
              title={t("admin.settings.saveTitle")}
              target={
                <span className="flex flex-col gap-0.5">
                  {changed.map((k) => (
                    <span key={k}>
                      {t(`admin.settings.${k}`)}: <Num value={settings[k]} /> → <Num value={draft[k].trim()} /> USDT
                    </span>
                  ))}
                  {delayChanged && (
                    <span>
                      {t("admin.changes.delay")}: {settings.change_delay_seconds} → {delay.trim()}
                    </span>
                  )}
                </span>
              }
              confirmWord="save"
              run={(reason) =>
                put({
                  ...Object.fromEntries(changed.map((k) => [k, draft[k].trim()])),
                  ...(delayChanged ? { change_delay_seconds: Number(delay.trim()) } : {}),
                  reason,
                })
              }
              success={t("admin.settings.saved")}
            />
          )}
          <span>
            {t("admin.settings.used", { used: settings.daily_used_usdt })}
            {settings.updated_by && (
              <>
                {" · "}
                {t("admin.settings.updatedBy", { who: settings.updated_by })} <TimeText value={settings.updated_at} />
              </>
            )}
          </span>
        </div>
      </div>
    </div>
  );
}

function pick(s: Record<Limit, string>): Record<Limit, string> {
  return { single_max_usdt: s.single_max_usdt, daily_max_usdt: s.daily_max_usdt, withdrawal_max_usdt: s.withdrawal_max_usdt };
}

/** Appearance is this browser's theme, language and page size. */
function Appearance() {
  const { t } = useTranslation();
  const theme = useTheme();
  const locale = useSettings((s) => s.locale);
  const [size, setSize] = useState(pageSize);
  return (
    <div className="flex flex-wrap gap-8">
      <label className="flex flex-col gap-1.5 text-sm text-fg-2">
        {t("admin.settings.theme")}
        <Segmented
          size="md"
          value={theme}
          onValueChange={(v) => setTheme(v === "dark" ? "dark" : "light")}
          items={[
            { value: "light", label: t("admin.shell.light") },
            { value: "dark", label: t("admin.shell.dark") },
          ]}
        />
      </label>
      <label className="flex flex-col gap-1.5 text-sm text-fg-2">
        {t("admin.settings.language")}
        <Segmented
          size="md"
          value={locale}
          onValueChange={(v) => setLocale(v as Locale)}
          items={[
            { value: "zh-CN", label: "中文" },
            { value: "en", label: "English" },
          ]}
        />
      </label>
      <label className="flex flex-col gap-1.5 text-sm text-fg-2">
        {t("admin.settings.pageSize")}
        <Segmented
          size="md"
          value={String(size)}
          onValueChange={(v) => {
            setPageSize(Number(v));
            setSize(Number(v));
          }}
          items={PAGE_SIZES.map((n) => ({ value: String(n), label: String(n) }))}
        />
        <span className="text-xs text-fg-3">{t("admin.settings.pageSizeHint")}</span>
      </label>
    </div>
  );
}
