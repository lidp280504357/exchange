import createClient, { type Middleware } from "openapi-fetch";
import type { components, paths } from "./gen/admin";

export type Admin = components["schemas"]["Admin"];
export type Permission = Admin["permissions"][number];
export type Withdrawal = components["schemas"]["Withdrawal"];
export type UserView = components["schemas"]["UserView"];
export type Asset = components["schemas"]["Asset"];
export type Pair = components["schemas"]["Pair"];
export type Flag = components["schemas"]["Flag"];
export type Approval = components["schemas"]["Approval"];
export type AuditEntry = components["schemas"]["AuditEntry"];

// ApiError carries the unified error body (requirements §7.1).
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public traceId = "",
  ) {
    super(message);
  }
}

type ErrorBody = { code?: string; message?: string; trace_id?: string };

export function errorFrom(status: number, body: unknown): ApiError {
  const b: ErrorBody = typeof body === "object" && body !== null ? (body as ErrorBody) : {};
  const code = b.code ?? (status === 429 ? "COMMON_RATE_LIMITED" : status >= 500 ? "COMMON_UNAVAILABLE" : "COMMON_INTERNAL");
  return new ApiError(status, code, b.message ?? `HTTP ${status}`, b.trace_id ?? "");
}

// Every write carries the CSRF header, which a cross-site form cannot set.
const csrf: Middleware = {
  onRequest({ request }) {
    if (request.method !== "GET" && request.method !== "HEAD") request.headers.set("X-Admin-CSRF", "1");
    return request;
  },
};

// The API is same-origin; unit tests run without a location.
export const api = createClient<paths>({ baseUrl: globalThis.location?.origin ?? "http://localhost", credentials: "same-origin" });
api.use(csrf);

// data returns a response's body or throws its ApiError.
export function data<T>(res: { data?: T; error?: unknown; response: Response }): T {
  if (!res.response.ok || res.error !== undefined) throw errorFrom(res.response.status, res.error);
  return res.data as T;
}

const messages: Record<string, string> = {
  ADMIN_LOGIN_FAILED: "邮箱、密码或验证码错误（每个验证码只能用一次）",
  ADMIN_LOCKED: "连续失败次数过多，账号已锁定 15 分钟",
  ADMIN_UNAUTHORIZED: "请先登录",
  ADMIN_FORBIDDEN: "你的角色没有这项权限",
  ADMIN_SELF_APPROVAL: "不能审批自己发起的申请，需要另一位管理员",
  ADMIN_APPROVAL_DECIDED: "该申请已经处理过了",
  ADMIN_CSRF: "请求缺少安全头，请刷新页面",
  COMMON_RATE_LIMITED: "操作太频繁，请稍后再试",
  COMMON_NOT_FOUND: "没有找到",
  COMMON_UNAVAILABLE: "服务暂时不可用，请稍后再试",
  USER_STATUS_TRANSITION_INVALID: "账户当前状态不能直接改成这个状态",
  INSTRUMENT_STATUS_TRANSITION_INVALID: "交易对当前状态不能直接改成这个状态",
  LEDGER_ADJUSTMENT_DISABLED: "手动调账开关（ledger.manual_adjustment）未开启",
};

// describe turns an error into a sentence for the operator.
export function describe(err: unknown): string {
  if (err instanceof ApiError) {
    const text = messages[err.code] ?? err.message;
    return err.traceId ? `${text}（${err.code}，trace ${err.traceId.slice(0, 8)}）` : `${text}（${err.code}）`;
  }
  return err instanceof Error ? err.message : String(err);
}
