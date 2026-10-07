import { useQuery } from "@tanstack/react-query";
import { platformApi, unwrap } from "../api/client";
import type { components } from "../api/gen/platform";
import { formatDecimal } from "../format/number";

// The apps to download (design 2026-10-07, App download page §2.2, §4):
// each platform's link or uploaded file as the console set it, null while
// it is not offered. The sites show their download entries while one of
// the two is offered.

export type AppDownload = components["schemas"]["AppDownload"];
export type PlatformApps = components["schemas"]["PlatformApps"];
export type AppPlatform = "android" | "ios";

export const APP_PLATFORMS: readonly AppPlatform[] = ["android", "ios"];

export const appsKey = ["platform", "apps"] as const;

/** usePlatformApps reads the apps, again every minute (the console changes them without a build). */
export function usePlatformApps() {
  return useQuery({
    queryKey: appsKey,
    queryFn: () => unwrap(platformApi.GET("/v1/platform/apps")),
    staleTime: 60_000,
    refetchInterval: 60_000,
  });
}

/** useAppsOffered is whether any app is offered: the download entries show only then. */
export function useAppsOffered(): boolean {
  const apps = usePlatformApps().data;
  return Boolean(apps?.android || apps?.ios);
}

/** An offered app with its platform. */
export type OfferedApp = { platform: AppPlatform; app: AppDownload };

/** offeredApps lists the offered apps, `first` leading (a phone's own platform). */
export function offeredApps(apps: PlatformApps | undefined, first?: AppPlatform | null): OfferedApp[] {
  const all = APP_PLATFORMS.flatMap((platform) => {
    const app = apps?.[platform];
    return app ? [{ platform, app }] : [];
  });
  return first ? [...all.filter((a) => a.platform === first), ...all.filter((a) => a.platform !== first)] : all;
}

/**
 * devicePlatform reads a phone's platform from its user agent: iOS
 * (iPhone, iPod, iPad, or an iPad asking for the desktop site, which says
 * Macintosh but has a touch screen), Android, or null for anything else.
 */
export function devicePlatform(userAgent: string, touchPoints = 0): AppPlatform | null {
  if (/android/i.test(userAgent)) return "android";
  if (/iphone|ipad|ipod/i.test(userAgent) || (/macintosh/i.test(userAgent) && touchPoints > 1)) return "ios";
  return null;
}

/** parsePlatform reads ?platform= (the QR codes' links say which app they are for). */
export function parsePlatform(v: string | null | undefined): AppPlatform | null {
  return v === "android" || v === "ios" ? v : null;
}

/**
 * installUrl is where an app's button leads: an uploaded iOS app installs
 * over the air (itms-services, only from iOS), anything else opens its
 * link or downloads its file.
 */
export function installUrl(app: AppDownload): string {
  return app.install_url ?? app.url;
}

/**
 * qrUrl is what an app's QR code holds: a link as it is (a store opens
 * it); for an uploaded file, the download page on the phone (`page`, with
 * ?platform=), which installs it and says how to allow it — an
 * itms-services link does not work from a QR code, and an Android phone
 * needs the unknown-sources note.
 */
export function qrUrl(platform: AppPlatform, app: AppDownload, page: string): string {
  return app.mode === "LINK" ? app.url : `${page}?platform=${platform}`;
}

// Android API levels and the versions that brought them: an .apk's min_os
// is its minSdkVersion.
const ANDROID_VERSIONS: Record<string, string> = {
  "21": "5.0", "22": "5.1", "23": "6.0", "24": "7.0", "25": "7.1", "26": "8.0", "27": "8.1", "28": "9", "29": "10", "30": "11", "31": "12",
  "32": "12L", "33": "13", "34": "14", "35": "15", "36": "16",
};

/** androidVersion names the Android version of an API level ("24" → "7.0"); null when it is not one of these. */
export function androidVersion(level: string | null | undefined): string | null {
  return (level && ANDROID_VERSIONS[level.trim()]) || null;
}

/** fileSize renders a size in bytes: "48.2 MB", "860 KB". */
export function fileSize(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes) || bytes < 0) return "—";
  if (bytes < 1024) return `${bytes} B`;
  const kb = bytes / 1024;
  if (kb < 1024) return `${formatDecimal(String(Math.round(kb)))} KB`;
  return `${formatDecimal((kb / 1024).toFixed(1))} MB`;
}
