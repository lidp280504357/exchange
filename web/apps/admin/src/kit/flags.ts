import { adminApi, adminData } from "@exchange/core/api/admin";
import { useQuery } from "@tanstack/react-query";

// Pages behind a feature flag (the margin pages behind margin.enabled,
// A55): shown while the flag is on, hidden while it is off, unknown (a
// flag nobody set is off) or the flags cannot be read.

/** The flags list's query key, shared with the flags page. */
export const flagsKey = ["admin", "flags"];

const DEV_FLAGS = "admin.dev.flags";

/**
 * devOn reports a flag taken as on in development only: the flags listed,
 * comma-separated, in this browser's localStorage admin.dev.flags, to
 * preview pages behind a flag the server keeps off. A production build
 * leaves it out.
 */
function devOn(key: string): boolean {
  if (!import.meta.env.DEV) return false;
  return (globalThis.localStorage?.getItem(DEV_FLAGS) ?? "").split(",").some((k) => k.trim() === key);
}

/** useFlagCheck returns whether a flag is on: undefined while the flags load. */
export function useFlagCheck(): (key: string) => boolean | undefined {
  const q = useQuery({
    queryKey: flagsKey,
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/flags")).items,
    staleTime: 60_000,
  });
  return (key) => {
    if (devOn(key)) return true;
    if (q.isPending) return undefined;
    return q.data?.some((f) => f.key === key && f.enabled) ?? false;
  };
}
