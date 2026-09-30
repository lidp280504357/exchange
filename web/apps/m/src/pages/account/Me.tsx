import { enumLabel, errorText, routes, selectRestoring, selectSignedIn, selectUserId, signOut, switchSite, useSession } from "@exchange/core";
import { useUnreadNotifications } from "@exchange/core/user/notifications";
import { useProfile } from "@exchange/core/user/profile";
import { useBoundIdentities } from "@exchange/core/user/security";
import { Badge, Button, Skeleton, copyText, listItem, toast } from "@exchange/ui";
import {
  Bell, Copy, LifeBuoy, LogOut, Megaphone, Monitor, MonitorSmartphone, RotateCcw, ShieldCheck, SlidersHorizontal, UserRound,
} from "lucide-react";
import { motion } from "motion/react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "react-router";
import { usePageHeader } from "../../layout/header";
import { ConfirmSheet } from "./parts/ConfirmSheet";
import { countBadge, primaryIdentity, shortId } from "./parts/logic";
import { Group, NavRow, Section } from "./parts/rows";

/**
 * Me (design §7.2 我的), a tab: the avatar, the masked identity and the
 * UID (tap to copy), then large rows — security, devices, notifications
 * with the unread count, settings, announcements, help, the PC site and
 * sign-out (after a confirmation sheet). Visitors get a sign-in card and
 * the public rows only.
 */
export default function Me() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const signedIn = useSession(selectSignedIn);
  const restoring = useSession(selectRestoring);
  const unread = useUnreadNotifications();
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  usePageHeader({ title: <span className="text-md font-semibold text-fg-1">{t("nav.me")}</span> }, [t]);

  const toPC = () => {
    // switchSite keeps the path, and the PC site has no "me" page: go
    // through the home page, which both sites have (navigate replaces the
    // address at once).
    navigate(routes.home, { replace: true });
    switchSite("pc");
  };

  const logout = async () => {
    setBusy(true);
    try {
      await signOut();
      toast.success(t("mAccount.me.loggedOut"));
    } catch (e) {
      // The session is forgotten here either way; the server call failed.
      toast.error(errorText(e));
    } finally {
      setBusy(false);
      setConfirm(false);
    }
  };

  return (
    <div className="flex flex-col gap-4 px-4 py-3">
      {restoring ? <ProfileSkeleton /> : signedIn ? <ProfileCard /> : <WelcomeCard />}

      {(signedIn || restoring) && (
        <Section title={t("mAccount.me.account")}>
          {restoring ? (
            <div aria-busy className="flex flex-col divide-y divide-line-1 rounded-3 bg-bg-1">
              {[0, 1, 2].map((i) => (
                <div key={i} className="flex min-h-14 items-center gap-3 px-4">
                  <Skeleton className="size-9 rounded-2" />
                  <Skeleton className="h-4 w-28" />
                </div>
              ))}
            </div>
          ) : (
            <Group index={1}>
              <NavRow icon={<ShieldCheck size={18} />} label={t("mAccount.security.title")} to={routes.security} />
              <NavRow icon={<MonitorSmartphone size={18} />} label={t("nav.sessions")} to={routes.sessions} />
              <NavRow
                icon={<Bell size={18} />}
                label={t("nav.notifications")}
                to={routes.notifications}
                trailing={
                  unread > 0 && (
                    <>
                      <span aria-hidden className="min-w-5 rounded-full bg-danger px-1.5 text-center text-xs font-semibold leading-5 text-white tabular-nums">
                        {countBadge(unread)}
                      </span>
                      <span className="sr-only">{t("mAccount.me.unread", { count: unread })}</span>
                    </>
                  )
                }
              />
            </Group>
          )}
        </Section>
      )}

      <Section title={t("mAccount.me.general")}>
        <Group index={2}>
          <NavRow icon={<SlidersHorizontal size={18} />} label={t("nav.settings")} to={routes.settings} />
          <NavRow icon={<Megaphone size={18} />} label={t("nav.announcements")} to={routes.announcements} />
          <NavRow icon={<LifeBuoy size={18} />} label={t("nav.help")} to={routes.help} />
          <NavRow icon={<Monitor size={18} />} label={t("footer.toPC")} onClick={toPC} />
        </Group>
      </Section>

      {signedIn && (
        <Group index={3}>
          <NavRow icon={<LogOut size={18} />} label={t("nav.logout")} tone="danger" chevron={false} onClick={() => setConfirm(true)} />
        </Group>
      )}

      <p className="px-1 pt-2 text-center text-xs text-fg-3">{t("footer.copyright")}</p>

      <ConfirmSheet
        open={confirm}
        onOpenChange={setConfirm}
        title={t("mAccount.me.logoutTitle")}
        description={t("mAccount.me.logoutDesc")}
        tone="danger"
        confirmText={t("nav.logout")}
        loading={busy}
        onConfirm={() => void logout()}
      />
    </div>
  );
}

function ProfileCard() {
  const { t } = useTranslation();
  const userId = useSession(selectUserId);
  const ids = useBoundIdentities();
  const profile = useProfile();
  const identity = primaryIdentity(ids.data);
  const status = profile.data?.status;

  const copy = async () => {
    if (await copyText(userId)) toast.success(t("mAccount.me.uidCopied"));
  };

  return (
    <motion.section variants={listItem} initial="initial" animate="animate" custom={0} className="flex items-center gap-4 rounded-3 bg-bg-1 p-4">
      <span aria-hidden className="grid size-14 shrink-0 place-items-center rounded-full bg-brand-soft text-brand">
        <UserRound size={28} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex min-h-7 min-w-0 items-center gap-2">
          {ids.isPending ? (
            <Skeleton className="h-5 w-40 max-w-full" />
          ) : (
            <span className="min-w-0 truncate text-md font-semibold text-fg-1">{identity ?? t("mAccount.me.user")}</span>
          )}
          {status && status !== "ACTIVE" && (
            <Badge tone={status === "CLOSED" ? "neutral" : "warn"} dot>
              {enumLabel(status, "accountStatus")}
            </Badge>
          )}
          {ids.isError && (
            // The identity could not be read: the fallback name shows, with a way to try again.
            <button
              type="button"
              aria-label={`${t("common.retry")}: ${errorText(ids.error)}`}
              onClick={() => void ids.refetch()}
              className="-my-2 grid size-11 shrink-0 place-items-center text-danger"
            >
              <RotateCcw size={16} />
            </button>
          )}
        </div>
        <button
          type="button"
          onClick={() => void copy()}
          aria-label={`${t("mAccount.me.copyUid")}: ${userId}`}
          className="-ml-1 inline-flex min-h-11 items-center gap-1.5 rounded-2 px-1 text-sm text-fg-3 transition-colors active:text-fg-1"
        >
          {t("mAccount.uid")}
          <span className="text-fg-2 tabular-nums">{shortId(userId)}</span>
          <Copy size={14} aria-hidden />
        </button>
      </div>
    </motion.section>
  );
}

function WelcomeCard() {
  const { t } = useTranslation();
  return (
    <motion.section variants={listItem} initial="initial" animate="animate" custom={0} className="relative overflow-hidden rounded-3 bg-bg-1 p-5">
      <div aria-hidden className="pointer-events-none absolute -right-10 -top-10 size-40 rounded-full bg-brand-soft blur-2xl" />
      <div className="relative flex items-center gap-3">
        <span aria-hidden className="grid size-12 shrink-0 place-items-center rounded-full bg-bg-2 text-fg-3">
          <UserRound size={24} />
        </span>
        <h2 className="text-lg font-semibold text-fg-1">{t("mAccount.me.welcome")}</h2>
      </div>
      <p className="relative mt-3 text-sm leading-relaxed text-fg-2">{t("mAccount.me.welcomeHint")}</p>
      <div className="relative mt-4 grid grid-cols-2 gap-3">
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

function ProfileSkeleton() {
  return (
    <div aria-busy className="flex items-center gap-4 rounded-3 bg-bg-1 p-4">
      <Skeleton round className="size-14" />
      <div className="flex flex-1 flex-col gap-2">
        <Skeleton className="h-5 w-40" />
        <Skeleton className="h-4 w-32" />
      </div>
    </div>
  );
}
