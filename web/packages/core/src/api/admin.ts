import createClient, { type Middleware } from "openapi-fetch";
import { errorFrom } from "./errors";
import type { components, paths } from "./gen/admin";

// The admin console's API (api/admin/admin.yaml), served by admin-service
// at /admin/v1 on admin.astras.vip. The session is an HttpOnly cookie
// scoped to /admin/; every write carries X-Admin-CSRF, which a cross-site
// form cannot set.

export type AdminSchemas = components["schemas"];
export type Admin = AdminSchemas["Admin"];
export type Permission = Admin["permissions"][number];

const csrf: Middleware = {
  onRequest({ request }) {
    if (request.method !== "GET" && request.method !== "HEAD") request.headers.set("X-Admin-CSRF", "1");
    return request;
  },
};

export const adminApi = createClient<paths>({ baseUrl: globalThis.location?.origin ?? "http://localhost", credentials: "same-origin" });
adminApi.use(csrf);

/** adminData returns a response's body or throws its ApiError. */
export function adminData<T>(res: { data?: T; error?: unknown; response: Response }): T {
  if (!res.response.ok || res.error !== undefined) throw errorFrom(res.response.status, res.response.statusText, res.error);
  return res.data as T;
}

/** can reports whether an administrator holds a permission. */
export function can(admin: Pick<Admin, "permissions"> | null | undefined, perm: Permission): boolean {
  return admin?.permissions.includes(perm) ?? false;
}
