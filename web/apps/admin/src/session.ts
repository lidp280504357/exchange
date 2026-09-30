import { ApiError } from "@exchange/core";
import { adminApi, adminData, type Admin } from "@exchange/core/api/admin";
import { useQuery } from "@tanstack/react-query";

/** useMe returns the signed-in administrator; a 401 means signed out (null). */
export function useMe() {
  return useQuery({
    queryKey: ["admin", "me"],
    queryFn: async (): Promise<Admin | null> => {
      try {
        return adminData(await adminApi.GET("/admin/v1/me"));
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) return null;
        throw err;
      }
    },
    staleTime: 60_000,
    retry: false,
  });
}
