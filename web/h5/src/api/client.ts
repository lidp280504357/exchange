import createClient from "openapi-fetch";
import { deviceId } from "../lib/device";
import { apiOrigin, native, refreshTokens } from "../lib/native";
import { useSession, type Session } from "../store/session";
import type { paths as AccountPaths } from "./gen/account";
import type { paths as AuthPaths } from "./gen/auth";
import type { paths as GatewayPaths } from "./gen/gateway";
import type { paths as MarketPaths } from "./gen/market";
import type { paths as NotificationPaths } from "./gen/notification";
import type { paths as TradingPaths } from "./gen/trading";
import type { paths as UserPaths } from "./gen/user";
import type { paths as WalletPaths } from "./gen/wallet";

// ApiError carries the unified error body (§7.1).
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public details: Record<string, unknown> = {},
    public traceId = "",
  ) {
    super(message);
  }
}

type ErrorBody = { code?: string; message?: string; details?: Record<string, unknown>; trace_id?: string };

// errorFrom builds an ApiError from a parsed error body; anything else (a
// proxy's HTML page, an empty body) keeps only the HTTP status.
export function errorFrom(status: number, statusText: string, body: unknown): ApiError {
  const b: ErrorBody = typeof body === "object" && body !== null ? (body as ErrorBody) : {};
  const code = b.code ?? (status === 429 ? "COMMON_RATE_LIMITED" : status >= 500 ? "COMMON_UNAVAILABLE" : "COMMON_INTERNAL");
  return new ApiError(status, code, b.message ?? statusText, b.details ?? {}, b.trace_id ?? "");
}

// toApiError reads the error from a response whose body is still unread.
export async function toApiError(res: Response): Promise<ApiError> {
  let body: unknown;
  try {
    body = await res.clone().json();
  } catch {
    // not JSON
  }
  return errorFrom(res.status, res.statusText, body);
}

type TokenResponse = {
  user_id: string;
  session_id: string;
  access_token: string;
  scope: string;
  expires_at: string;
};

export function sessionFrom(t: TokenResponse): Session {
  return {
    accessToken: t.access_token,
    userId: t.user_id,
    sessionId: t.session_id,
    scope: t.scope,
    expiresAt: Date.parse(t.expires_at),
  };
}

// The API is same-origin in a browser; the desktop app calls it across
// origins (lib/native.ts).
const baseUrl = apiOrigin;

// refreshRequest asks for new tokens: browsers send the HttpOnly cookie,
// the desktop app the stored token in the body as an APP client.
async function refreshRequest(): Promise<Response | null> {
  if (!native) return fetch(`${baseUrl}/v1/auth/token/refresh`, { method: "POST", credentials: "include" });
  const token = await refreshTokens.get();
  if (!token) return null;
  return fetch(`${baseUrl}/v1/auth/token/refresh`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Client-Type": "APP" },
    body: JSON.stringify({ refresh_token: token, device_id: deviceId() }),
  });
}

// keepRefreshToken stores the refresh token an APP answer carries.
async function keepRefreshToken(res: Response): Promise<void> {
  try {
    const t = ((await res.clone().json()) as { refresh_token?: unknown }).refresh_token;
    if (typeof t === "string" && t !== "") await refreshTokens.set(t);
  } catch {
    // not a token answer
  }
}

let refreshing: Promise<Session | null> | null = null;

// refresh rotates the refresh cookie once, however many requests ask for
// it at the same time. A 401 AUTH_TOKEN_EXPIRED from refresh means another
// tab rotated the cookie a moment ago: wait and retry once (§5.2). A
// failure does not sign out a session that was set meanwhile (a login
// finishing while the start-up refresh was still running).
export function refresh(): Promise<Session | null> {
  if (!refreshing) {
    refreshing = (async () => {
      const before = useSession.getState().session;
      for (let attempt = 0; attempt < 2; attempt++) {
        const res = await refreshRequest();
        if (!res) break; // the desktop app holds no refresh token
        if (res.status === 401 && native) await refreshTokens.clear(); // it is no longer valid
        if (res.ok) {
          if (native) await keepRefreshToken(res);
          const s = sessionFrom((await res.json()) as TokenResponse);
          useSession.getState().set(s);
          return s;
        }
        const err = await toApiError(res);
        if (err.code !== "AUTH_TOKEN_EXPIRED") break;
        await new Promise((r) => setTimeout(r, 1500));
      }
      if (useSession.getState().session === before) useSession.getState().set(null);
      return null;
    })().finally(() => {
      refreshing = null;
    });
  }
  return refreshing;
}

// authFetch adds the access token and, when it expired (or the account
// status changed, which the gateway reports the same way), refreshes and
// retries the request once.
async function authFetch(input: Request): Promise<Response> {
  // The desktop app signs in as an APP client and keeps the refresh token
  // the answers carry.
  const signIn = native && new URL(input.url).pathname.startsWith("/v1/auth/");
  if (signIn) input.headers.set("X-Client-Type", "APP");
  const retry = input.clone();
  const send = async (req: Request, token?: string) => {
    if (token) req.headers.set("Authorization", `Bearer ${token}`);
    const res = await fetch(req);
    if (signIn && res.ok) await keepRefreshToken(res);
    return res;
  };
  const res = await send(input, useSession.getState().session?.accessToken);
  if (res.status !== 401) return res;
  const err = await toApiError(res);
  if (err.code !== "AUTH_TOKEN_EXPIRED") {
    if (err.code === "AUTH_SESSION_REVOKED") {
      useSession.getState().set(null);
      await refreshTokens.clear();
    }
    return res;
  }
  const s = await refresh();
  return s ? send(retry, s.accessToken) : res;
}

const options = { baseUrl, credentials: native ? ("omit" as const) : ("include" as const), fetch: authFetch };

export const authApi = createClient<AuthPaths>(options);
export const userApi = createClient<UserPaths>(options);
export const accountApi = createClient<AccountPaths>(options);
export const marketApi = createClient<MarketPaths>(options);
export const notificationApi = createClient<NotificationPaths>(options);
export const gatewayApi = createClient<GatewayPaths>(options);
export const tradingApi = createClient<TradingPaths>(options);
export const walletApi = createClient<WalletPaths>(options);

// unwrap turns an openapi-fetch result into data or a thrown ApiError.
// openapi-fetch has already read and parsed the error body into `error`.
export async function unwrap<T>(p: Promise<{ data?: T; error?: unknown; response: Response }>): Promise<T> {
  const { data, error, response } = await p;
  if (!response.ok) throw errorFrom(response.status, response.statusText, error);
  return data as T;
}
