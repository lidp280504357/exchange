import { DEFAULT_SYMBOL, routes, selectSignedIn, useSession } from "@exchange/core";
import { useUnreadNotifications } from "@exchange/core/user/notifications";
import { cn } from "@exchange/ui";
import { ArrowLeftRight, Bell, CandlestickChart, Home, Search, UserRound, Wallet } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, NavLink, Outlet, useLocation } from "react-router";
import { useScrollTop } from "../components/useScrollTop";
import { BrandMark, TestModeStrip } from "./Brand";
import { useHeader } from "./header";
import { StatusStrip } from "./StatusStrip";

/**
 * MobileShell (design §7.1): a 44 px top bar (the page's title or pair
 * switcher, or the logo with search and notifications), the page, and a
 * 56 px tab bar above the safe area — home, markets, trade (the last
 * pair), assets, me. Touch targets are at least 44 px; nothing scrolls
 * sideways.
 */
export function MobileShell() {
  const { t } = useTranslation();
  const { pathname } = useLocation();
  useScrollTop(pathname);
  const [lastTrade, setLastTrade] = useState(() => localStorage.getItem("m.lastTrade") ?? routes.trade(DEFAULT_SYMBOL));
  useEffect(() => {
    if (pathname.startsWith("/trade/") || pathname.startsWith("/futures/")) {
      localStorage.setItem("m.lastTrade", pathname);
      setLastTrade(pathname);
    }
  }, [pathname]);
  return (
    <div className="flex min-h-dvh flex-col bg-bg-0 pb-[calc(56px+env(safe-area-inset-bottom))]">
      <TopBar />
      <main key={pathname} className="flex-1 animate-fade-up">
        <Outlet />
      </main>
      <nav
        aria-label={t("nav.home")}
        className="fixed inset-x-0 bottom-0 z-[var(--z-sticky)] border-t border-line-1 bg-bg-1/95 pb-[env(safe-area-inset-bottom)] backdrop-blur"
      >
        <div className="grid h-14 grid-cols-5">
          <Tab to={routes.home} icon={<Home size={20} />} label={t("nav.home")} end />
          <Tab to={routes.markets} icon={<CandlestickChart size={20} />} label={t("nav.markets")} />
          <Tab to={lastTrade} icon={<ArrowLeftRight size={20} />} label={t("nav.trade")} match={/^\/(trade|futures)\//} />
          <Tab to={routes.assets} icon={<Wallet size={20} />} label={t("nav.assets")} />
          <Tab to={routes.me} icon={<UserRound size={20} />} label={t("nav.me")} />
        </div>
      </nav>
    </div>
  );
}

function Tab({ to, icon, label, end, match }: { to: string; icon: ReactNode; label: string; end?: boolean; match?: RegExp }) {
  const { pathname } = useLocation();
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        cn(
          "flex flex-col items-center justify-center gap-0.5 text-xs transition-colors",
          isActive || match?.test(pathname) ? "text-brand" : "text-fg-3",
        )
      }
    >
      {icon}
      {label}
    </NavLink>
  );
}

function TopBar() {
  const { t } = useTranslation();
  const header = useHeader();
  const signedIn = useSession(selectSignedIn);
  const unread = useUnreadNotifications();
  return (
    <header className="sticky top-0 z-[var(--z-sticky)] bg-bg-0/95 pt-[env(safe-area-inset-top)] backdrop-blur">
      <div className="flex h-tap items-center justify-between gap-2 px-4">
        <div className="min-w-0 flex-1 truncate">{header?.title ?? <BrandMark />}</div>
        <div className="flex items-center">
          {header?.right ?? (
            <>
              <Link to={routes.markets} aria-label={t("nav.search")} className="grid size-tap place-items-center text-fg-2">
                <Search size={20} />
              </Link>
              {signedIn && (
                <Link
                  to={routes.notifications}
                  aria-label={unread > 0 ? `${t("nav.notifications")} (${unread})` : t("nav.notifications")}
                  className="relative grid size-tap place-items-center text-fg-2"
                >
                  <Bell size={20} />
                  {unread > 0 && (
                    <span className="absolute right-1.5 top-1.5 grid h-4 min-w-4 place-items-center rounded-full bg-danger px-1 text-[10px] font-semibold leading-none text-black">
                      {unread > 99 ? "99+" : unread}
                    </span>
                  )}
                </Link>
              )}
            </>
          )}
        </div>
      </div>
      <TestModeStrip />
      <StatusStrip />
    </header>
  );
}
