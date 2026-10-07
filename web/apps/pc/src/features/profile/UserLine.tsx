import { selectUserId, useSession } from "@exchange/core";
import { useProfile } from "@exchange/core/user/profile";
import { Skeleton } from "@exchange/ui";
import { MyAvatar } from "@exchange/ui/profile/MyAvatar";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** shortUid keeps the ends of a user ID: "0192e4c8…7e8f". */
export function shortUid(id: string): string {
  return id.length > 14 ? `${id.slice(0, 8)}…${id.slice(-4)}` : id;
}

/**
 * UserLine is the signed-in user's avatar, username and UID: the top bar's
 * account menu and the account pages' side column start with it (design
 * 2026-10-07, avatars and usernames §1 #5). `after` puts something after
 * the UID (the side column's copy button; the top bar, on every page,
 * keeps to what it needs).
 */
export function UserLine({ size = 40, className, after }: { size?: number; className?: string; after?: (uid: string) => ReactNode }) {
  const { t } = useTranslation();
  const sessionUser = useSession(selectUserId);
  const profile = useProfile();
  const userId = profile.data?.user_id ?? sessionUser;
  return (
    <div className={className ?? "flex items-center gap-3"}>
      <MyAvatar size={size} />
      <div className="min-w-0">
        {profile.data ? (
          <div className="truncate text-sm font-medium text-fg-1" data-testid="my-username">
            {profile.data.username}
          </div>
        ) : (
          <Skeleton className="h-4 w-24" />
        )}
        <div className="mt-0.5 flex items-center gap-1 text-xs text-fg-3">
          <span>{t("common.uid")}</span>
          <span className="truncate tabular-nums">{shortUid(userId)}</span>
          {after?.(userId)}
        </div>
      </div>
    </div>
  );
}
