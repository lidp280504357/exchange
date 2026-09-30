import { DEFAULT_SYMBOL, routes, switchSite, useWsStatus } from "@exchange/core";
import { cn } from "@exchange/ui";
import { ArrowLeftRight, CandlestickChart, Home, UserRound, Wallet } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { NavLink, Outlet, useLocation } from "react-router";

/**
 * MobileShell (design §7.1): a 44 px top bar, the page, and a 56 px tab bar
 * above the safe area — home, markets, trade (the last pair), assets, me.
 * Touch targets are at least 44 px; nothing scrolls sideways.
 */
export function MobileShell() {
  const { t } = useTranslation();
  const { pathname } = useLocation();
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
      <main className="flex-1">
        <Outlet />
      </main>
      <nav className="fixed inset-x-0 bottom-0 z-[var(--z-sticky)] border-t border-line-1 bg-bg-1/95 pb-[env(safe-area-inset-bottom)] backdrop-blur">
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
  const status = useWsStatus();
  const [online, setOnline] = useState(() => navigator.onLine);
  useEffect(() => {
    const on = () => setOnline(true);
    const off = () => setOnline(false);
    window.addEventListener("online", on);
    window.addEventListener("offline", off);
    return () => {
      window.removeEventListener("online", on);
      window.removeEventListener("offline", off);
    };
  }, []);
  return (
    <header className="sticky top-0 z-[var(--z-sticky)] bg-bg-0/95 pt-[env(safe-area-inset-top)] backdrop-blur">
      <div className="flex h-11 items-center justify-between px-4">
        <span className="font-semibold tracking-wide">ASTRAS</span>
        <button type="button" onClick={() => switchSite("pc")} className="min-h-11 px-2 text-xs text-fg-3">
          {t("footer.toPC")}
        </button>
      </div>
      {(!online || status === "reconnecting") && (
        <div role="status" className="bg-warn px-4 py-1 text-center text-xs text-brand-fg">
          {online ? t("common.reconnecting") : t("common.offline")}
        </div>
      )}
    </header>
  );
}
