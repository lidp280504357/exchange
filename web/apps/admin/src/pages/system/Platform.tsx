import { formatDecimal, LANGUAGES } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, ErrorState, FormField, IconButton, Input, KeyTag, Segmented, Select, ShortList, Skeleton, SummaryRow, SummaryTable, Switch } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { ImageUp, Plus, Trash2, X } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { DangerAction, FormError } from "../../kit/actions";
import { readSquareImage, type ChosenImage, type ImageMime } from "../../kit/image";
import { Card, Page } from "../../kit/Page";
import { platformKey, usePlatformProfile } from "../../kit/profile";
import { launchKey } from "./Launch";
import { ReadOnly } from "../../kit/ReadOnly";
import { Amount, AmountGrid, FlagState, Lines, positive } from "../../kit/summary";
import { TextsField } from "../../kit/texts";

// The platform's settings (design 2026-10-04 §4.1, §4.2, §5; D2): the
// profile the sites show (name, colours, footer, contact, test mode and
// its banner, registration; one ADMIN, saved at once, the sites follow within a
// minute without a build), its images, and the welcome credits: lowering
// them applies at once, raising them waits for a second ADMIN.

type Profile = AdminSchemas["PlatformProfileAdmin"];
type Write = AdminSchemas["PlatformProfileWrite"];
type Draft = Omit<Write, "expected_version">;
type SocialKind = Write["social"][number]["kind"];
type ImageKind = keyof Profile["images"];
type Setting = AdminSchemas["WelcomeCreditsSetting"];

const welcomeKey = ["admin", "platform", "welcome"];

const SOCIAL: SocialKind[] = ["x", "telegram", "discord", "youtube", "facebook", "instagram", "linkedin", "reddit", "medium", "github", "tiktok", "weibo"];
const IMAGES: { kind: ImageKind; mimes: ImageMime[]; minSide?: number; dark?: boolean }[] = [
  { kind: "logo_light", mimes: ["image/png", "image/svg+xml", "image/webp"] },
  { kind: "logo_dark", mimes: ["image/png", "image/svg+xml", "image/webp"], dark: true },
  { kind: "favicon", mimes: ["image/png", "image/svg+xml"] },
  { kind: "apple_touch_icon", mimes: ["image/png"], minSide: 180 },
];
const MAX_IMAGE = 200 * 1024;
// The colour picker needs a valid value while the typed one is not: an
// input's value, not a style (no token applies to it).
const PICKER_FALLBACK = "#" + "000000";

const draftOf = (p: Profile): Draft => ({
  name: p.name, short_name: p.short_name, domain: p.domain, theme_color: p.theme_color, brand_color: p.brand_color,
  footer: { copyright: { ...p.footer.copyright }, compliance: { ...p.footer.compliance } },
  contact: { email: p.contact.email, support_url: p.contact.support_url ?? null },
  social: p.social.map((s) => ({ kind: s.kind as SocialKind, url: s.url })),
  default_locale: p.default_locale,
  test_mode: { enabled: p.test_mode.enabled, banner: p.test_mode.banner, text: { ...p.test_mode.text } },
  registration: { status: p.registration.status, closed_text: { ...p.registration.closed_text } },
});

/** changedFields are the profile's fields a draft changes. */
const changedFields = (p: Profile, d: Draft) => {
  const base = draftOf(p);
  return (Object.keys(d) as (keyof Draft)[]).filter((k) => JSON.stringify(base[k]) !== JSON.stringify(d[k]));
};

export default function Platform({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const edit = can(admin, "settings.write");
  const q = usePlatformProfile();
  if (q.isError) return <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />;
  return (
    <Page title={t("admin.nav.platform")} help={t("admin.platform.help")}>
      <ReadOnly admin={admin} perm="settings.write" />
      {q.data ? <ProfileForm key={q.data.version} profile={q.data} edit={edit} /> : <Skeleton className="h-96 w-full" />}
      {q.data && <Images profile={q.data} edit={edit} />}
      <Welcome edit={edit} />
    </Page>
  );
}

/** ColorField is a #rrggbb colour with its picker. */
function ColorField({ label, value, onChange, disabled }: { label: string; value: string; onChange: (v: string) => void; disabled: boolean }) {
  return (
    <FormField label={label}>
      <span className="flex items-center gap-2">
        <input
          type="color"
          value={/^#[0-9a-f]{6}$/.test(value) ? value : PICKER_FALLBACK}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value.toLowerCase())}
          className="h-8 w-10 cursor-pointer rounded-1 border border-line-1 bg-bg-1"
          aria-label={label}
        />
        <Input size="sm" value={value} disabled={disabled} onValueChange={(v) => onChange(v.trim().toLowerCase())} className="w-28 font-mono" />
      </span>
    </FormField>
  );
}

function ProfileForm({ profile, edit }: { profile: Profile; edit: boolean }) {
  const { t } = useTranslation();
  const [d, setD] = useState<Draft>(() => draftOf(profile));
  const changed = changedFields(profile, d);
  const set = (p: Partial<Draft>) => setD({ ...d, ...p });
  const off = !edit;
  return (
    <Card
      title={t("admin.platform.profile")}
      extra={
        <span className="flex items-center gap-2">
          <span className="font-mono text-xs text-fg-3">v{profile.version} · {profile.updated_by}</span>
          {edit && (
            <DangerAction
              trigger={(open) => (
                <Button size="sm" disabled={!changed.length} onClick={open} data-testid="platform-save">
                  {t("admin.platform.save", { n: changed.length })}
                </Button>
              )}
              danger={false}
              title={t("admin.platform.saveTitle")}
              description={t("admin.platform.saveHint")}
              target={<span className="font-mono text-xs">{changed.join(", ")}</span>}
              confirmWord="platform"
              run={async (reason) => {
                if (d.name.trim().length < 2) throw new FormError(t("admin.platform.badName"));
                return adminData(await adminApi.PUT("/admin/v1/platform/profile", { body: { ...d, expected_version: profile.version, reason } }));
              }}
              success={t("admin.platform.saved")}
              invalidate={[platformKey, launchKey]}
            />
          )}
        </span>
      }
    >
      <div className="grid gap-6 lg:grid-cols-2">
        <fieldset className="flex flex-col gap-3">
          <legend className="mb-1 text-sm font-semibold text-fg-1">{t("admin.platform.brand")}</legend>
          <div className="grid gap-3 sm:grid-cols-2">
            <FormField label={t("admin.platform.name")} hint={t("admin.platform.nameHint")}>
              <Input size="sm" value={d.name} disabled={off} onValueChange={(v) => set({ name: v })} data-testid="platform-name" />
            </FormField>
            <FormField label={t("admin.platform.shortName")}>
              <Input size="sm" value={d.short_name} disabled={off} onValueChange={(v) => set({ short_name: v })} />
            </FormField>
          </div>
          <FormField label={t("admin.platform.domain")} hint={t("admin.platform.domainHint")}>
            <Input size="sm" value={d.domain} disabled={off} placeholder="astras.vip" onValueChange={(v) => set({ domain: v.trim().toLowerCase() })} className="font-mono" />
          </FormField>
          <div className="grid gap-3 sm:grid-cols-2">
            <ColorField label={t("admin.platform.themeColor")} value={d.theme_color} disabled={off} onChange={(v) => set({ theme_color: v })} />
            <ColorField label={t("admin.platform.brandColor")} value={d.brand_color} disabled={off} onChange={(v) => set({ brand_color: v })} />
          </div>
          {/* The sites follow the visitor's browser; this is the language when it asks for none of theirs (F30, A96). */}
          <FormField label={t("admin.platform.defaultLocale")} hint={t("admin.platform.defaultLocaleHint")}>
            <Segmented
              size="sm"
              value={d.default_locale}
              disabled={off}
              onValueChange={(v) => set({ default_locale: v as Draft["default_locale"] })}
              items={LANGUAGES.map((l) => ({ value: l.locale, label: l.name }))}
            />
          </FormField>
        </fieldset>
        <fieldset className="flex flex-col gap-3">
          <legend className="mb-1 text-sm font-semibold text-fg-1">{t("admin.platform.testMode")}</legend>
          <Switch
            checked={d.test_mode.enabled}
            disabled={off}
            onCheckedChange={(v) => set({ test_mode: { ...d.test_mode, enabled: v } })}
            label={t("admin.platform.testModeOn")}
          />
          <p className="-mt-1 text-xs text-fg-3">{t("admin.platform.testModeHint")}</p>
          <Switch
            checked={d.test_mode.banner}
            disabled={off || !d.test_mode.enabled}
            onCheckedChange={(v) => set({ test_mode: { ...d.test_mode, banner: v } })}
            label={t("admin.platform.testBanner")}
          />
          <TextsField
            label={t("admin.platform.testText")}
            value={d.test_mode.text}
            disabled={off || !d.test_mode.enabled || !d.test_mode.banner}
            onChange={(v) => set({ test_mode: { ...d.test_mode, text: v } })}
          />
          <FormField label={t("admin.platform.registration")}>
            <Segmented
              size="sm"
              value={d.registration.status}
              disabled={off}
              onValueChange={(v) => set({ registration: { ...d.registration, status: v as Draft["registration"]["status"] } })}
              items={[{ value: "OPEN", label: t("admin.launch.registration.OPEN") }, { value: "CLOSED", label: t("admin.launch.registration.CLOSED") }]}
            />
          </FormField>
          <TextsField
            label={t("admin.platform.closedText")}
            value={d.registration.closed_text}
            disabled={off}
            onChange={(v) => set({ registration: { ...d.registration, closed_text: v } })}
          />
        </fieldset>
        <fieldset className="flex flex-col gap-3 lg:col-span-2">
          <legend className="mb-1 text-sm font-semibold text-fg-1">{t("admin.platform.footer")}</legend>
          <div className="grid gap-3 lg:grid-cols-2">
            <TextsField label={t("admin.platform.copyright")} value={d.footer.copyright} disabled={off} onChange={(v) => set({ footer: { ...d.footer, copyright: v } })} />
            <TextsField label={t("admin.platform.compliance")} value={d.footer.compliance} disabled={off} long onChange={(v) => set({ footer: { ...d.footer, compliance: v } })} />
            <FormField label={t("admin.platform.email")}>
              <Input size="sm" value={d.contact.email} disabled={off} placeholder="support@example.com" onValueChange={(v) => set({ contact: { ...d.contact, email: v.trim() } })} />
            </FormField>
            <FormField label={t("admin.platform.supportUrl")}>
              <Input
                size="sm"
                value={d.contact.support_url ?? ""}
                disabled={off}
                placeholder="https://"
                onValueChange={(v) => set({ contact: { ...d.contact, support_url: v.trim() || null } })}
              />
            </FormField>
          </div>
          <Social value={d.social} disabled={off} onChange={(v) => set({ social: v })} />
        </fieldset>
      </div>
    </Card>
  );
}

/** Social edits the footer's social links (at most 10, https). */
function Social({ value, onChange, disabled }: { value: Draft["social"]; onChange: (v: Draft["social"]) => void; disabled: boolean }) {
  const { t } = useTranslation();
  return (
    <FormField label={t("admin.platform.social")}>
      <div className="flex flex-col gap-2">
        {value.map((s, i) => (
          <span key={i} className="flex items-center gap-2">
            <Select
              size="sm"
              value={s.kind}
              disabled={disabled}
              onValueChange={(k) => onChange(value.map((x, j) => (j === i ? { ...x, kind: k as SocialKind } : x)))}
              options={SOCIAL.map((k) => ({ value: k, label: k }))}
              className="w-36"
            />
            <Input
              size="sm"
              value={s.url}
              disabled={disabled}
              placeholder="https://"
              onValueChange={(u) => onChange(value.map((x, j) => (j === i ? { ...x, url: u.trim() } : x)))}
              className="flex-1"
            />
            {!disabled && <IconButton icon={<X />} size="xs" label={t("admin.platform.removeSocial")} onClick={() => onChange(value.filter((_, j) => j !== i))} />}
          </span>
        ))}
        {!disabled && value.length < 10 && (
          <Button size="sm" variant="secondary" icon={<Plus size={14} />} onClick={() => onChange([...value, { kind: "x", url: "" }])} className="self-start">
            {t("admin.platform.addSocial")}
          </Button>
        )}
      </div>
    </FormField>
  );
}

/** Images shows the platform's images and uploads or removes them, each its own audited change. */
function Images({ profile, edit }: { profile: Profile; edit: boolean }) {
  const { t } = useTranslation();
  const file = useRef<HTMLInputElement>(null);
  const [slot, setSlot] = useState<(typeof IMAGES)[number] | null>(null);
  const [chosen, setChosen] = useState<ChosenImage | null>(null);
  const [error, setError] = useState("");
  return (
    <Card title={t("admin.platform.images")} extra={<span className="text-xs text-fg-3">{t("admin.platform.imagesHint")}</span>}>
      <input
        ref={file}
        type="file"
        className="hidden"
        accept={slot?.mimes.join(",")}
        data-testid="platform-image-file"
        onChange={async (e) => {
          const f = e.target.files?.[0];
          e.target.value = "";
          if (!f || !slot) return;
          try {
            setChosen(await readSquareImage(f, { mimes: slot.mimes, maxBytes: MAX_IMAGE, minSide: slot.minSide }, t));
            setError("");
          } catch (err) {
            setError(err instanceof Error ? err.message : String(err));
          }
        }}
      />
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {IMAGES.map((s) => {
          const url = profile.images[s.kind];
          return (
            <div key={s.kind} className="flex flex-col gap-2 rounded-2 border border-line-1 p-3" data-testid={`platform-image-${s.kind}`}>
              <span className="text-sm font-medium text-fg-1">{t(`admin.platform.image.${s.kind}`)}</span>
              <div data-theme={s.dark ? "dark" : undefined} className="grid h-24 place-items-center rounded-1 bg-bg-2">
                {url ? <img src={url} alt="" className="max-h-20 max-w-20" /> : <span className="text-xs text-fg-3">{t("admin.platform.builtIn")}</span>}
              </div>
              <span className="text-xs text-fg-3">{t(`admin.platform.imageRule.${s.kind}`)}</span>
              {edit && (
                <span className="flex gap-2">
                  <Button
                    size="sm"
                    variant="secondary"
                    icon={<ImageUp size={14} />}
                    onClick={() => {
                      setSlot(s);
                      setError("");
                      setTimeout(() => file.current?.click());
                    }}
                  >
                    {t("admin.profile.upload")}
                  </Button>
                  {url && (
                    <DangerAction
                      trigger={(open) => (
                        <Button size="sm" variant="ghost" icon={<Trash2 size={14} />} onClick={open}>
                          {t("admin.profile.removeLogo")}
                        </Button>
                      )}
                      danger={false}
                      title={t("admin.platform.removeTitle", { kind: t(`admin.platform.image.${s.kind}`) })}
                      target={t(`admin.platform.image.${s.kind}`)}
                      confirmWord={s.kind}
                      run={async (reason) =>
                        adminData(await adminApi.DELETE("/admin/v1/platform/images/{kind}", { params: { path: { kind: s.kind } }, body: { reason } }))
                      }
                      success={t("admin.platform.removed")}
                      invalidate={[platformKey, launchKey]}
                    />
                  )}
                </span>
              )}
            </div>
          );
        })}
      </div>
      {error && <p className="mt-2 text-xs text-danger-strong">{error}</p>}
      {slot && chosen && (
        <DangerAction
          open
          onOpenChange={(o) => !o && setChosen(null)}
          danger={false}
          title={t("admin.platform.uploadTitle", { kind: t(`admin.platform.image.${slot.kind}`) })}
          target={
            <span className="flex items-center gap-3">
              <span data-theme={slot.dark ? "dark" : undefined} className="grid size-16 place-items-center rounded-1 bg-bg-2">
                <img src={chosen.preview} alt="" className="max-h-14 max-w-14" />
              </span>
              <span className="font-mono text-xs">
                {chosen.mime} · {Math.ceil(chosen.size / 1024)} KB
              </span>
            </span>
          }
          confirmWord={slot.kind}
          run={async (reason) =>
            adminData(
              await adminApi.PUT("/admin/v1/platform/images/{kind}", { params: { path: { kind: slot.kind } }, body: { data: chosen.data, mime: chosen.mime, reason } }),
            )
          }
          success={t("admin.platform.uploaded")}
          invalidate={[platformKey, launchKey]}
          onDone={() => setChosen(null)}
        />
      )}
    </Card>
  );
}

type Row = { asset: string; amount: string };

/** Welcome shows the welcome credits and changes them: lower at once, higher with a second ADMIN. */
function Welcome({ edit }: { edit: boolean }) {
  const { t } = useTranslation();
  const q = useQuery({ queryKey: welcomeKey, queryFn: async () => adminData(await adminApi.GET("/admin/v1/platform/welcome-credits")) });
  const [rows, setRows] = useState<Row[] | null>(null);
  const s = q.data;
  let body: ReactNode;
  if (q.isPending) body = <Skeleton className="h-16 w-full" />;
  else if (q.isError || !s) body = <p className="text-sm text-warn-strong">{t("admin.platform.welcomeUnknown")}</p>;
  else body = <WelcomeBody setting={s} rows={rows} setRows={setRows} edit={edit} />;
  return (
    <div id="welcome">
      <Card title={t("admin.platform.welcome")} extra={<Badge tone="warn">{t("admin.platform.welcomeLaunch")}</Badge>}>
        {body}
      </Card>
    </div>
  );
}

function WelcomeBody({ setting: s, rows, setRows, edit }: { setting: Setting; rows: Row[] | null; setRows: (r: Row[] | null) => void; edit: boolean }) {
  const { t } = useTranslation();
  const now = new Map(s.credits.map((c) => [c.asset, c.amount]));
  const asked = (rows ?? []).filter((r) => r.asset.trim() !== "");
  const raising = asked.some((r) => Number(r.amount) > Number(now.get(r.asset.trim().toUpperCase()) ?? 0));
  const given = s.credits.filter((c) => positive(c.amount));
  // A set of credits in words: "10,000 USDT、0.1 BTC", or none given.
  const creditsText = (cs: { asset: string; amount: string }[]) =>
    cs.length ? cs.map((c) => `${formatDecimal(c.amount)} ${c.asset}`).join(t("admin.summary.sep")) : t("admin.launch.nothing");
  return (
    <div className="flex flex-col gap-3">
      <SummaryTable label={t("admin.platform.welcome")} noStatus>
        <SummaryRow
          data-testid="welcome-now"
          title={t("admin.summary.welcome.now")}
          source={t("admin.summary.welcome.version", { v: s.version })}
          summary={
            given.length ? (
              <span>
                {t("admin.summary.welcome.given")} <ShortList items={given.map((c) => <Amount key={c.asset} value={c.amount} asset={c.asset} />)} />
              </span>
            ) : (
              t("admin.summary.welcome.none")
            )
          }
          details={given.length > 3 ? <AmountGrid rows={given.map((c) => [c.asset, c.amount])} /> : undefined}
        />
        <SummaryRow title={t("admin.summary.welcome.master")} source={<KeyTag>ledger.welcome_credit</KeyTag>} summary={<FlagState on={s.flag_enabled} scope={false} />} />
      </SummaryTable>
      <p className="text-xs text-fg-3">{t("admin.platform.welcomeHint")}</p>
      {edit && rows === null && (
        <Button size="sm" variant="secondary" className="self-start" onClick={() => setRows(s.credits.map((c) => ({ asset: c.asset, amount: c.amount })))} data-testid="welcome-edit">
          {t("admin.platform.welcomeEdit")}
        </Button>
      )}
      {edit && rows !== null && (
        <div className="flex flex-col gap-2" data-testid="welcome-rows">
          {rows.map((r, i) => (
            <span key={i} className="flex items-center gap-2">
              <Input size="sm" value={r.asset} placeholder="USDT" onValueChange={(v) => setRows(rows.map((x, j) => (j === i ? { ...x, asset: v.toUpperCase() } : x)))} className="w-28 font-mono" aria-label={`${t("admin.platform.asset")} ${i + 1}`} />
              <Input size="sm" value={r.amount} inputMode="decimal" onValueChange={(v) => setRows(rows.map((x, j) => (j === i ? { ...x, amount: v.trim() } : x)))} className="w-40 font-mono" aria-label={`${t("admin.platform.amount")} ${i + 1}`} />
              <IconButton icon={<X />} size="xs" label={t("admin.platform.removeCredit")} onClick={() => setRows(rows.filter((_, j) => j !== i))} />
            </span>
          ))}
          <span className="flex flex-wrap items-center gap-2">
            {rows.length < 10 && (
              <Button size="sm" variant="secondary" icon={<Plus size={14} />} onClick={() => setRows([...rows, { asset: "", amount: "" }])}>
                {t("admin.platform.addCredit")}
              </Button>
            )}
            <Button size="sm" variant="ghost" onClick={() => setRows([])}>
              {t("admin.platform.clearCredits")}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setRows(null)}>
              {t("common.cancel")}
            </Button>
            <DangerAction
              trigger={(open) => (
                <Button size="sm" onClick={open} data-testid="welcome-save">
                  {raising ? t("admin.platform.welcomeAsk") : t("admin.platform.welcomeSave")}
                </Button>
              )}
              danger={raising}
              title={t(raising ? "admin.platform.welcomeAskTitle" : "admin.platform.welcomeSaveTitle")}
              description={raising ? t("admin.platform.welcomeRaiseHint") : undefined}
              target={
                <Lines
                  items={[
                    [t("admin.summary.welcome.from"), creditsText(s.credits)],
                    [t("admin.summary.welcome.to"), creditsText(asked.map((r) => ({ asset: r.asset.trim().toUpperCase(), amount: r.amount || "0" })))],
                  ]}
                />
              }
              confirmWord="welcome"
              run={async (reason) => {
                for (const r of asked) {
                  if (!/^\d+(\.\d+)?$/.test(r.amount)) throw new FormError(t("admin.platform.badAmount", { asset: r.asset }));
                }
                const res = await adminApi.PUT("/admin/v1/platform/welcome-credits", {
                  body: { credits: asked.map((r) => ({ asset: r.asset.trim().toUpperCase(), amount: r.amount })), expected_version: s.version, reason },
                });
                return adminData(res);
              }}
              success={(res) => ((res as { approval?: unknown }).approval ? t("admin.platform.welcomeRequested") : t("admin.platform.welcomeSaved"))}
              invalidate={[welcomeKey, platformKey, launchKey, ["admin", "approvals"]]}
              onDone={() => setRows(null)}
            />
            <Link to="/approvals" className="text-xs text-info-strong hover:underline">
              {t("admin.sim.toApprovals")}
            </Link>
          </span>
        </div>
      )}
    </div>
  );
}
