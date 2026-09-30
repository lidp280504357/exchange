import { page, Skeleton, SkeletonLines } from "@exchange/ui";
import { Lock, ShieldCheck } from "lucide-react";
import { motion } from "motion/react";
import { useEffect, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

export type AuthScreenProps = {
  title: ReactNode;
  subtitle?: ReactNode;
  /** Links under the form ("no account yet? sign up"). */
  footer?: ReactNode;
  children: ReactNode;
};

/**
 * AuthScreen is the full-screen column of sign-in, sign-up and reset
 * inside AuthShell (design §7.2): a large title, the form, the links, and
 * at the bottom the address to check and a short security note. A new
 * step (key) fades in, rises 8 px and starts at the top (the long sign-up
 * form is left scrolled down when its code step opens).
 */
export function AuthScreen({ title, subtitle, footer, children }: AuthScreenProps) {
  const { t } = useTranslation();
  const origin = globalThis.location?.origin ?? "https://m.astras.vip";
  useEffect(() => {
    if (window.scrollY > 0) window.scrollTo(0, 0);
  }, []);
  return (
    <motion.div variants={page} initial="initial" animate="animate" className="flex flex-1 flex-col gap-6">
      <header className="flex flex-col gap-2">
        <h1 className="text-2xl font-semibold text-fg-1">{title}</h1>
        {subtitle && <p className="text-sm leading-relaxed text-fg-3">{subtitle}</p>}
      </header>
      {children}
      {footer && <div className="flex flex-wrap items-center gap-x-1 text-sm text-fg-3">{footer}</div>}
      <div className="mt-auto flex flex-col gap-2 border-t border-line-1 pt-4 text-xs leading-relaxed text-fg-3">
        <p className="flex min-w-0 items-center gap-1.5">
          <Lock size={12} className="shrink-0 text-success" aria-hidden />
          <span className="min-w-0 truncate">
            {t("mAuth.siteCheck")} <span className="font-medium text-success">{origin}</span>
          </span>
        </p>
        <p className="flex items-start gap-1.5">
          <ShieldCheck size={12} className="mt-0.5 shrink-0 text-success" aria-hidden />
          <span>
            {t("mAuth.securityNote")} {t("mAuth.simulated")}
          </span>
        </p>
      </div>
    </motion.div>
  );
}

/** AuthPending holds the screen's place while the session is restored at start-up. */
export function AuthPending() {
  return (
    <div aria-busy className="flex flex-col gap-6">
      <div className="flex flex-col gap-2">
        <Skeleton className="h-8 w-40" />
        <Skeleton className="h-4 w-64 max-w-full" />
      </div>
      <Skeleton className="h-12 w-full rounded-2" />
      <Skeleton className="h-12 w-full rounded-2" />
      <SkeletonLines lines={2} />
      <Skeleton className="h-12 w-full rounded-2" />
    </div>
  );
}
