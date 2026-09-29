import { useQuery } from "@tanstack/react-query";
import { ApiError, api, data, type Admin, type Permission } from "./api/client";

// can reports whether an administrator holds a permission.
export function can(admin: Pick<Admin, "permissions"> | undefined, perm: Permission): boolean {
  return admin?.permissions.includes(perm) ?? false;
}

// useMe returns the signed-in administrator; a 401 means signed out
// (data null) rather than an error.
export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: async (): Promise<Admin | null> => {
      try {
        return data(await api.GET("/admin/v1/me"));
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) return null;
        throw err;
      }
    },
    staleTime: 60_000,
    retry: false,
  });
}

export const roleNames: Record<Admin["role"], string> = {
  ADMIN: "管理员",
  OPERATOR: "运营",
  FINANCE: "财务",
  AUDITOR: "审计",
};
