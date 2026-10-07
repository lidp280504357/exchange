import { errorText, formatTime, routes } from "@exchange/core";
import { nextUsernameChange } from "@exchange/core/user/avatar";
import { useProfile } from "@exchange/core/user/profile";
import { ErrorState, Skeleton, TimeText, copyText, toast, useFormatContext, useNow } from "@exchange/ui";
import { ChevronRight, Copy } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { usePageHeader } from "../../layout/header";
import { shortId } from "./parts/logic";
import { Group, Section } from "./parts/rows";
import { AvatarEditor } from "./profile/AvatarEditor";
import { UsernameSheet } from "./profile/UsernameSheet";

/**
 * Profile on the phone (design 2026-10-07, avatars and usernames §1 #5):
 * the avatar (tap to change, or back to the built-in one), the username
 * (a sheet to change it, once in 7 days), the UID to copy and when the
 * account was made. A frozen or closed account sees them without changes.
 */
export default function Profile() {
  const { t } = useTranslation();
  usePageHeader({ title: t("mProfile.title"), back: routes.me }, [t]);
  const profile = useProfile();
  const { locale, timeZone } = useFormatContext();
  const now = useNow(60_000);
  const [rename, setRename] = useState(false);
  const p = profile.data;
  const locked = p?.status === "FROZEN" || p?.status === "CLOSED";
  const next = p ? nextUsernameChange(p, now) : null;

  if (profile.isError) {
    return (
      <div className="px-4 py-3">
        <ErrorState message={errorText(profile.error)} onRetry={() => void profile.refetch()} />
      </div>
    );
  }

  const copy = async () => {
    if (p && (await copyText(p.user_id))) toast.success(t("mProfile.uidCopied"));
  };

  return (
    <div className="flex flex-col gap-5 px-4 py-3">
      {locked && <p className="rounded-3 border border-warn/40 bg-warn/10 px-4 py-3 text-sm text-warn">{t("mProfile.locked")}</p>}
      <AvatarEditor profile={p} locked={locked} />
      <Section>
        <Group index={1}>
          <button
            type="button"
            onClick={() => setRename(true)}
            disabled={!p || locked || next !== null}
            data-testid="profile-username-row"
            className="group flex min-h-14 w-full items-center gap-3 px-4 py-2.5 text-left transition-colors enabled:active:bg-bg-2"
          >
            <span className="shrink-0 text-base text-fg-1">{t("mProfile.username.title")}</span>
            <span className="ml-auto min-w-0 text-right">
              {p ? (
                <span className="block truncate text-base font-medium text-fg-1" data-testid="profile-username">
                  {p.username}
                </span>
              ) : (
                <Skeleton className="ml-auto h-5 w-28" />
              )}
              {next !== null && (
                <span className="block text-xs text-fg-3">{t("mProfile.username.cooldown", { time: formatTime(next, "datetime", locale, timeZone) })}</span>
              )}
            </span>
            {next === null && !locked && <ChevronRight size={18} aria-hidden className="shrink-0 text-fg-3" />}
          </button>
          <Row label={t("common.uid")}>
            {p ? (
              <button
                type="button"
                onClick={() => void copy()}
                aria-label={`${t("common.copy")}: ${p.user_id}`}
                className="-my-2 -mr-1 inline-flex min-h-tap items-center gap-1.5 rounded-2 px-1 text-fg-2 tabular-nums transition-colors active:text-fg-1"
              >
                {shortId(p.user_id)}
                <Copy size={14} aria-hidden />
              </button>
            ) : (
              <Skeleton className="h-5 w-32" />
            )}
          </Row>
          <Row label={t("mProfile.joined")}>{p ? <TimeText value={p.created_at} className="text-fg-2" /> : <Skeleton className="h-5 w-32" />}</Row>
        </Group>
      </Section>
      <p className="px-1 text-xs leading-relaxed text-fg-3">{t("mProfile.note")}</p>
      <UsernameSheet open={rename} current={p?.username ?? ""} onClose={() => setRename(false)} />
    </div>
  );
}

function Row({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <div className="flex min-h-14 items-center justify-between gap-3 px-4">
      <span className="shrink-0 text-base text-fg-1">{label}</span>
      <span className="min-w-0 text-right text-base">{children}</span>
    </div>
  );
}
