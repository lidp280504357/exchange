import { adminApi, adminData } from "@exchange/core/api/admin";
import { useQuery } from "@tanstack/react-query";

// The platform profile as the sites read it (design 2026-10-04 §4.1): the
// platform page edits it, and the content pages ask it which mode the
// exchange is in. Reading it takes reports.read, which all four roles have.

export const platformKey = ["admin", "platform"];

/** usePlatformProfile reads the platform profile. */
export function usePlatformProfile() {
  return useQuery({ queryKey: platformKey, queryFn: async () => adminData(await adminApi.GET("/admin/v1/platform/profile")) });
}

/** The exchange's mode now: TEST while the profile's test mode is on, FORMAL live; null until the profile is read. */
export function useSiteMode(): "TEST" | "FORMAL" | null {
  const q = usePlatformProfile();
  if (!q.data) return null;
  return q.data.test_mode.enabled ? "TEST" : "FORMAL";
}
