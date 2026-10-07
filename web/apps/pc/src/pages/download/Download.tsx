import { errorText, routes, useSettings } from "@exchange/core";
import {
  androidVersion, fileSize, installUrl, offeredApps, parsePlatform, qrUrl, usePlatformApps, type AppDownload, type AppPlatform,
} from "@exchange/core/platform/apps";
import { textOf } from "@exchange/core/platform/index";
import { Button, CopyButton, EmptyState, ErrorState, QrCode, Skeleton, TimeText, cn, listItem } from "@exchange/ui";
import { Apple, Bot, ExternalLink } from "lucide-react";
import { motion } from "motion/react";
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
          <AppCard key={platform} platform={platform} app={app} page={page} index={i} />
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

type Kind = "androidFile" | "androidLink" | "iosOta" | "iosStore";

function kindOf(platform: AppPlatform, app: AppDownload): Kind {
  if (platform === "android") return app.mode === "FILE" ? "androidFile" : "androidLink";
  return app.ios_install === "OTA" ? "iosOta" : "iosStore";
}

// AppCard is one app: what it is, its QR code and facts, its notes, the
// button (an OTA install only works on the phone: its QR code says so) and
// how to install a file.
function AppCard({ platform, app, page, index }: { platform: AppPlatform; app: AppDownload; page: string; index: number }) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const kind = kindOf(platform, app);
  const Icon = platform === "ios" ? Apple : Bot;
  const notes = textOf(app.notes, locale);
  const minOs = !app.min_os
    ? null
    : platform === "ios"
      ? t("pcDownload.minIos", { version: app.min_os })
      : androidVersion(app.min_os)
        ? t("pcDownload.minAndroid", { version: androidVersion(app.min_os) })
        : t("pcDownload.minAndroidApi", { level: app.min_os });
  const button = { androidFile: t("pcDownload.downloadApk"), androidLink: t("pcDownload.open"), iosStore: t("pcDownload.appStore"), iosOta: null }[kind];
  const steps = kind === "androidFile" ? "helpAndroid" : kind === "iosOta" ? "helpIos" : null;

  return (
    <motion.section
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={index}
      aria-labelledby={`app-${platform}`}
      data-testid={`app-${platform}`}
      className="flex flex-col gap-5 rounded-3 border border-line-1 bg-bg-1 p-6"
    >
      <div className="flex items-center gap-3">
        <span aria-hidden className="grid size-12 shrink-0 place-items-center rounded-3 bg-bg-2 text-fg-1">
          <Icon size={26} />
        </span>
        <div className="min-w-0">
          <h2 id={`app-${platform}`} className="text-lg font-semibold text-fg-1">
            {t(`pcDownload.platforms.${platform}`)}
          </h2>
          <p className="text-sm text-fg-3">{t(`pcDownload.kinds.${kind}`)}</p>
        </div>
      </div>
      <div className="flex gap-6">
        <div className="flex w-40 shrink-0 flex-col items-center gap-2">
          <QrCode value={qrUrl(platform, app, page)} size={132} label={t("pcDownload.scan")} />
          <span className="text-center text-xs leading-relaxed text-fg-3">{kind === "iosOta" ? t("pcDownload.scanIos") : t("pcDownload.scan")}</span>
        </div>
        <dl className="grid min-w-0 flex-1 content-start grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm">
          {app.version && (
            <Fact label={t("pcDownload.version")}>
              {app.version}
              {app.build ? <span className="text-fg-3"> ({app.build})</span> : null}
            </Fact>
          )}
          <Fact label={t("pcDownload.updated")}>
            <TimeText value={app.updated_at} format="date" />
          </Fact>
          {app.size !== null && <Fact label={t("pcDownload.size")}>{fileSize(app.size)}</Fact>}
          {minOs && <Fact label={t("pcDownload.minOs")}>{minOs}</Fact>}
          {app.sha256 && (
            <Fact label="SHA-256">
              <span className="flex items-start gap-1">
                <span className="break-all font-mono text-xs leading-5 text-fg-2">{app.sha256}</span>
                <CopyButton value={app.sha256} size={12} />
              </span>
            </Fact>
          )}
        </dl>
      </div>
      {notes && (
        <div>
          <h3 className="text-sm font-medium text-fg-1">{t("pcDownload.notes")}</h3>
          <p className="mt-1 whitespace-pre-line text-sm leading-relaxed text-fg-2">{notes}</p>
        </div>
      )}
      <div className="mt-auto flex flex-wrap items-center gap-4">
        {button && (
          <Button asChild size="lg" icon={kind === "androidFile" ? undefined : <ExternalLink size={16} />}>
            <a href={installUrl(app)} {...(app.mode === "LINK" ? { target: "_blank", rel: "noopener noreferrer" } : {})}>
              {button}
            </a>
          </Button>
        )}
        {app.mobileconfig_url && (
          <a href={app.mobileconfig_url} className="text-sm text-brand hover:underline">
            {t("pcDownload.mobileconfig")}
          </a>
        )}
      </div>
      {steps && (
        <details className="rounded-2 bg-bg-2 px-4 py-3 text-sm">
          <summary className="cursor-pointer font-medium text-fg-1">{t("pcDownload.help")}</summary>
          <ol className="mt-2 list-decimal space-y-1 pl-5 leading-relaxed text-fg-2">
            {(["s1", "s2", "s3"] as const).map((s) => (
              <li key={s}>{t(`pcDownload.${steps}.${s}`)}</li>
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
      <dd className="min-w-0 text-fg-1 tabular-nums">{children}</dd>
    </>
  );
}
