import {
  DEFAULT_CONTRACT, DEFAULT_SYMBOL, isContract, isInverse, LOCALE_NAMES, LOCALES, routes, selectSignedIn, setLocale, signOut, useContracts, useSession,
  useSettings, useTerminalPrefs, type Locale,
} from "@exchange/core";
import { useBranding } from "@exchange/core/platform/index";
import { useUnreadNotifications } from "@exchange/core/user/notifications";
import { Button, cn } from "@exchange/ui";
import { Bell, Check, ChevronDown, Globe, UserRound } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, NavLink } from "react-router";
import { Logo } from "./Logo";
import { SearchPalette } from "./SearchPalette";

// How many contracts the futures menu names in each group.
const FUTURES_MENU_SIZE = 5;

/**
 * TopNav: the bar fixed on every page (design §6.1), TOP_NAV_HEIGHT tall — markets, spot,
 * futures, assets and announcements on the left; search, notifications,
 * the account and the language on the right.
 */
/** The top bar's height (its h-14): what sticky headers of the pages stick under. */
export const TOP_NAV_HEIGHT = "3.5rem";

export function TopNav() {
  const { t } = useTranslation();
  const signedIn = useSession(selectSignedIn);
  const recent = useTerminalPrefs((s) => s.recent);
  const recentSpot = recent.filter((s) => !isContract(s)).slice(0, 5);
  const recentFutures = recent.filter(isContract);
  const contracts = useContracts();
  // The futures menu names a few contracts in two groups, USDT-margined and
  // coin-margined (design 2026-10-06 §2.6): the ones visited lately first,
  // then those trading in the list's order; each group leads to the
  // markets' futures tab for the rest (the list follows Binance's, §3.4:
  // some 100 contracts).
  const listed = contracts.data?.contracts ?? [];
  const bySymbol = new Map(listed.map((c) => [c.symbol, c]));
  const menuOf = (coin: boolean) =>
    [
      ...new Set([
        ...recentFutures.filter((s) => bySymbol.has(s) && isInverse(bySymbol.get(s)) === coin),
        ...listed.filter((c) => c.status === "TRADING" && isInverse(c) === coin).map((c) => c.symbol),
      ]),
    ].slice(0, FUTURES_MENU_SIZE);
  const futuresGroups = [
    { key: "usdt", label: t("pc.usdtMargined"), symbols: menuOf(false), all: `${routes.markets}?cat=futures` },
    { key: "coin", label: t("pc.coinMargined"), symbols: menuOf(true), all: `${routes.markets}?cat=futures&margin=coin` },
  ].filter((g) => g.symbols.length > 0);
  const brand = useBranding().name;
  // The top bar's layer is above the pages' sticky table headers: its menus
  // open over them (review B61).
  return (
    <header className="sticky top-0 z-[var(--z-topbar)] h-14 border-b border-line-1 bg-bg-0/95 backdrop-blur">
      <div className="mx-auto flex h-full max-w-[1920px] items-center gap-6 px-6">
        <Link to={routes.home} className="flex items-center gap-2" aria-label={brand}>
          <Logo />
        </Link>
        <nav className="flex h-full items-center gap-1 text-base">
          <Item to={routes.markets}>{t("nav.markets")}</Item>
          <Menu label={t("nav.spot")} to={routes.trade(recentSpot[0] ?? DEFAULT_SYMBOL)}>
            <p className="px-4 pb-1 pt-2 text-xs text-fg-3">{t("nav.recent")}</p>
            {(recentSpot.length > 0 ? recentSpot : [DEFAULT_SYMBOL]).map((s) => (
              <MenuLink key={s} to={routes.trade(s)}>
                {s.replace("-", "/")}
              </MenuLink>
            ))}
            <MenuLink to={routes.markets}>{t("nav.markets")} →</MenuLink>
          </Menu>
          <Menu label={t("nav.futures")} to={routes.futures(recentFutures[0] ?? DEFAULT_CONTRACT)}>
            {futuresGroups.map((g) => (
              <div key={g.key} data-testid={`futures-menu-${g.key}`}>
                <p className="px-4 pb-1 pt-2 text-xs text-fg-3">{g.label}</p>
                {g.symbols.map((symbol) => {
                  const c = bySymbol.get(symbol)!;
                  return (
                    <MenuLink key={symbol} to={routes.futures(symbol)}>
                      {c.base_asset}
                      {c.quote_asset} {t("pc.perpetual")}
                    </MenuLink>
                  );
                })}
                <MenuLink to={g.all}>{t("pc.allFutures")} →</MenuLink>
              </div>
            ))}
            {futuresGroups.length === 0 && <MenuLink to={`${routes.markets}?cat=futures`}>{t("pc.allFutures")} →</MenuLink>}
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
          <SearchPalette />
          {signedIn ? (
            <>
              <NotificationBell />
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
          <LanguageMenu />
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

// Menu opens on hover and on keyboard focus (CSS only, no timers): focus
// that came from the keyboard (:focus-visible), not the focus a click
// leaves on the item, which kept the menu open after the pointer had left
// it (B109); an item followed blurs too, so the keyboard's Enter closes it.
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
        <ChevronDown size={14} className="transition-transform duration-[var(--t-fast)] group-hover:rotate-180 group-has-[:focus-visible]:rotate-180" />
      </NavLink>
      <div
        className={cn(
          "invisible absolute top-full z-[var(--z-dropdown)] min-w-44 translate-y-1 rounded-2 border border-line-1 bg-bg-1 py-1 opacity-0 shadow-pop transition-[opacity,transform] duration-[var(--t-fast)]",
          "group-has-[:focus-visible]:visible group-has-[:focus-visible]:translate-y-0 group-has-[:focus-visible]:opacity-100 group-hover:visible group-hover:translate-y-0 group-hover:opacity-100",
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
    <Link
      to={to}
      onClick={(e) => e.currentTarget.blur()}
      className="block px-4 py-2 text-sm text-fg-2 transition-colors hover:bg-bg-2 hover:text-fg-1"
    >
      {children}
    </Link>
  );
}

// The bell shows the unread count; notification pushes refresh it.
function NotificationBell() {
  const { t } = useTranslation();
  const unread = useUnreadNotifications();
  return (
    <Link
      to={routes.notifications}
      aria-label={unread > 0 ? `${t("nav.notifications")} (${unread})` : t("nav.notifications")}
      className="relative grid size-9 place-items-center rounded-2 text-fg-2 transition-colors hover:bg-bg-2 hover:text-fg-1"
    >
      <Bell size={18} />
      {unread > 0 && (
        <span className="absolute right-1 top-1 grid h-4 min-w-4 animate-pop-in place-items-center rounded-full bg-danger px-1 text-[10px] font-semibold leading-none text-black">
          {unread > 99 ? "99+" : unread}
        </span>
      )}
    </Link>
  );
}

// The language button's short names, each in itself.
const SHORT_NAMES: Record<Locale, string> = { "zh-CN": "简体", "zh-TW": "繁體", en: "EN" };

// The languages open as Menu does (hover and keyboard focus, CSS only),
// each named in itself; choosing one switches at once.
function LanguageMenu() {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  return (
    <div className="group relative flex h-full items-center">
      <button
        type="button"
        aria-label={t("nav.language")}
        className="flex h-9 items-center gap-1 rounded-2 px-2 text-sm text-fg-2 transition-colors hover:bg-bg-2 hover:text-fg-1"
      >
        <Globe size={16} />
        <span lang={locale}>{SHORT_NAMES[locale]}</span>
      </button>
      <div
        className={cn(
          "invisible absolute right-0 top-full z-[var(--z-dropdown)] min-w-36 translate-y-1 rounded-2 border border-line-1 bg-bg-1 py-1 opacity-0 shadow-pop transition-[opacity,transform] duration-[var(--t-fast)]",
          "group-has-[:focus-visible]:visible group-has-[:focus-visible]:translate-y-0 group-has-[:focus-visible]:opacity-100 group-hover:visible group-hover:translate-y-0 group-hover:opacity-100",
        )}
      >
        {LOCALES.map((l) => (
          <button
            key={l}
            type="button"
            lang={l}
            aria-pressed={l === locale}
            onClick={(e) => {
              setLocale(l);
              e.currentTarget.blur();
            }}
            className={cn(
              "flex w-full items-center justify-between gap-3 px-4 py-2 text-left text-sm transition-colors hover:bg-bg-2 hover:text-fg-1",
              l === locale ? "text-fg-1" : "text-fg-2",
            )}
          >
            {LOCALE_NAMES[l]}
            {l === locale && <Check size={14} className="text-brand" />}
          </button>
        ))}
      </div>
    </div>
  );
}
