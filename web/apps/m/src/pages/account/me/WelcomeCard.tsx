import { routes } from "@exchange/core";
import { useBranding, useWelcomeCredits } from "@exchange/core/platform/index";
import { Button, durations, ease, listItem } from "@exchange/ui";
import { BadgeCheck, CandlestickChart, Gift, Layers } from "lucide-react";
import { motion } from "motion/react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

const PERKS = [
  { key: "funds", icon: <Gift size={16} /> },
  { key: "markets", icon: <CandlestickChart size={16} /> },
  { key: "futures", icon: <Layers size={16} /> },
] as const;

/**
 * WelcomeCard (design §7.3, visitors): the wordmark drawn once in 1.2 s,
 * the slogan, three perks sliding in one after another, and sign-in and
 * sign-up.
 */
export function WelcomeCard() {
  const { t } = useTranslation();
  const name = useBranding().name;
  const credits = useWelcomeCredits();
  // The welcome credits' perk only while there are any (design 2026-10-04 §4.2).
  const perks = PERKS.filter((p) => p.key !== "funds" || credits);
  return (
    <motion.section
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={0}
      data-testid="me-welcome"
      className="relative isolate overflow-hidden rounded-3 border border-line-1 bg-bg-1 p-5"
    >
      <div aria-hidden className="me-grid pointer-events-none absolute inset-0 -z-10" />
      <span
        aria-hidden
        className="pointer-events-none absolute -right-14 -top-20 -z-10 size-48 animate-[me-drift-a_20s_ease-in-out_infinite_alternate] rounded-full bg-brand/20 blur-3xl will-change-transform"
      />
      <h2 className="sr-only">{t("mAccount.me.welcome")}</h2>
      <svg aria-hidden viewBox="0 0 220 44" className="h-9 w-auto text-brand">
        <text
          x="0"
          y="35"
          className="me-wordmark"
          fill="currentColor"
          stroke="currentColor"
          strokeWidth="1"
          fontSize="40"
          fontWeight="800"
          letterSpacing="4"
          style={{ fontFamily: "var(--font-sans)" }}
        >
          {name.toUpperCase()}
        </text>
      </svg>
      <p className="mt-2 text-md font-semibold text-fg-1">{t("mAccount.me.slogan")}</p>
      <ul className="mt-4 flex flex-col gap-2.5">
        {perks.map((p, i) => (
          <motion.li
            key={p.key}
            initial={{ opacity: 0, x: -12 }}
            animate={{ opacity: 1, x: 0 }}
            transition={{ duration: durations.slow, ease, delay: 0.5 + i * 0.15 }}
            className="flex items-center gap-2.5 text-sm text-fg-2"
          >
            <span className="grid size-7 shrink-0 place-items-center rounded-full bg-brand-soft text-brand">{p.icon}</span>
            {p.key === "funds" ? t("mAccount.me.perks.funds", { credits }) : t(`mAccount.me.perks.${p.key}`)}
            {p.key === "funds" && <BadgeCheck size={14} className="text-success" aria-hidden />}
          </motion.li>
        ))}
      </ul>
      <div className="mt-5 grid grid-cols-2 gap-3">
        <Button asChild size="lg">
          <Link to={`${routes.login}?next=${encodeURIComponent(routes.me)}`}>{t("nav.login")}</Link>
        </Button>
        <Button asChild size="lg" variant="secondary">
          <Link to={routes.register}>{t("nav.register")}</Link>
        </Button>
      </div>
    </motion.section>
  );
}
