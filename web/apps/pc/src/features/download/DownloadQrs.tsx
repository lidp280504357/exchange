import { routes } from "@exchange/core";
import { offeredApps, qrUrl, usePlatformApps } from "@exchange/core/platform/apps";
import { QrCode } from "@exchange/ui";
import { ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

/**
 * DownloadQrs is the top bar's download panel (design 2026-10-07, App
 * download page §4, as Binance's top bar): a QR code for each app offered,
 * and the way to the download page.
 */
export function DownloadQrs() {
  const { t } = useTranslation();
  const list = offeredApps(usePlatformApps().data);
  const page = `${globalThis.location?.origin ?? ""}${routes.download}`;
  return (
    <div className="flex flex-col gap-3" data-testid="download-qrs">
      <div className="text-sm font-medium text-fg-1">{t("pcDownload.menu.title")}</div>
      <div className="flex gap-4">
        {list.map(({ platform, app }) => (
          <figure key={platform} className="flex flex-col items-center gap-1.5">
            <QrCode value={qrUrl(platform, app, page)} size={112} label={t(`pcDownload.platforms.${platform}`)} />
            <figcaption className="text-xs text-fg-2">{t(`pcDownload.platforms.${platform}`)}</figcaption>
          </figure>
        ))}
      </div>
      <Link
        to={routes.download}
        onClick={(e) => e.currentTarget.blur()}
        className="flex items-center justify-center gap-0.5 rounded-2 bg-bg-2 py-2 text-sm text-fg-1 transition-colors hover:bg-bg-3"
      >
        {t("pcDownload.menu.more")}
        <ChevronRight size={14} aria-hidden />
      </Link>
    </div>
  );
}
