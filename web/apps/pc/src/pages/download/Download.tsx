import { errorText, routes, useSettings } from "@exchange/core";
import {
  appHref, appKind, devicePlatform, installUrl, minOsText, offeredApps, parsePlatform, qrUrl, usePlatformApps, type AppDownload, type AppPlatform,
} from "@exchange/core/platform/apps";
import { textOf } from "@exchange/core/platform/index";
import { Button, EmptyState, ErrorState, QrCode, Skeleton, cn } from "@exchange/ui";
import { AppCard } from "@exchange/ui/download/AppCard";
import { ExternalLink } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";
import { usePageTitle } from "../markets/hooks";

/**
 * Download (design 2026-10-07, App download page §4): a card for each app
 * the console offers — its QR code for a phone, version, date, size, the
 * system it needs, the SHA-256 to copy, the version notes, its button and
 * how to install a file that is not from a store; "no app yet" while none
 * is offered. ?platform= (the QR codes' own links) puts that app first.
 */
export default function Download() {
  const { t } = useTranslation();
  const apps = usePlatformApps();
  const [params] = useSearchParams();
  usePageTitle(t("nav.downloadApp"));
  const list = offeredApps(apps.data, parsePlatform(params.get("platform")));
  const page = `${globalThis.location?.origin ?? ""}${routes.download}`;

  let body: ReactNode;
  if (apps.isPending) {
    body = (
      <div className="grid gap-6 md:grid-cols-2">
        {[0, 1].map((i) => (
          <Skeleton key={i} className="h-96 rounded-3" />
        ))}
      </div>
    );
  } else if (apps.isError) {
    body = <ErrorState message={errorText(apps.error)} onRetry={() => void apps.refetch()} />;
  } else if (list.length === 0) {
    body = <EmptyState title={t("pcDownload.none")} description={t("pcDownload.noneHint")} />;
  } else {
    body = (
      <div className={cn("grid gap-6", list.length > 1 ? "md:grid-cols-2" : "mx-auto max-w-xl")}>
        {list.map(({ platform, app }, i) => (
          <Card key={platform} platform={platform} app={app} page={page} index={i} />
        ))}
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-[1200px] px-6 py-12" data-testid="download-page">
      <header className="mb-10 text-center">
        <h1 className="text-3xl font-semibold text-fg-1">{t("pcDownload.title")}</h1>
        <p className="mt-3 text-base text-fg-3">{t("pcDownload.subtitle")}</p>
      </header>
      {body}
    </div>
  );
}

// Card is one app on the PC: its QR code beside the facts. An OTA install
// only works on an iPhone or iPad, so its QR code says so and it has a
// button only on an iPad that asked for the desktop site (review GN, F12).
function Card({ platform, app, page, index }: { platform: AppPlatform; app: AppDownload; page: string; index: number }) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const kind = appKind(platform, app);
  const nav = globalThis.navigator;
  const onIos = devicePlatform(nav?.userAgent ?? "", nav?.maxTouchPoints ?? 0) === "ios";
  const href = installUrl(app);
  const mobileconfig = appHref(app.mobileconfig_url);
  const label = { androidFile: t("pcDownload.downloadApk"), androidLink: t("pcDownload.open"), iosStore: t("pcDownload.appStore"), iosOta: onIos ? t("pcDownload.install") : null }[kind];
  const button = href ? label : null;
  const name = t(`pcDownload.platforms.${platform}`);
  const steps = kind === "androidFile" ? "helpAndroid" : kind === "iosOta" ? "helpIos" : null;
  return (
    <AppCard
      platform={platform}
      app={app}
      index={index}
      labels={{
        platform: name,
        kind: t(`pcDownload.kinds.${kind}`),
        version: t("pcDownload.version"),
        updated: t("pcDownload.updated"),
        size: t("pcDownload.size"),
        minOs: t("pcDownload.minOs"),
        notes: t("pcDownload.notes"),
        help: t("pcDownload.help"),
        copy: t("pcDownload.copySha"),
      }}
      minOs={minOsText(platform, app.min_os, (key, vars) => t(`pcDownload.${key}`, vars))}
      notes={textOf(app.notes, locale)}
      qr={
        <>
          <QrCode value={qrUrl(platform, app, page)} size={132} label={t("pcDownload.qrLabel", { platform: name })} />
          <span className="text-center text-xs leading-relaxed text-fg-3">{kind === "iosOta" ? t("pcDownload.scanIos") : t("pcDownload.scan")}</span>
        </>
      }
      actions={
        button || mobileconfig ? (
          <>
            {button && (
              <Button asChild size="lg" icon={kind === "androidLink" || kind === "iosStore" ? <ExternalLink size={16} /> : undefined}>
                <a href={href} {...(app.mode === "LINK" ? { target: "_blank", rel: "noopener noreferrer" } : {})}>
                  {button}
                </a>
              </Button>
            )}
            {mobileconfig && (
              <a href={mobileconfig} className="text-sm text-brand hover:underline">
                {t("pcDownload.mobileconfig")}
              </a>
            )}
          </>
        ) : undefined
      }
      steps={steps ? (["s1", "s2", "s3"] as const).map((s) => t(`pcDownload.${steps}.${s}`)) : null}
    />
  );
}
