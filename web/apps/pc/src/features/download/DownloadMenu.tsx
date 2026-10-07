import { routes } from "@exchange/core";
import { useAppsOffered } from "@exchange/core/platform/apps";
import { cn } from "@exchange/ui";
import { Download } from "lucide-react";
import { Suspense, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { DownloadQrs } from "./lazyQrs";

/**
 * DownloadMenu is the top bar's download entry (design 2026-10-07, App
 * download page §4, as Binance's): a button to the download page whose
 * panel, on hover or keyboard focus (CSS only, as the other menus), shows
 * a QR code for each app. Hidden while no app is offered. The panel's
 * chunk loads when the pointer or the focus first reaches the button.
 */
export function DownloadMenu() {
  const { t } = useTranslation();
  const offered = useAppsOffered();
  const [armed, setArmed] = useState(false);
  if (!offered) return null;
  const arm = () => {
    if (armed) return;
    setArmed(true);
    void DownloadQrs.preload();
  };
  return (
    <div className="group relative flex h-full items-center" onPointerEnter={arm} onFocus={arm}>
      <Link
        to={routes.download}
        aria-label={t("nav.downloadApp")}
        data-testid="download-menu"
        onClick={(e) => e.currentTarget.blur()}
        className="grid size-9 place-items-center rounded-2 text-fg-2 transition-colors hover:bg-bg-2 hover:text-fg-1"
      >
        <Download size={18} />
      </Link>
      <div
        className={cn(
          "invisible absolute right-0 top-full z-[var(--z-dropdown)] translate-y-1 rounded-2 border border-line-1 bg-bg-1 p-4 opacity-0 shadow-pop transition-[opacity,transform] duration-[var(--t-fast)]",
          "group-has-[:focus-visible]:visible group-has-[:focus-visible]:translate-y-0 group-has-[:focus-visible]:opacity-100 group-hover:visible group-hover:translate-y-0 group-hover:opacity-100",
        )}
      >
        {armed && (
          <Suspense fallback={<div className="h-52 w-60" />}>
            <DownloadQrs />
          </Suspense>
        )}
      </div>
    </div>
  );
}
