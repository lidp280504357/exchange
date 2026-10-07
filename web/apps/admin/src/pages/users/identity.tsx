import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { Avatar, Button } from "@exchange/ui";
import { useTranslation } from "react-i18next";
import { DangerAction, lastFour } from "../../kit/actions";

// A user's username and avatar in the console (design 2026-10-07, avatars
// and usernames §1.6, I3): shown in the list and on the user's page, reset
// by an administrator with users.status when they break the rules (one
// person, audited; the user is told in the app).

type UserSummary = AdminSchemas["UserSummary"];

/** UserIdentity is a user's avatar (the uploaded one, else the default) and username. */
export function UserIdentity({ user, size = 24 }: { user: UserSummary; size?: number }) {
  const src = (size > 64 ? user.avatar_url : user.avatar_thumb_url) ?? undefined;
  return (
    <span className="inline-flex min-w-0 items-center gap-2" data-testid="user-identity">
      <Avatar name={user.username || user.id} src={src} size={size} />
      <span className="truncate font-mono text-xs text-fg-1">{user.username || "—"}</span>
    </span>
  );
}

/** ProfileResets gives a user a drawn username, or the default avatar back. */
export function ProfileResets({ user }: { user: UserSummary }) {
  const { t } = useTranslation();
  const name = user.username || user.id;
  const invalidate = [["admin", "user", user.id], ["admin", "users"]];
  return (
    <div className="grid grid-cols-2 gap-2">
      <DangerAction
        trigger={(open) => (
          <Button size="sm" variant="secondary" onClick={open} block data-testid="user-reset-username">
            {t("admin.user.resetUsername")}
          </Button>
        )}
        title={t("admin.user.resetUsernameTitle", { name })}
        description={t("admin.user.resetUsernameHint")}
        target={<span className="font-mono text-xs">{name}</span>}
        confirmWord={lastFour(user.id)}
        run={async (reason) =>
          adminData(await adminApi.POST("/admin/v1/users/{id}/username-reset", { params: { path: { id: user.id } }, body: { reason } }))
        }
        success={(u) => t("admin.user.usernameReset", { name: (u as UserSummary).username })}
        invalidate={invalidate}
      />
      <DangerAction
        trigger={(open) => (
          <Button
            size="sm"
            variant="secondary"
            onClick={open}
            block
            disabled={!user.avatar_url}
            title={user.avatar_url ? undefined : t("admin.user.noAvatar")}
            data-testid="user-reset-avatar"
          >
            {t("admin.user.resetAvatar")}
          </Button>
        )}
        title={t("admin.user.resetAvatarTitle", { name })}
        description={t("admin.user.resetAvatarHint")}
        target={<Avatar name={name} src={user.avatar_url ?? undefined} size={48} />}
        confirmWord={lastFour(user.id)}
        run={async (reason) =>
          adminData(await adminApi.POST("/admin/v1/users/{id}/avatar-reset", { params: { path: { id: user.id } }, body: { reason } }))
        }
        success={t("admin.user.avatarReset")}
        invalidate={invalidate}
      />
    </div>
  );
}
