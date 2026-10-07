import { formatTime } from "@exchange/core";
import { nextUsernameChange } from "@exchange/core/user/avatar";
import { useProfile } from "@exchange/core/user/profile";
import { Button, CopyButton, TimeText, useFormatContext, useNow } from "@exchange/ui";
import { AtSign, CalendarDays, Fingerprint } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AccountLayout, Section } from "./parts/AccountLayout";
import { SecurityItem } from "./parts/SecurityItem";
import { AvatarCard } from "./profile/AvatarCard";
import { UsernameDialog } from "./profile/UsernameDialog";

/**
 * Profile (design 2026-10-07, avatars and usernames §1 #5): the avatar
 * (upload, or back to the built-in one), the username (drawn at sign-up,
 * changeable once in 7 days), the UID and when the account was made.
 * A frozen or closed account sees them without the changes.
 */
export default function Profile() {
  const { t } = useTranslation();
  const profile = useProfile();
  const { locale, timeZone } = useFormatContext();
  const now = useNow(60_000);
  const [rename, setRename] = useState(false);
  const p = profile.data;
  const locked = p?.status === "FROZEN" || p?.status === "CLOSED";
  const next = p ? nextUsernameChange(p, now) : null;
  const loading = profile.isPending;
  const failed = profile.isError ? profile.error : undefined;
  const retry = () => void profile.refetch();

  const usernameDesc = next
    ? t("pcProfile.username.cooldown", { time: formatTime(next, "datetime", locale, timeZone) })
    : p && !p.username_changed_at
      ? t("pcProfile.username.drawn")
      : t("pcProfile.username.desc");

  return (
    <AccountLayout title={t("pcProfile.title")} subtitle={t("pcProfile.subtitle")}>
      {locked && <p className="rounded-3 border border-warn/40 bg-warn/10 px-4 py-3 text-sm text-warn">{t("pcProfile.locked")}</p>}
      <div className="flex flex-col gap-3">
        <AvatarCard profile={p} locked={locked} index={0} />
        <SecurityItem
          index={1}
          icon={<AtSign size={20} />}
          title={t("pcProfile.username.title")}
          desc={usernameDesc}
          detail={
            p && (
              <span className="text-base font-medium text-fg-1" data-testid="profile-username">
                {p.username}
              </span>
            )
          }
          action={
            <Button size="sm" variant="secondary" disabled={!p || locked || next !== null} onClick={() => setRename(true)}>
              {t("pcProfile.username.change")}
            </Button>
          }
          loading={loading}
          error={failed}
          onRetry={retry}
        />
      </div>
      <Section title={t("pcProfile.account.title")}>
        <div className="flex flex-col gap-3">
          <SecurityItem
            index={2}
            icon={<Fingerprint size={20} />}
            title={t("common.uid")}
            desc={t("pcProfile.account.uidDesc")}
            detail={
              p && (
                <span className="flex items-center gap-1.5">
                  {p.user_id}
                  <CopyButton value={p.user_id} size={14} />
                </span>
              )
            }
            loading={loading}
            error={failed}
            onRetry={retry}
          />
          <SecurityItem
            index={3}
            icon={<CalendarDays size={20} />}
            title={t("pcProfile.account.joined")}
            desc={t("pcProfile.account.joinedDesc")}
            detail={p && <TimeText value={p.created_at} />}
            loading={loading}
            error={failed}
            onRetry={retry}
          />
        </div>
      </Section>
      <UsernameDialog open={rename} current={p?.username ?? ""} onClose={() => setRename(false)} />
    </AccountLayout>
  );
}
