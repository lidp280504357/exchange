import { routes, useTotpStatus } from "@exchange/core";
import { useMarginEntry } from "@exchange/core/margin/hooks";
import { Button, cn, copyText } from "@exchange/ui";
import { ArrowDownToLine, ArrowLeftRight, ArrowUpFromLine, Check, Copy, Landmark, LayoutGrid, ScrollText, ShieldCheck } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, NavLink } from "react-router";

const items = [
  { to: routes.assets, label: "nav.overview", icon: LayoutGrid, end: true },
  { to: routes.deposit, label: "nav.deposit", icon: ArrowDownToLine, end: false },
  { to: routes.withdraw, label: "nav.withdraw", icon: ArrowUpFromLine, end: false },
  { to: routes.transfer, label: "nav.transfer", icon: ArrowLeftRight, end: false },
  { to: routes.history, label: "nav.history", icon: ScrollText, end: false },
] as const;

// The margin accounts: shown to whom margin trading is open, and to whom
// still has a margin account (to repay and move out when it closed).
const marginItem = { to: routes.margin, label: "nav.margin", icon: Landmark, end: false } as const;

/**
 * AssetsLayout frames every assets page: the section's side navigation
 * (icons only below 1280 px) and the page title with its actions.
 */
export function AssetsLayout({
  title, subtitle, actions, children,
}: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  return (
    <div className="mx-auto flex max-w-[1440px] gap-6 px-6 py-6">
      <SideNav />
      <div className="min-w-0 flex-1">
        <header className="mb-5 flex min-h-10 flex-wrap items-end justify-between gap-3">
          <div className="min-w-0">
            <h1 className="text-xl font-semibold text-fg-1">{title}</h1>
            {subtitle && <p className="mt-1 text-sm text-fg-3">{subtitle}</p>}
          </div>
          {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
        </header>
        {children}
      </div>
    </div>
  );
}

function SideNav() {
  const { t } = useTranslation();
  const totp = useTotpStatus();
  const margin = useMarginEntry();
  const shown = margin ? [...items, marginItem] : items;
  return (
    <aside className="sticky top-20 flex h-fit w-14 shrink-0 flex-col gap-4 xl:w-52">
      <nav aria-label={t("pcAssets.nav.label")} className="flex flex-col gap-1 rounded-3 border border-line-1 bg-bg-1 p-1.5">
        {shown.map(({ to, label, icon: Icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            aria-label={t(label)}
            title={t(label)}
            className={({ isActive }) =>
              cn(
                "group relative flex h-10 items-center gap-3 rounded-2 px-3 text-base transition-colors duration-[var(--t-fast)]",
                isActive ? "bg-bg-2 font-medium text-fg-1" : "text-fg-2 hover:bg-bg-2 hover:text-fg-1",
              )
            }
          >
            {({ isActive }) => (
              <>
                <span
                  aria-hidden
                  className={cn("absolute inset-y-2 left-0 w-0.5 rounded-full bg-brand transition-opacity", isActive ? "opacity-100" : "opacity-0")}
                />
                <Icon size={18} className={cn("shrink-0", isActive ? "text-brand" : "text-fg-3 group-hover:text-fg-2")} />
                <span className="hidden truncate xl:inline">{t(label)}</span>
              </>
            )}
          </NavLink>
        ))}
      </nav>
      {totp.data && !totp.data.enabled && (
        <div className="hidden rounded-3 border border-line-1 bg-bg-1 p-4 xl:block">
          <div className="mb-2 grid size-9 place-items-center rounded-full bg-brand-soft text-brand">
            <ShieldCheck size={18} />
          </div>
          <div className="text-sm font-medium text-fg-1">{t("pcAssets.securityTip.title")}</div>
          <p className="mt-1 text-xs leading-relaxed text-fg-3">{t("pcAssets.securityTip.body")}</p>
          <Button asChild size="sm" variant="secondary" className="mt-3">
            <Link to={routes.security}>{t("pcAssets.securityTip.action")}</Link>
          </Button>
        </div>
      )}
    </aside>
  );
}

/** Tips lists the short rules of a page, each with an icon. */
export function Tips({ title, tips }: { title: string; tips: [ReactNode, string][] }) {
  return (
    <Card title={title} bodyClassName="p-4">
      <ul className="flex flex-col gap-3">
        {tips.map(([icon, text]) => (
          <li key={text} className="flex items-start gap-2.5 text-sm leading-relaxed text-fg-2">
            <span aria-hidden className="mt-0.5 grid size-6 shrink-0 place-items-center rounded-full bg-bg-2 text-fg-3">
              {icon}
            </span>
            {text}
          </li>
        ))}
      </ul>
    </Card>
  );
}

/** CopyAction is a button that copies a value and says so for 1.5 s. */
export function CopyAction({ value, label, size = "sm" }: { value: string; label: string; size?: "sm" | "md" }) {
  const { t } = useTranslation();
  const [done, setDone] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const copy = async () => {
    if (!(await copyText(value))) return;
    setDone(true);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setDone(false), 1500);
  };
  return (
    <Button size={size} icon={done ? <Check size={14} className="animate-fade-in" /> : <Copy size={14} />} onClick={() => void copy()}>
      {done ? t("common.copied") : label}
      <span aria-live="polite" className="sr-only">
        {done ? t("common.copied") : ""}
      </span>
    </Button>
  );
}

/** Card is the panel every block of the assets pages sits in. */
export function Card({
  title, extra, children, className, bodyClassName, id,
}: { title?: ReactNode; extra?: ReactNode; children: ReactNode; className?: string; bodyClassName?: string; id?: string }) {
  return (
    <section id={id} className={cn("rounded-3 border border-line-1 bg-bg-1", className)}>
      {(title || extra) && (
        <div className="flex min-h-12 items-center justify-between gap-3 border-b border-line-1 px-5 py-2.5">
          {title && <h2 className="text-md font-semibold text-fg-1">{title}</h2>}
          {extra && <div className="flex items-center gap-2">{extra}</div>}
        </div>
      )}
      <div className={cn("p-5", bodyClassName)}>{children}</div>
    </section>
  );
}
