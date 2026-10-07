import { routes } from "@exchange/core";
import { offeredApps, qrUrl, usePlatformApps } from "@exchange/core/platform/apps";
import { AppQrPanel } from "@exchange/ui/download/AppQrPanel";
import { ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

/**
 * DownloadQrs fills the top bar's download panel (design 2026-10-07, App
 * download page §4): a QR code for each app offered, and the way to the
 * download page; while none is offered (the entry follows the console's
 * switch alone, H6), that none is yet.
 */
export function DownloadQrs() {
  const { t } = useTranslation();
  const list = offeredApps(usePlatformApps().data);
  const page = `${globalThis.location?.origin ?? ""}${routes.download}`;
  return (
    <AppQrPanel
      title={t("pcDownload.menu.title")}
      codes={list.map(({ platform, app }) => {
        const caption = t(`pcDownload.platforms.${platform}`);
        return { key: platform, value: qrUrl(platform, app, page), caption, label: t("pcDownload.qrLabel", { platform: caption }) };
      })}
      empty={<p className="w-56 py-2 text-sm text-fg-3">{t("pcDownload.none")}</p>}
      footer={
        <Link
          to={routes.download}
          onClick={(e) => e.currentTarget.blur()}
          className="flex items-center justify-center gap-0.5 rounded-2 bg-bg-2 py-2 text-sm text-fg-1 transition-colors hover:bg-bg-3"
        >
          {t("pcDownload.menu.more")}
          <ChevronRight size={14} aria-hidden />
        </Link>
      }
    />
  );
}
