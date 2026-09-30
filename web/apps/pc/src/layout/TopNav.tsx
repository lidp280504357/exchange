import { DEFAULT_CONTRACT, DEFAULT_SYMBOL, routes, selectSignedIn, setLocale, signOut, useSession, useSettings } from "@exchange/core";
import { Button, cn } from "@exchange/ui";
import { Bell, ChevronDown, Globe, Search, UserRound } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, NavLink } from "react-router";
import { Logo } from "./Logo";

/**
 * TopNav: the 56 px bar fixed on every page (design §6.1) — markets, spot,
 * futures, assets and announcements on the left; search, notifications,
 * the account and the language on the right.
 */
export function TopNav() {
  const { t } = useTranslation();
  const signedIn = useSession(selectSignedIn);
  return (
    <header className="sticky top-0 z-[var(--z-sticky)] h-14 border-b border-line-1 bg-bg-0/95 backdrop-blur">
      <div className="mx-auto flex h-full max-w-[1920px] items-center gap-6 px-6">
        <Link to={routes.home} className="flex items-center gap-2" aria-label="Astras">
          <Logo />
        </Link>
        <nav className="flex h-full items-center gap-1 text-base">
          <Item to={routes.markets}>{t("nav.markets")}</Item>
          <Menu label={t("nav.spot")} to={routes.trade(DEFAULT_SYMBOL)}>
            <MenuLink to={routes.trade("BTC-USDT")}>BTC/USDT</MenuLink>
            <MenuLink to={routes.trade("ETH-USDT")}>ETH/USDT</MenuLink>
            <MenuLink to={routes.markets}>{t("nav.markets")} →</MenuLink>
          </Menu>
          <Menu label={t("nav.futures")} to={routes.futures(DEFAULT_CONTRACT)}>
            <MenuLink to={routes.futures("BTC-USDT-PERP")}>BTCUSDT {t("market.futures")}</MenuLink>
            <MenuLink to={routes.futures("ETH-USDT-PERP")}>ETHUSDT {t("market.futures")}</MenuLink>
          </Menu>
          {signedIn && (
            <Menu label={t("nav.assets")} to={routes.assets}>
              <MenuLink to={routes.assets}>{t("nav.overview")}</MenuLink>
              <MenuLink to={routes.deposit}>{t("nav.deposit")}</MenuLink>
              <MenuLink to={routes.withdraw}>{t("nav.withdraw")}</MenuLink>
              <MenuLink to={routes.transfer}>{t("nav.transfer")}</MenuLink>
              <MenuLink to={routes.history}>{t("nav.history")}</MenuLink>
            </Menu>
          )}
          <Item to={routes.announcements}>{t("nav.announcements")}</Item>
        </nav>
        <div className="ml-auto flex items-center gap-2">
          <button
            type="button"
            className="flex h-9 w-56 items-center gap-2 rounded-2 bg-bg-2 px-3 text-sm text-fg-3 transition-colors hover:text-fg-2"
          >
            <Search size={16} />
            <span className="flex-1 text-left">{t("nav.search")}</span>
            <kbd className="rounded-1 border border-line-2 px-1.5 text-xs">⌘K</kbd>
          </button>
          {signedIn ? (
            <>
              <IconLink to={routes.notifications} label={t("nav.notifications")}>
                <Bell size={18} />
              </IconLink>
              <Menu label={<UserRound size={18} />} to={routes.security} align="right">
                <MenuLink to={routes.security}>{t("nav.security")}</MenuLink>
                <MenuLink to={routes.sessions}>{t("nav.sessions")}</MenuLink>
                <MenuLink to={routes.settings}>{t("nav.settings")}</MenuLink>
                <button type="button" onClick={() => void signOut()} className="w-full px-4 py-2 text-left text-sm text-fg-2 hover:bg-bg-2 hover:text-fg-1">
                  {t("nav.logout")}
                </button>
              </Menu>
            </>
          ) : (
            <>
              <Button asChild variant="ghost" size="sm">
                <Link to={routes.login}>{t("nav.login")}</Link>
              </Button>
              <Button asChild size="sm">
                <Link to={routes.register}>{t("nav.register")}</Link>
              </Button>
            </>
          )}
          <LanguageToggle />
        </div>
      </div>
    </header>
  );
}

function Item({ to, children }: { to: string; children: ReactNode }) {
  return (
    <NavLink
      to={to}
      className={({ isActive }) =>
        cn("flex h-9 items-center rounded-2 px-3 transition-colors hover:text-fg-1", isActive ? "text-fg-1" : "text-fg-2")
      }
    >
      {children}
    </NavLink>
  );
}

// Menu opens on hover and on keyboard focus (CSS only, no timers).
function Menu({ label, to, children, align = "left" }: { label: ReactNode; to: string; children: ReactNode; align?: "left" | "right" }) {
  return (
    <div className="group relative flex h-full items-center">
      <NavLink
        to={to}
        className={({ isActive }) =>
          cn("flex h-9 items-center gap-1 rounded-2 px-3 transition-colors hover:text-fg-1", isActive ? "text-fg-1" : "text-fg-2")
        }
      >
        {label}
        <ChevronDown size={14} className="transition-transform duration-[var(--t-fast)] group-hover:rotate-180" />
      </NavLink>
      <div
        className={cn(
          "invisible absolute top-full z-[var(--z-dropdown)] min-w-44 translate-y-1 rounded-2 border border-line-1 bg-bg-1 py-1 opacity-0 shadow-pop transition-[opacity,transform] duration-[var(--t-fast)]",
          "group-focus-within:visible group-focus-within:translate-y-0 group-focus-within:opacity-100 group-hover:visible group-hover:translate-y-0 group-hover:opacity-100",
          align === "right" ? "right-0" : "left-0",
        )}
      >
        {children}
      </div>
    </div>
  );
}

function MenuLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Link to={to} className="block px-4 py-2 text-sm text-fg-2 transition-colors hover:bg-bg-2 hover:text-fg-1">
      {children}
    </Link>
  );
}

function IconLink({ to, label, children }: { to: string; label: string; children: ReactNode }) {
  return (
    <Link to={to} aria-label={label} className="grid size-9 place-items-center rounded-2 text-fg-2 transition-colors hover:bg-bg-2 hover:text-fg-1">
      {children}
    </Link>
  );
}

function LanguageToggle() {
  const locale = useSettings((s) => s.locale);
  const next = locale === "zh-CN" ? "en" : "zh-CN";
  return (
    <button
      type="button"
      onClick={() => setLocale(next)}
      aria-label="Language"
      className="flex h-9 items-center gap-1 rounded-2 px-2 text-sm text-fg-2 transition-colors hover:bg-bg-2 hover:text-fg-1"
    >
      <Globe size={16} />
      {locale === "zh-CN" ? "中文" : "EN"}
    </button>
  );
}
