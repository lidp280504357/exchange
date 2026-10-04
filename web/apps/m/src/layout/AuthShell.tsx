import { routes } from "@exchange/core";
import { X } from "lucide-react";
import { Link, Outlet, useLocation } from "react-router";
import { useTranslation } from "react-i18next";
import { useScrollTop } from "../components/useScrollTop";
import { BrandMark, TestModeStrip } from "./Brand";

/** AuthShell: sign-in, sign-up and reset full screen, with a close button home (design §7.2). */
export function AuthShell() {
  const { t } = useTranslation();
  useScrollTop(useLocation().pathname);
  return (
    <div className="relative flex min-h-dvh flex-col overflow-hidden bg-bg-0 px-5 pb-[calc(24px+env(safe-area-inset-bottom))] pt-[env(safe-area-inset-top)]">
      <div className="pointer-events-none absolute -right-24 -top-24 size-72 rounded-full bg-brand-soft blur-3xl" />
      <div className="relative flex h-tap items-center justify-between">
        <BrandMark />
        <Link to={routes.home} aria-label={t("common.close")} className="grid size-tap place-items-center text-fg-2">
          <X size={22} />
        </Link>
      </div>
      <div className="relative -mx-5">
        <TestModeStrip />
      </div>
      <main className="relative flex flex-1 flex-col pt-6">
        <Outlet />
      </main>
    </div>
  );
}
