import { adminApi, adminData } from "@exchange/core/api/admin";
import { useQuery } from "@tanstack/react-query";

// The reads of a user's page that its header and tabs share through the
// query cache.

/** useSecurity reads an account's sign-in security (identities masked). */
export function useSecurity(userId: string) {
  return useQuery({
    queryKey: ["admin", "user", userId, "security"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/users/{id}/security", { params: { path: { id: userId } } })),
  });
}

/**
 * useContacts holds an account's identities unmasked once revealed:
 * refetch() reveals them (each call is audited), and they are dropped as
 * soon as no component shows them. Its key is outside the user's own, so
 * that reloading the user never reveals again.
 */
export function useContacts(userId: string) {
  return useQuery({
    queryKey: ["admin", "contacts", userId],
    queryFn: async () => adminData(await adminApi.POST("/admin/v1/users/{id}/contacts/reveal", { params: { path: { id: userId } } })).identities,
    enabled: false,
    gcTime: 0,
    retry: false,
  });
}

/** useRisk reads the risk rules' assessments of an account, newest first. */
export function useRisk(userId: string) {
  return useQuery({
    queryKey: ["admin", "user", userId, "risk"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/users/{id}/risk", { params: { path: { id: userId }, query: { limit: 100 } } })).assessments,
  });
}

/** useHistory reads an account's status changes and accepted documents. */
export function useHistory(userId: string) {
  return useQuery({
    queryKey: ["admin", "user", userId, "history"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/users/{id}/history", { params: { path: { id: userId } } })),
  });
}
