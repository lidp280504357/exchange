import { routes, selectSignedIn, selectUserId, useSession } from "@exchange/core";
import { useUnreadNotifications } from "@exchange/core/user/notifications";
import { Button, CopyButton, cn } from "@exchange/ui";
import { Bell, MonitorSmartphone, ShieldCheck, SlidersHorizontal, UserRound, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, NavLink } from "react-router";

const NAV: { to: string; icon: LucideIcon; label: string; auth: boolean }[] = [
  { to: routes.security, icon: ShieldCheck, label: "nav.security", auth: true },
  { to: routes.sessions, icon: MonitorSmartphone, label: "nav.sessions", auth: true },
  { to: routes.notifications, icon: Bell, label: "nav.notifications", auth: true },
  { to: routes.settings, icon: SlidersHorizontal, label: "nav.settings", auth: false },
];

/** shortId keeps the ends of an ID: "0192e4c8…7e8f". */
export function shortId(id: string): string {
  return id.length > 14 ? `${id.slice(0, 8)}…${id.slice(-4)}` : id;
}

/**
 * AccountLayout frames the account pages: a side menu (security,
 * devices, notifications with the unread count, settings) with the
 * user's UID, and the page's title, subtitle and actions.
 */
export function AccountLayout({ title, subtitle, actions, children }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  const { t } = useTranslation();
  const signedIn = useSession(selectSignedIn);
  const userId = useSession(selectUserId);
  const unread = useUnreadNotifications();
  return (
    <div className="mx-auto max-w-[1440px] px-6 py-8">
      <div className="grid grid-cols-[216px_minmax(0,1fr)] items-start gap-6">
        <aside className="sticky top-20 flex flex-col gap-3 rounded-3 border border-line-1 bg-bg-1 p-3">
          {signedIn ? (
            <div className="flex items-center gap-3 rounded-2 bg-bg-2 p-3">
              <span aria-hidden className="grid size-9 shrink-0 place-items-center rounded-full bg-brand-soft text-brand">
                <UserRound size={18} />
              </span>
              <div className="min-w-0">
                <div className="text-xs text-fg-3">{t("pcAccount.uid")}</div>
                <div className="flex items-center gap-1 text-sm font-medium text-fg-1">
                  <span className="truncate tabular-nums">{shortId(userId)}</span>
                  <CopyButton value={userId} size={12} />
                </div>
              </div>
            </div>
          ) : (
            <div className="flex flex-col gap-2 rounded-2 bg-bg-2 p-3 text-xs leading-relaxed text-fg-3">
              {t("pcAccount.signInToManage")}
              <Button asChild size="sm">
                <Link to={`${routes.login}?next=${encodeURIComponent(routes.security)}`}>{t("nav.login")}</Link>
              </Button>
            </div>
          )}
          <nav aria-label={t("pcAccount.center")} className="flex flex-col gap-0.5">
            {NAV.map(({ to, icon: Icon, label, auth }) => (
              <NavLink
                key={to}
                to={to}
                className={({ isActive }) =>
                  cn(
                    "relative flex h-10 items-center gap-3 rounded-2 px-3 text-sm transition-colors",
                    isActive ? "bg-bg-2 font-medium text-fg-1" : "text-fg-2 hover:bg-bg-2 hover:text-fg-1",
                    auth && !signedIn && "opacity-60",
                  )
                }
              >
                {({ isActive }) => (
                  <>
                    {isActive && <span aria-hidden className="absolute inset-y-2 left-0 w-0.5 rounded-full bg-brand" />}
                    <Icon size={16} className={isActive ? "text-brand" : undefined} />
                    <span className="flex-1">{t(label)}</span>
                    {to === routes.notifications && unread > 0 && (
                      <span className="min-w-5 rounded-full bg-brand px-1.5 text-center text-xs font-semibold leading-5 text-brand-fg tabular-nums">
                        {unread > 99 ? "99+" : unread}
                      </span>
                    )}
                  </>
                )}
              </NavLink>
            ))}
          </nav>
        </aside>
        <section className="flex min-w-0 flex-col gap-6">
          <header className="flex flex-wrap items-end justify-between gap-4">
            <div className="min-w-0">
              <h1 className="text-xl font-semibold text-fg-1">{title}</h1>
              {subtitle && <p className="mt-1 text-sm text-fg-3">{subtitle}</p>}
            </div>
            {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
          </header>
          {children}
        </section>
      </div>
    </div>
  );
}

/** Section is a titled group of cards on an account page. */
export function Section({
  title, extra, children, className, id,
}: {
  title: ReactNode;
  extra?: ReactNode;
  children: ReactNode;
  className?: string;
  id?: string;
}) {
  return (
    <section id={id} className={cn("flex scroll-mt-20 flex-col gap-3", className)}>
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-md font-semibold text-fg-1">{title}</h2>
        {extra}
      </div>
      {children}
    </section>
  );
}
