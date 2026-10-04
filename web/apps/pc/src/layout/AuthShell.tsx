import { routes } from "@exchange/core";
import { useBrandText } from "@exchange/core/platform/index";
import { useTranslation } from "react-i18next";
import { Link, Outlet } from "react-router";
import { LearningBanner } from "./LearningBanner";
import { Logo } from "./Logo";

/** AuthShell: sign-in, sign-up and reset share one centred card beside a brand panel. */
export function AuthShell() {
  const { t } = useTranslation();
  const copyright = useBrandText((p) => p.footer.copyright);
  return (
    <div className="flex min-h-dvh min-w-[1024px] flex-col bg-bg-0">
      <LearningBanner />
      <div className="grid flex-1 grid-cols-[1fr_minmax(420px,520px)]">
        <aside className="relative hidden overflow-hidden border-r border-line-1 bg-bg-1 lg:block">
          <div className="absolute -left-24 top-1/4 size-[520px] animate-float rounded-full bg-brand-soft blur-3xl" />
          <div className="relative flex h-full flex-col justify-between p-12">
            <Link to={routes.home}>
              <Logo />
            </Link>
            <div className="max-w-md">
              <h1 className="text-2xl font-semibold text-fg-1">{t("pc.heroTitle")}</h1>
              <p className="mt-3 text-fg-2">{t("pc.heroSubtitle")}</p>
            </div>
            <p className="text-xs text-fg-3">{copyright || t("footer.copyright")}</p>
          </div>
        </aside>
        <main className="flex items-center justify-center p-8">
          <div className="w-full max-w-sm">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}
