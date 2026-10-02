import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { Button, CoinIcon, FormField, Input, Skeleton } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { ImageUp, Trash2 } from "lucide-react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, FormError } from "../../kit/actions";

// An asset's profile (ASTRA design §5.3, C4c): the name, introductions,
// links and logo the sites show for it. instrument-service checks the
// upload again (type, size, square; an SVG is rebuilt from an allow list)
// and versions it, so the sites pick up a new logo within a minute.

type Profile = AdminSchemas["AssetProfile"];
type Mime = "image/png" | "image/svg+xml" | "image/webp";

const MIMES: Mime[] = ["image/png", "image/svg+xml", "image/webp"];
const MAX_LOGO = 200 * 1024;
const LINKS = ["website", "explorer", "whitepaper"] as const;
const profileKey = (code: string) => ["admin", "asset-profile", code];

type Draft = { name: string; zh: string; en: string; links: Record<string, string>; logo?: { data: string; mime: Mime; preview: string }; clear: boolean };

const draftOf = (p: Profile): Draft => ({
  name: p.display_name, zh: p.description["zh-CN"] ?? "", en: p.description.en ?? "", links: { ...p.links }, clear: false,
});

/** readLogo checks a chosen file as instrument-service will (type, size, square) and reads it. */
async function readLogo(file: File, t: (k: string, o?: Record<string, unknown>) => string): Promise<NonNullable<Draft["logo"]>> {
  if (!MIMES.includes(file.type as Mime)) throw new FormError(t("admin.profile.badType"));
  if (file.size > MAX_LOGO) throw new FormError(t("admin.profile.tooLarge"));
  const preview = await new Promise<string>((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(new FormError(t("admin.profile.unreadable")));
    r.readAsDataURL(file);
  });
  if (file.type !== "image/svg+xml") {
    const img = new Image();
    img.src = preview;
    await img.decode().catch(() => {
      throw new FormError(t("admin.profile.unreadable"));
    });
    if (img.naturalWidth !== img.naturalHeight) throw new FormError(t("admin.profile.notSquare", { w: img.naturalWidth, h: img.naturalHeight }));
  }
  return { data: preview.slice(preview.indexOf(",") + 1), mime: file.type as Mime, preview };
}

/** AssetProfileSection shows an asset's profile in its drawer and edits it. */
export function AssetProfileSection({ code }: { code: string }) {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: profileKey(code),
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/assets/{code}/profile", { params: { path: { code } } })),
  });
  const [draft, setDraft] = useState<Draft | null>(null);
  const [logoError, setLogoError] = useState("");
  const file = useRef<HTMLInputElement>(null);
  const p = q.data;
  if (q.isPending) return <Skeleton className="h-24 w-full" />;
  if (!p) return <p className="text-sm text-fg-3">{t("admin.common.unavailable")}</p>;
  const shown = draft?.logo ? draft.logo.preview : draft?.clear ? "" : p.logo_url;
  const save = async (reason: string) => {
    if (!draft) return null;
    const links: Record<string, string> = {};
    for (const [k, v] of Object.entries(draft.links)) if (v.trim()) links[k] = v.trim();
    if (Object.values(links).some((v) => !/^https:\/\/[^\s/]+\.[^\s]+$/.test(v))) throw new FormError(t("admin.profile.badLink"));
    const description: Record<string, string> = {};
    if (draft.zh.trim()) description["zh-CN"] = draft.zh.trim();
    if (draft.en.trim()) description.en = draft.en.trim();
    return adminData(
      await adminApi.PUT("/admin/v1/assets/{code}/profile", {
        params: { path: { code } },
        body: {
          display_name: draft.name.trim(), description, links, reason,
          ...(draft.logo ? { logo: draft.logo.data, logo_mime: draft.logo.mime } : {}),
          ...(draft.clear ? { clear_logo: true } : {}),
        },
      }),
    );
  };
  return (
    <section className="flex flex-col gap-3" data-testid="asset-profile">
      <div className="flex items-center gap-2">
        <h3 className="flex-1 text-sm font-semibold">{t("admin.profile.title")}</h3>
        <span className="font-mono text-xs text-fg-3">v{p.version}</span>
        {!draft && (
          <Button size="sm" variant="secondary" onClick={() => setDraft(draftOf(p))}>
            {t("admin.listing.edit")}
          </Button>
        )}
      </div>
      <div className="flex items-start gap-4 rounded-2 border border-line-1 p-3">
        <div className="flex flex-col items-center gap-1.5">
          {shown ? (
            <img src={shown} alt="" className="size-14 rounded-full border border-line-1 bg-bg-2 object-contain" />
          ) : (
            <CoinIcon symbol={code} size={56} />
          )}
          <span className="text-xs text-fg-3">{shown ? (draft?.logo ? draft.logo.mime : p.logo_mime) : t("admin.profile.noLogo")}</span>
        </div>
        {draft ? (
          <div className="flex min-w-0 flex-1 flex-col gap-3">
            <FormField label={t("admin.profile.displayName")} hint={t("admin.profile.displayNameHint")}>
              <Input id="profile-name" value={draft.name} maxLength={32} onValueChange={(v) => setDraft({ ...draft, name: v })} />
            </FormField>
            {(["zh", "en"] as const).map((l) => (
              <FormField key={l} label={t(l === "zh" ? "admin.profile.descriptionZh" : "admin.profile.descriptionEn")}>
                <textarea
                  id={`profile-description-${l}`}
                  value={draft[l]}
                  maxLength={1000}
                  rows={3}
                  onChange={(e) => setDraft({ ...draft, [l]: e.target.value })}
                  className="w-full rounded-2 border border-line-1 bg-bg-1 px-3 py-2 text-sm text-fg-1 outline-none focus-visible:border-brand"
                />
              </FormField>
            ))}
            {LINKS.map((k) => (
              <FormField key={k} label={t(`admin.profile.links.${k}`)}>
                <Input value={draft.links[k] ?? ""} placeholder="https://" onValueChange={(v) => setDraft({ ...draft, links: { ...draft.links, [k]: v } })} />
              </FormField>
            ))}
            <div className="flex flex-wrap items-center gap-2">
              <input
                ref={file}
                type="file"
                accept={MIMES.join(",")}
                className="hidden"
                data-testid="profile-logo-file"
                onChange={async (e) => {
                  const f = e.target.files?.[0];
                  e.target.value = "";
                  if (!f) return;
                  try {
                    setDraft({ ...draft, logo: await readLogo(f, t), clear: false });
                    setLogoError("");
                  } catch (err) {
                    setLogoError(err instanceof Error ? err.message : String(err));
                  }
                }}
              />
              <Button size="sm" variant="secondary" icon={<ImageUp size={14} />} onClick={() => file.current?.click()}>
                {t("admin.profile.upload")}
              </Button>
              {(p.logo_url || draft.logo) && !draft.clear && (
                <Button size="sm" variant="ghost" icon={<Trash2 size={14} />} onClick={() => setDraft({ ...draft, logo: undefined, clear: Boolean(p.logo_url) })}>
                  {t("admin.profile.removeLogo")}
                </Button>
              )}
              <span className="text-xs text-fg-3">{t("admin.profile.logoHint")}</span>
            </div>
            {logoError && <p className="text-xs text-danger">{logoError}</p>}
            <div className="flex justify-end gap-2">
              <Button variant="secondary" onClick={() => setDraft(null)}>
                {t("common.cancel")}
              </Button>
              <DangerAction
                trigger={(open) => <Button onClick={open} data-testid="profile-save">{t("admin.common.save")}</Button>}
                danger={false}
                title={t("admin.profile.saveTitle", { code })}
                description={t("admin.profile.saveHint")}
                target={<span className="font-mono">{code}</span>}
                confirmWord={code.toLowerCase()}
                run={save}
                success={t("admin.profile.saved")}
                invalidate={[profileKey(code)]}
                onDone={() => setDraft(null)}
              />
            </div>
          </div>
        ) : (
          <dl className="grid min-w-0 flex-1 grid-cols-[6rem_1fr] gap-x-3 gap-y-1.5 text-sm">
            <dt className="text-fg-3">{t("admin.profile.displayName")}</dt>
            <dd>{p.display_name || <span className="text-fg-3">{t("admin.profile.sameAsName")}</span>}</dd>
            <dt className="text-fg-3">{t("admin.profile.description")}</dt>
            <dd className="whitespace-pre-wrap break-words">{p.description["zh-CN"] || p.description.en || "—"}</dd>
            {LINKS.filter((k) => p.links[k]).map((k) => (
              <span key={k} className="contents">
                <dt className="text-fg-3">{t(`admin.profile.links.${k}`)}</dt>
                <dd className="truncate font-mono text-xs">{p.links[k]}</dd>
              </span>
            ))}
          </dl>
        )}
      </div>
    </section>
  );
}
