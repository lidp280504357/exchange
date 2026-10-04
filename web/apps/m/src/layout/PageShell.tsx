import { routes } from "@exchange/core";
import { ChevronLeft } from "lucide-react";
import { Outlet, useLocation, useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import { useScrollTop } from "../components/useScrollTop";
import { LearningStrip } from "./Brand";
import { useHeader } from "./header";
import { StatusStrip } from "./StatusStrip";

/**
 * PageShell holds a sub page (deposit, security, an article): a 44 px bar
 * with the back button and the page's title, no tab bar. Back returns to
 * the previous page, or to the page's parent after a direct visit.
 */
export function PageShell() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const header = useHeader();
  useScrollTop(location.pathname);
  const back = () => {
    // A direct visit has no history entry of this site to go back to.
    if (location.key !== "default") navigate(-1);
    else navigate(header?.back ?? routes.home, { replace: true });
  };
  return (
    <div className="flex min-h-dvh flex-col bg-bg-0 pb-[env(safe-area-inset-bottom)]">
      <header className="sticky top-0 z-[var(--z-sticky)] bg-bg-0/95 pt-[env(safe-area-inset-top)] backdrop-blur">
        <div className="grid h-tap grid-cols-[44px_1fr_auto] items-center px-1">
          <button type="button" onClick={back} aria-label={t("common.back")} className="grid size-tap place-items-center text-fg-1">
            <ChevronLeft size={22} />
          </button>
          <div className="min-w-0 truncate text-center text-md font-semibold text-fg-1">{header?.title}</div>
          <div className="flex min-w-tap items-center justify-end gap-1 pr-1">{header?.right}</div>
        </div>
        <LearningStrip />
        <StatusStrip />
      </header>
      <main key={location.pathname} className="flex-1 animate-fade-up">
        <Outlet />
      </main>
    </div>
  );
}
