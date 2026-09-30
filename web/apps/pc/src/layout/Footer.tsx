import { routes, setLocale, switchSite, useSettings } from "@exchange/core";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { Logo } from "./Logo";

/** Footer: product and support links, the risk note, the switch to the mobile site. */
export function Footer() {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  return (
    <footer className="border-t border-line-1 bg-bg-1">
      <div className="mx-auto grid max-w-[1440px] grid-cols-[2fr_1fr_1fr_1fr] gap-8 px-6 py-10 text-sm">
        <div className="flex flex-col gap-3">
          <Logo />
          <p className="max-w-sm text-fg-3">{t("footer.risk")}</p>
        </div>
        <Column title={t("footer.products")}>
          <Link to={routes.markets}>{t("nav.markets")}</Link>
          <Link to={routes.trade("BTC-USDT")}>{t("nav.spot")}</Link>
          <Link to={routes.futures("BTC-USDT-PERP")}>{t("nav.futures")}</Link>
        </Column>
        <Column title={t("footer.support")}>
          <Link to={routes.help}>{t("nav.help")}</Link>
          <Link to={routes.announcements}>{t("nav.announcements")}</Link>
          <a href="/docs/">API</a>
        </Column>
        <Column title={t("footer.about")}>
          <button type="button" className="text-left" onClick={() => switchSite("m")}>
            {t("footer.toMobile")}
          </button>
          <button type="button" className="text-left" onClick={() => setLocale(locale === "zh-CN" ? "en" : "zh-CN")}>
            {locale === "zh-CN" ? "English" : "中文"}
          </button>
        </Column>
      </div>
      <div className="border-t border-line-1 py-4 text-center text-xs text-fg-3">{t("footer.copyright")}</div>
    </footer>
  );
}

function Column({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2 [&_a]:text-fg-2 [&_a:hover]:text-fg-1 [&_button]:text-fg-2 [&_button:hover]:text-fg-1">
      <div className="mb-1 font-medium text-fg-1">{title}</div>
      {children}
    </div>
  );
}
