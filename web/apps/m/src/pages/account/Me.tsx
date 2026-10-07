import { errorText, routes, selectRestoring, selectSignedIn, signOut, switchSite, useSession } from "@exchange/core";
import { useAppsOffered } from "@exchange/core/platform/apps";
import { useBrandText } from "@exchange/core/platform/index";
import { useTerminalPrefs } from "@exchange/core/trading/prefs";
import { useUnreadNotifications } from "@exchange/core/user/notifications";
import { toast } from "@exchange/ui";
import {
  Bell, ClipboardList, Download, FileText, History, Info, LifeBuoy, ListChecks, LogOut, Megaphone, Monitor, MonitorSmartphone, ReceiptText,
  ShieldCheck, SlidersHorizontal, Star,
} from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { usePageHeader } from "../../layout/header";
import { AboutSheet } from "./me/AboutSheet";
import { AssetsCard } from "./me/AssetsCard";
import { GuardCard } from "./me/GuardCard";
import { HeaderActions } from "./me/HeaderActions";
import { IdentityCard, IdentitySkeleton } from "./me/IdentityCard";
import { ordersPath } from "./me/logic";
import { MarketGlance } from "./me/MarketGlance";
import { NewsStrip } from "./me/NewsStrip";
import { QuickGrid, type QuickItem } from "./me/QuickGrid";
import { WelcomeCard } from "./me/WelcomeCard";
import { ConfirmSheet } from "./parts/ConfirmSheet";
import { countBadge } from "./parts/logic";
import { Group, NavRow, Section } from "./parts/rows";

/**
 * Me (design §7.3), a tab. Signed in: the identity card, the assets card,
 * shortcuts, the security ring, the announcement strip, then the grouped
 * rows and sign-out (after a confirmation sheet). Visitors get a welcome
 * card, a glance at the markets, the public shortcuts and rows.
 */
export default function Me() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const signedIn = useSession(selectSignedIn);
  const restoring = useSession(selectRestoring);
  const unread = useUnreadNotifications();
  const recent = useTerminalPrefs((s) => s.recent);
  const [confirm, setConfirm] = useState(false);
  const [about, setAbout] = useState(false);
  const copyright = useBrandText((p) => p.footer.copyright);
  const appsOffered = useAppsOffered();
  const [busy, setBusy] = useState(false);
  usePageHeader(
    { title: <span className="text-md font-semibold text-fg-1">{t("nav.me")}</span>, right: <HeaderActions signedIn={signedIn} /> },
    [t, signedIn],
  );

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

  const news: QuickItem = { key: "announcements", icon: <Megaphone size={18} />, label: t("nav.announcements"), to: routes.announcements };
  const help: QuickItem = { key: "help", icon: <LifeBuoy size={18} />, label: t("nav.help"), to: routes.help };
  const publicItems: QuickItem[] = [news, help, { key: "pc", icon: <Monitor size={18} />, label: t("mAccount.me.quick.toPC"), onClick: toPC }];
  const memberItems: QuickItem[] = [
    { key: "open", icon: <ClipboardList size={18} />, label: t("mAccount.me.quick.open"), to: ordersPath(recent, "open") },
    { key: "history", icon: <History size={18} />, label: t("mAccount.me.quick.history"), to: ordersPath(recent, "history") },
    { key: "fills", icon: <ListChecks size={18} />, label: t("mAccount.me.quick.fills"), to: ordersPath(recent, "fills") },
    { key: "ledger", icon: <ReceiptText size={18} />, label: t("mAccount.me.quick.ledger"), to: routes.history },
    { key: "favorites", icon: <Star size={18} />, label: t("mAccount.me.quick.favorites"), to: `${routes.markets}?cat=favorites` },
    { key: "devices", icon: <MonitorSmartphone size={18} />, label: t("mAccount.me.quick.devices"), to: routes.sessions },
    news,
    help,
  ];

  return (
    <div className="flex flex-col gap-4 px-4 py-3">
      {restoring ? <IdentitySkeleton /> : signedIn ? <IdentityCard /> : <WelcomeCard />}
      {signedIn && <AssetsCard index={1} />}
      {!signedIn && !restoring && <MarketGlance index={1} />}
      {!restoring && <QuickGrid items={signedIn ? memberItems : publicItems} index={2} />}
      {signedIn && <GuardCard index={3} />}
      <NewsStrip index={4} />

      {signedIn && (
        <Section title={t("mAccount.me.groups.account")}>
          <Group index={5}>
            <NavRow icon={<ShieldCheck size={18} />} label={t("mAccount.security.title")} to={routes.security} />
            <NavRow icon={<MonitorSmartphone size={18} />} label={t("mAccount.me.devices")} to={routes.sessions} />
            <NavRow
              icon={<Bell size={18} />}
              label={t("nav.notifications")}
              to={routes.notifications}
              trailing={
                unread > 0 && (
                  <>
                    <span aria-hidden className="min-w-5 rounded-full bg-danger px-1.5 text-center text-xs font-semibold leading-5 text-black tabular-nums">
                      {countBadge(unread)}
                    </span>
                    <span className="sr-only">{t("mAccount.me.unread", { count: unread })}</span>
                  </>
                )
              }
            />
          </Group>
        </Section>
      )}

      <Section title={t("mAccount.me.groups.prefs")}>
        <Group index={6}>
          <NavRow icon={<SlidersHorizontal size={18} />} label={t("nav.settings")} to={routes.settings} />
        </Group>
      </Section>

      <Section title={t("mAccount.me.groups.support")}>
        <Group index={7}>
          <NavRow icon={<LifeBuoy size={18} />} label={t("nav.help")} to={routes.help} />
          <NavRow icon={<FileText size={18} />} label={t("footer.legal")} to={routes.legal("terms")} />
          <NavRow icon={<Info size={18} />} label={t("mAccount.me.about.title")} onClick={() => setAbout(true)} />
        </Group>
      </Section>

      <Section title={t("mAccount.me.groups.other")}>
        <Group index={8}>
          {/* The apps to download, while any is offered (design 2026-10-07, App download page §4). */}
          {appsOffered && <NavRow icon={<Download size={18} />} label={t("nav.downloadApp")} to={routes.download} />}
          <NavRow icon={<Monitor size={18} />} label={t("footer.toPC")} onClick={toPC} />
        </Group>
      </Section>

      {signedIn && (
        <Group index={9}>
          <NavRow icon={<LogOut size={18} />} label={t("nav.logout")} tone="danger" chevron={false} onClick={() => setConfirm(true)} />
        </Group>
      )}

      <p className="px-1 pt-2 text-center text-xs text-fg-3">{copyright || t("footer.copyright")}</p>

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
      <AboutSheet open={about} onOpenChange={setAbout} />
    </div>
  );
}
