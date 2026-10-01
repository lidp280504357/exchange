import { routes } from "@exchange/core";
import { useUnreadNotifications } from "@exchange/core/user/notifications";
import { Bell, Settings } from "lucide-react";
import { motion } from "motion/react";
import { useRef } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { countBadge } from "../parts/logic";

/** useRises counts the times a number went up (a key that restarts an animation on each rise only). */
function useRises(n: number): number {
  const prev = useRef(n);
  const rises = useRef(0);
  if (n > prev.current) rises.current++;
  prev.current = n;
  return rises.current;
}

/**
 * HeaderActions are the "me" tab's top-bar buttons (design §7.3 ①): the
 * notifications bell with the unread count, which pops on a spring when a
 * new one arrives, and settings.
 */
export function HeaderActions({ signedIn }: { signedIn: boolean }) {
  const { t } = useTranslation();
  const unread = useUnreadNotifications();
  const rises = useRises(unread);
  const button = "relative grid size-11 place-items-center rounded-full text-fg-2 transition-colors active:bg-bg-2 active:text-fg-1";
  return (
    <div className="-mr-2 flex items-center">
      {signedIn && (
        <Link
          to={routes.notifications}
          aria-label={unread > 0 ? `${t("nav.notifications")} (${t("mAccount.me.unread", { count: unread })})` : t("nav.notifications")}
          className={button}
        >
          <Bell size={20} aria-hidden />
          {unread > 0 && (
            <motion.span
              key={rises}
              aria-hidden
              initial={{ scale: 0.4 }}
              animate={{ scale: 1 }}
              transition={{ type: "spring", stiffness: 520, damping: 16 }}
              className="absolute right-1 top-1.5 min-w-4 rounded-full bg-danger px-1 text-center text-[10px] font-semibold leading-4 text-white tabular-nums"
            >
              {countBadge(unread)}
            </motion.span>
          )}
        </Link>
      )}
      <Link to={routes.settings} aria-label={t("nav.settings")} className={button}>
        <Settings size={20} aria-hidden />
      </Link>
    </div>
  );
}
