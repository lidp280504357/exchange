import { page, Skeleton, SkeletonLines } from "@exchange/ui";
import { Lock, ShieldCheck } from "lucide-react";
import { motion } from "motion/react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

export type AuthCardProps = {
  icon: ReactNode;
  title: ReactNode;
  subtitle?: ReactNode;
  /** Links under the form ("no account yet? sign up"). */
  footer?: ReactNode;
  children: ReactNode;
};

/**
 * AuthCard is the column of the sign-in, sign-up and reset pages inside
 * AuthShell: the address to check, a titled header, the form, the links
 * and a short security note.
 */
export function AuthCard({ icon, title, subtitle, footer, children }: AuthCardProps) {
  const { t } = useTranslation();
  const origin = globalThis.location?.origin ?? "https://astras.vip";
  return (
    <motion.div variants={page} initial="initial" animate="animate" className="flex flex-col gap-6">
      <p className="inline-flex w-fit max-w-full items-center gap-1.5 rounded-full border border-line-1 bg-bg-1 px-3 py-1 text-xs text-fg-3">
        <Lock size={12} className="shrink-0 text-success" aria-hidden />
        <span className="truncate">
          {t("pcAuth.siteCheck")} <span className="font-medium text-success">{origin}</span>
        </span>
      </p>
      <header className="flex flex-col gap-4">
        <span aria-hidden className="grid size-11 place-items-center rounded-3 bg-brand-soft text-brand">
          {icon}
        </span>
        <div>
          <h1 className="text-xl font-semibold text-fg-1">{title}</h1>
          {subtitle && <p className="mt-1.5 text-sm leading-relaxed text-fg-3">{subtitle}</p>}
        </div>
      </header>
      {children}
      {footer && <div className="text-sm text-fg-3">{footer}</div>}
      <div className="flex items-start gap-2 border-t border-line-1 pt-4 text-xs leading-relaxed text-fg-3">
        <ShieldCheck size={14} className="mt-0.5 shrink-0 text-success" aria-hidden />
        <p>
          {t("pcAuth.securityNote")}
          <span className="mt-0.5 block">{t("pcAuth.simulated")}</span>
        </p>
      </div>
    </motion.div>
  );
}

/** AuthPending holds the card's place while the session is restored at start-up. */
export function AuthPending() {
  return (
    <div aria-busy className="flex flex-col gap-6">
      <Skeleton className="h-6 w-56 rounded-full" />
      <Skeleton className="size-11 rounded-3" />
      <div className="flex flex-col gap-2">
        <Skeleton className="h-6 w-40" />
        <Skeleton className="h-4 w-64" />
      </div>
      <SkeletonLines lines={5} />
    </div>
  );
}
