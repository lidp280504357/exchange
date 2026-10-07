import { errorText, routes, useSettings } from "@exchange/core";
import {
  androidVersion, devicePlatform, fileSize, installUrl, offeredApps, parsePlatform, usePlatformApps, type AppDownload, type AppPlatform,
} from "@exchange/core/platform/apps";
import { textOf } from "@exchange/core/platform/index";
import { Badge, Button, EmptyState, ErrorState, Skeleton, TimeText, cn, copyText, listItem, toast } from "@exchange/ui";
import { Apple, Bot, Copy } from "lucide-react";
import { motion } from "motion/react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";
import { usePageHeader } from "../../layout/header";

/**
 * Download on the phone (design 2026-10-07, App download page §4): the
 * phone's own platform first (or the one ?platform= names, as the PC
 * site's QR codes do) with its install button and, for a file that is not
 * from a store, how to install it, open; the other platform's after; "no
 * app yet" while none is offered.
 */
export default function Download() {
  const { t } = useTranslation();
  usePageHeader({ title: t("mDownload.header"), back: routes.me }, [t]);
  const apps = usePlatformApps();
  const [params] = useSearchParams();
  const nav = globalThis.navigator;
  const own = devicePlatform(nav?.userAgent ?? "", nav?.maxTouchPoints ?? 0);
  const list = offeredApps(apps.data, parsePlatform(params.get("platform")) ?? own);

  let body: ReactNode;
  if (apps.isPending) {
    body = <Skeleton className="h-80 rounded-3" />;
  } else if (apps.isError) {
    body = <ErrorState message={errorText(apps.error)} onRetry={() => void apps.refetch()} />;
  } else if (list.length === 0) {
    body = <EmptyState title={t("mDownload.none")} description={t("mDownload.noneHint")} />;
  } else {
    body = list.map(({ platform, app }, i) => <AppCard key={platform} platform={platform} app={app} own={platform === own} lead={i === 0} index={i} />);
  }

  return (
    <div className="flex flex-col gap-4 px-4 py-3" data-testid="download-page">
      <header className="px-1 pb-1 pt-2">
        <h1 className="text-xl font-semibold text-fg-1">{t("mDownload.title")}</h1>
        <p className="mt-1 text-sm leading-relaxed text-fg-3">{t("mDownload.subtitle")}</p>
      </header>
      {body}
    </div>
  );
}

type Kind = "androidFile" | "androidLink" | "iosOta" | "iosStore";

function kindOf(platform: AppPlatform, app: AppDownload): Kind {
  if (platform === "android") return app.mode === "FILE" ? "androidFile" : "androidLink";
  return app.ios_install === "OTA" ? "iosOta" : "iosStore";
}

// AppCard is one app: the leading one (the phone's own, or the one asked
// for) with a primary button and its install steps open.
function AppCard({ platform, app, own, lead, index }: { platform: AppPlatform; app: AppDownload; own: boolean; lead: boolean; index: number }) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const kind = kindOf(platform, app);
  const Icon = platform === "ios" ? Apple : Bot;
  const notes = textOf(app.notes, locale);
  const minOs = !app.min_os
    ? null
    : platform === "ios"
      ? t("mDownload.minIos", { version: app.min_os })
      : androidVersion(app.min_os)
        ? t("mDownload.minAndroid", { version: androidVersion(app.min_os) })
        : t("mDownload.minAndroidApi", { level: app.min_os });
  const label = { androidFile: t("mDownload.downloadApk"), androidLink: t("mDownload.open"), iosStore: t("mDownload.appStore"), iosOta: t("mDownload.install") }[kind];
  const steps = kind === "androidFile" ? "helpAndroid" : kind === "iosOta" ? "helpIos" : null;
  const copy = async () => {
    if (app.sha256 && (await copyText(app.sha256))) toast.success(t("mDownload.sha256Copied"));
  };

  return (
    <motion.section
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={index}
      aria-labelledby={`app-${platform}`}
      data-testid={`app-${platform}`}
      className="flex flex-col gap-4 rounded-3 bg-bg-1 p-4"
    >
      <div className="flex items-center gap-3">
        <span aria-hidden className="grid size-11 shrink-0 place-items-center rounded-3 bg-bg-2 text-fg-1">
          <Icon size={24} />
        </span>
        <div className="min-w-0 flex-1">
          <h2 id={`app-${platform}`} className="flex items-center gap-2 text-md font-semibold text-fg-1">
            {t(`mDownload.platforms.${platform}`)}
            {own && <Badge tone="brand">{t("mDownload.thisPhone")}</Badge>}
          </h2>
          <p className="text-sm text-fg-3">{t(`mDownload.kinds.${kind}`)}</p>
        </div>
      </div>
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-sm">
        {app.version && (
          <Fact label={t("mDownload.version")}>
            {app.version}
            {app.build ? <span className="text-fg-3"> ({app.build})</span> : null}
          </Fact>
        )}
        <Fact label={t("mDownload.updated")}>
          <TimeText value={app.updated_at} format="date" />
        </Fact>
        {app.size !== null && <Fact label={t("mDownload.size")}>{fileSize(app.size)}</Fact>}
        {minOs && <Fact label={t("mDownload.minOs")}>{minOs}</Fact>}
        {app.sha256 && (
          <Fact label="SHA-256">
            <button
              type="button"
              onClick={() => void copy()}
              className="-my-1 inline-flex min-h-tap items-start gap-1 text-left font-mono text-xs leading-5 text-fg-2 active:text-fg-1"
            >
              <span className="break-all">{app.sha256}</span>
              <Copy size={13} aria-hidden className="mt-1 shrink-0" />
            </button>
          </Fact>
        )}
      </dl>
      {notes && (
        <div>
          <h3 className="text-sm font-medium text-fg-1">{t("mDownload.notes")}</h3>
          <p className="mt-1 whitespace-pre-line text-sm leading-relaxed text-fg-2">{notes}</p>
        </div>
      )}
      <Button asChild size="lg" block variant={lead ? "primary" : "secondary"}>
        <a href={installUrl(app)} {...(app.mode === "LINK" ? { target: "_blank", rel: "noopener noreferrer" } : {})}>
          {label}
        </a>
      </Button>
      {app.mobileconfig_url && (
        <a href={app.mobileconfig_url} className="-mt-1 self-center py-2 text-sm text-brand">
          {t("mDownload.mobileconfig")}
        </a>
      )}
      {steps && (
        <details open={lead} className="rounded-2 bg-bg-2 px-4 py-3 text-sm">
          <summary className="cursor-pointer font-medium text-fg-1">{t("mDownload.help")}</summary>
          <ol className={cn("mt-2 list-decimal space-y-1 pl-5 leading-relaxed text-fg-2")}>
            {(["s1", "s2", "s3"] as const).map((s) => (
              <li key={s}>{t(`mDownload.${steps}.${s}`)}</li>
            ))}
          </ol>
        </details>
      )}
    </motion.section>
  );
}

function Fact({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <>
      <dt className="text-fg-3">{label}</dt>
      <dd className="min-w-0 text-right text-fg-1 tabular-nums">{children}</dd>
    </>
  );
}
