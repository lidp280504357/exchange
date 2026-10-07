import { selectUserId, useSession } from "@exchange/core";
import { avatarOf, useProfile } from "@exchange/core/user/profile";
import { Avatar } from "../components/Avatar";
import { Skeleton } from "../components/Skeleton";

/**
 * MyAvatar is the signed-in user's avatar on both sites (design 2026-10-07,
 * avatars and usernames §1 #5): the uploaded picture (the 64 px one up to
 * 32 px) or the built-in one of the user ID, the same everywhere. A
 * placeholder holds its place while the profile loads, so an uploaded
 * picture does not replace the built-in one on every page load; a profile
 * that failed shows the built-in one.
 */
export function MyAvatar({ size, className }: { size: number; className?: string }) {
  const userId = useSession(selectUserId);
  const profile = useProfile();
  if (profile.isPending && profile.fetchStatus !== "idle") {
    return <Skeleton round className={className} style={{ width: size, height: size }} />;
  }
  // The profile's ID is the session's; read first, it also serves a page rendered without a session (tests).
  const seed = profile.data?.user_id || userId || undefined;
  return <Avatar seed={seed} name={profile.data?.username} src={avatarOf(profile.data, size)} size={size} className={className} />;
}
