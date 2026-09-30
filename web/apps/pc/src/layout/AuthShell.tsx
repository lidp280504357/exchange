import { routes } from "@exchange/core";
import { useTranslation } from "react-i18next";
import { Link, Outlet } from "react-router";
import { Logo } from "./Logo";

/** AuthShell: sign-in, sign-up and reset share one centred card beside a brand panel. */
export function AuthShell() {
  const { t } = useTranslation();
  return (
    <div className="grid min-h-dvh min-w-[1024px] grid-cols-[1fr_minmax(420px,520px)] bg-bg-0">
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
          <p className="text-xs text-fg-3">{t("footer.copyright")}</p>
        </div>
      </aside>
      <main className="flex items-center justify-center p-8">
        <div className="w-full max-w-sm">
          <Outlet />
        </div>
      </main>
    </div>
  );
}
