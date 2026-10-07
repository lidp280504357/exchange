import { errorText, routes, useSettings } from "@exchange/core";
import {
  appKind, devicePlatform, installUrl, minOsText, offeredApps, parsePlatform, usePlatformApps, type AppDownload, type AppPlatform,
} from "@exchange/core/platform/apps";
import { textOf } from "@exchange/core/platform/index";
import { Button, EmptyState, ErrorState, Skeleton } from "@exchange/ui";
import { AppCard } from "@exchange/ui/download/AppCard";
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
    body = list.map(({ platform, app }, i) => <Card key={platform} platform={platform} app={app} own={platform === own} lead={i === 0} index={i} />);
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

// Card is one app on the phone: the leading one (the phone's own, or the
// one asked for) with a primary button and its install steps open.
function Card({ platform, app, own, lead, index }: { platform: AppPlatform; app: AppDownload; own: boolean; lead: boolean; index: number }) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const kind = appKind(platform, app);
  const label = { androidFile: t("mDownload.downloadApk"), androidLink: t("mDownload.open"), iosStore: t("mDownload.appStore"), iosOta: t("mDownload.install") }[kind];
  const steps = kind === "androidFile" ? "helpAndroid" : kind === "iosOta" ? "helpIos" : null;
  return (
    <AppCard
      platform={platform}
      app={app}
      index={index}
      layout="narrow"
      labels={{
        platform: t(`mDownload.platforms.${platform}`),
        kind: t(`mDownload.kinds.${kind}`),
        version: t("mDownload.version"),
        updated: t("mDownload.updated"),
        size: t("mDownload.size"),
        minOs: t("mDownload.minOs"),
        notes: t("mDownload.notes"),
        help: t("mDownload.help"),
        badge: own ? t("mDownload.thisPhone") : undefined,
      }}
      minOs={minOsText(platform, app.min_os, (key, vars) => t(`mDownload.${key}`, vars))}
      notes={textOf(app.notes, locale)}
      actions={
        <>
          <Button asChild size="lg" block variant={lead ? "primary" : "secondary"}>
            <a href={installUrl(app)} {...(app.mode === "LINK" ? { target: "_blank", rel: "noopener noreferrer" } : {})}>
              {label}
            </a>
          </Button>
          {app.mobileconfig_url && (
            <a href={app.mobileconfig_url} className="self-center py-2 text-sm text-brand">
              {t("mDownload.mobileconfig")}
            </a>
          )}
        </>
      }
      steps={steps ? (["s1", "s2", "s3"] as const).map((s) => t(`mDownload.${steps}.${s}`)) : null}
      stepsOpen={lead}
    />
  );
}
