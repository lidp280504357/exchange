import { errorFrom } from "@exchange/core/api/errors";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, ErrorState, FormField, Input, Progress, Segmented, Skeleton, Switch } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { FileUp, Trash2 } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { DangerAction, FormError } from "../../kit/actions";
import { IdText, TimeText } from "../../kit/format";
import { Card, Page } from "../../kit/Page";
import { ReadOnly } from "../../kit/ReadOnly";
import { sha256File } from "../../kit/sha256";
import { TextsField } from "../../kit/texts";
import { launchKey } from "./Launch";

// 平台设置 → App 下载 (design 2026-10-07, App download page §3; H2): each
// platform offered as a link, as an app uploaded here (in parts of 10 MiB,
// checked by the server and served by the sites under /downloads/) or not
// at all, with its notes and switch; the files kept, deleted one by one;
// iOS's optional configuration profile. One ADMIN (settings.write) alone,
// audited.

type App = AdminSchemas["PlatformAppAdmin"];
type AppFile = AdminSchemas["AppFile"];
type Platform = App["platform"];
type Mode = App["mode"];
type Kind = AppFile["kind"];
type Upload = AdminSchemas["AppUpload"];

export const appsKey = ["admin", "platform", "apps"];

/** The extension and the largest size of each platform's and kind's file. */
const EXT: Record<Platform, string> = { ANDROID: ".apk", IOS: ".ipa" };
const MAX_APP = 524288000;
const MAX_PROFILE = 1 << 20;

/** sizeText is a size in bytes as B, KB or MB. */
function sizeText(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1 << 20) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1 << 20)).toFixed(1)} MB`;
}

export default function AppsPage({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const edit = can(admin, "settings.write");
  const q = useQuery({ queryKey: appsKey, queryFn: async () => adminData(await adminApi.GET("/admin/v1/platform/apps")) });
  return (
    <Page title={t("admin.nav.appDownloads")} help={t("admin.apps.help")}>
      <ReadOnly admin={admin} perm="settings.write" />
      <p className="rounded-2 border border-line-1 bg-bg-2 px-3 py-2 text-xs text-fg-2" data-testid="apps-ota-hint">
        {t("admin.apps.otaHint")}
      </p>
      {q.isError ? (
        <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />
      ) : !q.data ? (
        <Skeleton className="h-96 w-full" />
      ) : (
        <>
          <EntryCard entry={q.data.entry} edit={edit} audit={can(admin, "audit.read")} />
          {q.data.apps.map((a) => (
            <AppCard key={a.platform} app={a} edit={edit} audit={can(admin, "audit.read")} />
          ))}
        </>
      )}
    </Page>
  );
}

/**
 * EntryCard is the switch for the sites' download entries (design
 * 2026-10-07, App download page §1.2 #8, user 19:3x; H5): on by default,
 * the entries showing even while no platform is offered; off, hidden, the
 * download page still open to a visit.
 */
function EntryCard({ entry, edit, audit }: { entry: AdminSchemas["AppEntryAdmin"]; edit: boolean; audit: boolean }) {
  const { t } = useTranslation();
  const hiding = entry.visible;
  return (
    <Card
      title={
        <span className="flex items-center gap-2">
          {t("admin.apps.entry.title")}
          <Badge tone={entry.visible ? "success" : "neutral"}>{t(entry.visible ? "admin.apps.entry.on" : "admin.apps.entry.off")}</Badge>
        </span>
      }
      extra={
        <span className="flex items-center gap-3 text-xs">
          <span className="font-mono text-fg-3">
            v{entry.version} · {entry.updated_by} · <TimeText value={entry.updated_at} />
          </span>
          {audit && (
            <Link to="/audit?target=app%3Adownload_entry" className="text-brand">
              {t("admin.apps.audit")}
            </Link>
          )}
        </span>
      }
    >
      <div className="flex flex-wrap items-center justify-between gap-3" data-testid="apps-entry" data-state={entry.visible ? "on" : "off"}>
        <p className="max-w-3xl text-xs text-fg-2">{t("admin.apps.entry.hint")}</p>
        {edit && (
          <DangerAction
            trigger={(open) => (
              <Button size="sm" variant={hiding ? "secondary" : "primary"} onClick={open} data-testid="apps-entry-switch">
                {t(hiding ? "admin.apps.entry.hide" : "admin.apps.entry.show")}
              </Button>
            )}
            danger={hiding}
            title={t(hiding ? "admin.apps.entry.hideTitle" : "admin.apps.entry.showTitle")}
            description={t(hiding ? "admin.apps.entry.hideHint" : "admin.apps.entry.showHint")}
            target={<span className="font-medium">{t("admin.apps.entry.title")}</span>}
            confirmWord="entry"
            run={async (reason) =>
              adminData(await adminApi.PUT("/admin/v1/platform/download-entry", { body: { visible: !entry.visible, reason } }))
            }
            success={t(hiding ? "admin.apps.entry.hidden" : "admin.apps.entry.shown")}
            invalidate={[appsKey, launchKey]}
          />
        )}
      </div>
    </Card>
  );
}

/** AppCard is one platform: its settings, its current app, uploads and the files kept. */
function AppCard({ app, edit, audit }: { app: App; edit: boolean; audit: boolean }) {
  const { t } = useTranslation();
  const name = t(`admin.apps.platform.${app.platform}`);
  return (
    <Card
      title={
        <span className="flex items-center gap-2">
          {name}
          <Badge tone={app.public ? "success" : "neutral"} title={app.public ? undefined : t("admin.apps.hiddenWhy")}>
            {app.public ? t("admin.apps.shown") : t("admin.apps.hidden")}
          </Badge>
        </span>
      }
      extra={
        <span className="flex items-center gap-3 text-xs">
          <span className="font-mono text-fg-3">
            v{app.version} · {app.updated_by}
          </span>
          {audit && (
            <Link to={`/audit?target=app%3A${app.platform}`} className="text-brand">
              {t("admin.apps.audit")}
            </Link>
          )}
        </span>
      }
    >
      <div className="flex flex-col gap-5" data-testid={`app-${app.platform}`}>
        <Settings key={app.version} app={app} edit={edit} />
        <section className="flex flex-col gap-2">
          <h3 className="text-sm font-semibold text-fg-1">{t("admin.apps.current")}</h3>
          {app.current ? <FileFacts app={app} file={app.current} /> : <p className="text-xs text-fg-3">{t("admin.apps.noCurrent")}</p>}
          {edit && <UploadFile platform={app.platform} kind="APP" />}
        </section>
        {app.platform === "IOS" && (
          <section className="flex flex-col gap-2">
            <h3 className="text-sm font-semibold text-fg-1">{t("admin.apps.mobileconfig")}</h3>
            <p className="text-xs text-fg-3">{t("admin.apps.mobileconfigHint")}</p>
            {app.mobileconfig ? <FileFacts app={app} file={app.mobileconfig} /> : <p className="text-xs text-fg-3">{t("admin.apps.noMobileconfig")}</p>}
            {edit && <UploadFile platform="IOS" kind="MOBILECONFIG" />}
          </section>
        )}
        <Files app={app} edit={edit} />
      </div>
    </Card>
  );
}

/** Settings edits a platform's mode, link, switch and notes, saved together on the version read. */
function Settings({ app, edit }: { app: App; edit: boolean }) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<Mode>(app.mode);
  const [link, setLink] = useState(app.link_url);
  const [enabled, setEnabled] = useState(app.enabled);
  const [notes, setNotes] = useState(app.notes);
  const changed = mode !== app.mode || link !== app.link_url || enabled !== app.enabled || JSON.stringify(notes) !== JSON.stringify(app.notes);
  const linkOK = link === "" || /^https:\/\/\S+$/.test(link);
  const off = !edit;
  return (
    <section className="grid gap-4 lg:grid-cols-2" data-testid={`app-settings-${app.platform}`}>
      <div className="flex flex-col gap-3">
        <FormField label={t("admin.apps.mode")}>
          <Segmented
            size="sm"
            value={mode}
            disabled={off}
            onValueChange={(v) => setMode(v as Mode)}
            items={(["OFF", "LINK", "FILE"] as const).map((m) => ({
              value: m,
              label: t(`admin.apps.modes.${m}`),
              disabled: m === "FILE" && !app.current,
            }))}
            aria-label={t("admin.apps.mode")}
          />
        </FormField>
        {!app.current && <p className="-mt-2 text-xs text-fg-3">{t("admin.apps.fileNeeded")}</p>}
        <FormField label={t("admin.apps.link")} hint={t("admin.apps.linkHint")}>
          <Input
            size="sm"
            value={link}
            disabled={off}
            placeholder="https://"
            error={linkOK ? undefined : t("admin.apps.badLink")}
            onValueChange={(v) => setLink(v.trim())}
            className="font-mono"
            aria-label={`${app.platform} link`}
          />
        </FormField>
        <Switch checked={enabled} disabled={off} onCheckedChange={setEnabled} label={t("admin.apps.enabled")} />
        {edit && (
          <span>
            <DangerAction
              trigger={(open) => (
                <Button size="sm" disabled={!changed || !linkOK} onClick={open} data-testid={`app-save-${app.platform}`}>
                  {changed ? t("admin.apps.save") : t("admin.apps.noChange")}
                </Button>
              )}
              danger={false}
              title={t("admin.apps.saveTitle", { platform: t(`admin.apps.platform.${app.platform}`) })}
              description={t("admin.apps.saveHint")}
              target={
                <span className="font-mono text-xs">
                  {t(`admin.apps.modes.${mode}`)}
                  {link && ` · ${link}`}
                  {` · ${enabled ? t("admin.apps.shown") : t("admin.apps.hidden")}`}
                </span>
              }
              confirmWord={app.platform.toLowerCase()}
              run={async (reason) => {
                if (mode === "LINK" && !link) throw new FormError(t("admin.apps.badLink"));
                return adminData(
                  await adminApi.PUT("/admin/v1/platform/apps/{platform}", {
                    params: { path: { platform: app.platform } },
                    body: { mode, link_url: link, notes, enabled, expected_version: app.version, reason },
                  }),
                );
              }}
              success={t("admin.apps.saved")}
              invalidate={[appsKey, launchKey]}
            />
          </span>
        )}
      </div>
      <TextsField label={t("admin.apps.notes")} value={notes} disabled={off} long onChange={setNotes} />
    </section>
  );
}

/** FileFacts is what the server read of a file and where the sites serve it. */
function FileFacts({ app, file }: { app: App; file: AppFile }) {
  const { t } = useTranslation();
  const row = (label: string, value: ReactNode) => (
    <>
      <dt className="text-fg-3">{label}</dt>
      <dd className="min-w-0 break-all">{value}</dd>
    </>
  );
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs" data-testid={`app-file-${file.file_id}`}>
      {row(t("admin.apps.fileName"), <span className="font-mono">{file.name}</span>)}
      {file.package && row(t(`admin.apps.package.${app.platform}`), <span className="font-mono">{file.package}</span>)}
      {file.version && row(t("admin.apps.version"), `${file.version}${file.build ? ` (${t("admin.apps.build")} ${file.build})` : ""}`)}
      {file.min_os && row(t(`admin.apps.minOS.${app.platform}`), file.min_os)}
      {row(t("admin.apps.size"), sizeText(file.size))}
      {row(t("admin.apps.sha256"), <IdText value={file.sha256} chars={16} />)}
      {row(t("admin.apps.uploaded"), (
        <span>
          <TimeText value={file.uploaded_at} /> · {file.uploaded_by}
        </span>
      ))}
      {row(t("admin.apps.download"), (
        <a href={file.url} className="font-mono text-brand" target="_blank" rel="noreferrer">
          {file.url}
        </a>
      ))}
      {file.manifest_url && row(t("admin.apps.manifest"), <span className="font-mono">{file.manifest_url}</span>)}
      {file.kind === "APP" && app.platform === "IOS" && app.public?.install_url && app.current?.file_id === file.file_id &&
        row(t("admin.apps.install"), <span className="font-mono">{app.public.install_url}</span>)}
    </dl>
  );
}

/** putPart sends one part's bytes as they are (application/octet-stream). */
async function putPart(platform: Platform, id: string, n: number, part: Blob) {
  const res = await fetch(`/admin/v1/platform/apps/${platform}/uploads/${id}/parts/${n}`, {
    method: "PUT",
    credentials: "same-origin",
    headers: { "Content-Type": "application/octet-stream", "X-Admin-CSRF": "1" },
    body: part,
  });
  if (!res.ok) throw errorFrom(res.status, res.statusText, await res.json().catch(() => undefined));
}

type Chosen = { file: File; sha256: string };

/** Unfinished is an upload left half-way, remembered in the browser until it is completed or abandoned. */
type Unfinished = { id: string; sha256: string; name: string; size: number };

const unfinishedKey = (platform: Platform, kind: Kind) => `admin.appUpload.${platform}.${kind}`;

function readUnfinished(platform: Platform, kind: Kind): Unfinished | null {
  try {
    const v = JSON.parse(localStorage.getItem(unfinishedKey(platform, kind)) ?? "null") as Unfinished | null;
    return v && typeof v.id === "string" && typeof v.sha256 === "string" ? v : null;
  } catch {
    return null;
  }
}

function keepUnfinished(platform: Platform, kind: Kind, u: Unfinished | null) {
  try {
    if (u) localStorage.setItem(unfinishedKey(platform, kind), JSON.stringify(u));
    else localStorage.removeItem(unfinishedKey(platform, kind));
  } catch {
    // Private windows may refuse storage: the upload is then resumed only until a reload.
  }
}

/**
 * UploadFile picks a file, hashes it, and once confirmed sends it in parts
 * and completes it. An upload that stopped half-way is remembered in the
 * browser (review FX, A74 ①): choosing the same file resumes it from the
 * parts the server has, another file drops it before starting anew; it
 * can be abandoned, its parts dropped.
 */
function UploadFile({ platform, kind }: { platform: Platform; kind: Kind }) {
  const { t } = useTranslation();
  const input = useRef<HTMLInputElement>(null);
  const ext = kind === "MOBILECONFIG" ? ".mobileconfig" : EXT[platform];
  const max = kind === "MOBILECONFIG" ? MAX_PROFILE : MAX_APP;
  const [chosen, setChosen] = useState<Chosen | null>(null);
  // The share of the file hashed, null while not hashing.
  const [hashing, setHashing] = useState<number | null>(null);
  const [error, setError] = useState("");
  const [progress, setProgress] = useState<{ done: number; parts: number; checking: boolean } | null>(null);
  // The upload a failure (or a reload) left, resumed with the same file.
  const [unfinished, setUnfinishedState] = useState<Unfinished | null>(() => readUnfinished(platform, kind));
  const setUnfinished = (u: Unfinished | null) => {
    keepUnfinished(platform, kind, u);
    setUnfinishedState(u);
  };
  const label = kind === "MOBILECONFIG" ? t("admin.apps.upload.MOBILECONFIG") : t(`admin.apps.upload.APP.${platform}`);

  const send = async (reason: string) => {
    if (!chosen) return null;
    const { file, sha256: sum } = chosen;
    let up: Upload | null = null;
    if (unfinished?.sha256 === sum) {
      const res = await adminApi.GET("/admin/v1/platform/apps/{platform}/uploads/{upload_id}", {
        params: { path: { platform, upload_id: unfinished.id } },
      });
      // Only onto the server's upload of this very file (review GF, A76 ③).
      const same = res.data && res.data.sha256 === sum && res.data.size === file.size && res.data.kind === kind;
      if (res.response.ok && same) up = res.data!;
    }
    if (unfinished && !up) {
      // Not resumed: dropped first, so it holds none of the platform's few
      // open uploads for the day it would wait (review GI, A78 ①); gone
      // already is as good, and one that cannot be dropped now expires.
      await adminApi
        .DELETE("/admin/v1/platform/apps/{platform}/uploads/{upload_id}", { params: { path: { platform, upload_id: unfinished.id } } })
        .catch(() => undefined);
      setUnfinished(null);
    }
    up ??= adminData(
      await adminApi.POST("/admin/v1/platform/apps/{platform}/uploads", {
        params: { path: { platform } },
        body: { kind, name: file.name, size: file.size, sha256: sum },
      }),
    );
    setUnfinished({ id: up.upload_id, sha256: sum, name: file.name, size: file.size });
    let done = up.received.length;
    setProgress({ done, parts: up.parts, checking: false });
    for (let n = 1; n <= up.parts; n++) {
      if (up.received.includes(n)) continue;
      await putPart(platform, up.upload_id, n, file.slice((n - 1) * up.part_size, n * up.part_size));
      setProgress({ done: ++done, parts: up.parts, checking: false });
    }
    setProgress({ done, parts: up.parts, checking: true });
    const out = adminData(
      await adminApi.POST("/admin/v1/platform/apps/{platform}/uploads/{upload_id}/complete", {
        params: { path: { platform, upload_id: up.upload_id } },
        body: { reason },
      }),
    );
    setUnfinished(null);
    return out;
  };

  // An unfinished upload is checked against the server when the card opens
  // (review GF, A76 ④): gone (completed, dropped, expired), it is forgotten;
  // not reached, it stays remembered until the page opens again (A78 ②).
  const checked = useRef(false);
  useEffect(() => {
    if (checked.current || !unfinished) return;
    checked.current = true;
    void adminApi
      .GET("/admin/v1/platform/apps/{platform}/uploads/{upload_id}", { params: { path: { platform, upload_id: unfinished.id } } })
      .then((res) => {
        if (res.response.status === 404) {
          keepUnfinished(platform, kind, null);
          setUnfinishedState(null);
        }
      })
      .catch(() => undefined);
  }, [platform, kind, unfinished]);

  const abandon = async () => {
    if (!unfinished) return;
    const res = await adminApi.DELETE("/admin/v1/platform/apps/{platform}/uploads/{upload_id}", {
      params: { path: { platform, upload_id: unfinished.id } },
    });
    // Gone already (completed, expired) is as good as dropped.
    if (!res.response.ok && res.response.status !== 404) return setError(String(res.response.status));
    setUnfinished(null);
  };

  return (
    <div className="flex flex-col gap-2">
      <input
        ref={input}
        type="file"
        className="hidden"
        accept={ext}
        data-testid={`app-upload-file-${platform}-${kind}`}
        onChange={async (e) => {
          const f = e.target.files?.[0];
          e.target.value = "";
          if (!f) return;
          setError("");
          if (!f.name.toLowerCase().endsWith(ext)) return setError(t("admin.apps.wrongType", { ext }));
          if (f.size > max) return setError(t("admin.apps.tooBig", { max: sizeText(max) }));
          setHashing(0);
          try {
            setChosen({ file: f, sha256: await sha256File(f, setHashing) });
          } catch (err) {
            setError(err instanceof Error ? err.message : String(err));
          } finally {
            setHashing(null);
          }
        }}
      />
      <span className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          variant="secondary"
          icon={<FileUp size={14} />}
          disabled={hashing !== null}
          onClick={() => input.current?.click()}
          data-testid={`app-upload-${platform}-${kind}`}
        >
          {label}
        </Button>
        {hashing !== null && <span className="text-xs text-fg-3">{t("admin.apps.hashing", { pct: Math.floor(hashing * 100) })}</span>}
        {error && <span className="text-xs text-danger-strong">{error}</span>}
      </span>
      {unfinished && !chosen && (
        <span className="flex flex-wrap items-center gap-2 text-xs text-fg-3" data-testid={`app-unfinished-${platform}-${kind}`}>
          {t("admin.apps.unfinished", { name: unfinished.name, size: sizeText(unfinished.size) })}
          <Button size="sm" variant="ghost" onClick={() => void abandon()} data-testid={`app-abandon-${platform}-${kind}`}>
            {t("admin.apps.abandon")}
          </Button>
        </span>
      )}
      {chosen && (
        <DangerAction
          open
          onOpenChange={(o) => {
            if (!o) {
              setChosen(null);
              setProgress(null);
            }
          }}
          danger={false}
          title={t("admin.apps.uploadTitle", { name: chosen.file.name })}
          description={kind === "MOBILECONFIG" ? t("admin.apps.uploadMobileconfigHint") : t("admin.apps.uploadHint")}
          target={
            <span className="flex flex-col gap-0.5 font-mono text-xs">
              <span>
                {chosen.file.name} · {sizeText(chosen.file.size)}
              </span>
              <span className="break-all text-fg-3">{chosen.sha256}</span>
            </span>
          }
          confirmWord={platform.toLowerCase()}
          run={send}
          success={t("admin.apps.uploaded_ok")}
          invalidate={[appsKey, launchKey]}
          onDone={() => {
            setChosen(null);
            setProgress(null);
          }}
        >
          {progress && (
            <div className="flex flex-col gap-1" data-testid="app-upload-progress">
              <Progress value={progress.done} max={progress.parts} aria-label={t("admin.apps.progress", { done: progress.done, parts: progress.parts })} />
              <span className="text-xs text-fg-3">
                {progress.checking ? t("admin.apps.checking") : t("admin.apps.progress", { done: progress.done, parts: progress.parts })}
              </span>
            </div>
          )}
          {!progress && unfinished?.sha256 === chosen.sha256 && <p className="text-xs text-fg-3">{t("admin.apps.resume")}</p>}
        </DangerAction>
      )}
    </div>
  );
}

/** Files lists the files kept on the server for a platform, each deleted on its own. */
function Files({ app, edit }: { app: App; edit: boolean }) {
  const { t } = useTranslation();
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-sm font-semibold text-fg-1">
        {t("admin.apps.files")} <span className="font-normal text-fg-3">· {t("admin.apps.filesHint")}</span>
      </h3>
      {app.files.length === 0 ? (
        <p className="text-xs text-fg-3">{t("admin.apps.noFiles")}</p>
      ) : (
        <ul className="flex flex-col divide-y divide-line-1 rounded-2 border border-line-1">
          {app.files.map((f) => {
            const current = app.current?.file_id === f.file_id;
            const profile = app.mobileconfig?.file_id === f.file_id;
            return (
              <li key={f.file_id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-xs" data-testid="app-file-row">
                <span className="font-mono text-fg-1">{f.name}</span>
                {current && <Badge tone="success">{t("admin.apps.isCurrent")}</Badge>}
                {profile && <Badge tone="neutral">{t("admin.apps.isMobileconfig")}</Badge>}
                {f.version && <span className="text-fg-2">{f.version}{f.build ? ` (${f.build})` : ""}</span>}
                <span className="text-fg-3">{sizeText(f.size)}</span>
                <span className="text-fg-3">
                  <TimeText value={f.uploaded_at} /> · {f.uploaded_by}
                </span>
                {edit && (
                  <span className="ml-auto">
                    <DangerAction
                      trigger={(open) => (
                        <Button size="sm" variant="ghost" icon={<Trash2 size={14} />} onClick={open} data-testid={`app-delete-${f.file_id}`}>
                          {t("admin.apps.delete")}
                        </Button>
                      )}
                      title={t("admin.apps.deleteTitle", { name: f.name })}
                      description={current ? t("admin.apps.deleteCurrent") : profile ? t("admin.apps.deleteMobileconfig") : t("admin.apps.deleteOther")}
                      target={<span className="font-mono text-xs">{f.name}</span>}
                      confirmWord={f.file_id.slice(-4)}
                      run={async (reason) =>
                        adminData(
                          await adminApi.DELETE("/admin/v1/platform/apps/{platform}/files/{file_id}", {
                            params: { path: { platform: app.platform, file_id: f.file_id } },
                            body: { reason },
                          }),
                        )
                      }
                      success={t("admin.apps.deleted")}
                      invalidate={[appsKey, launchKey]}
                    />
                  </span>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
