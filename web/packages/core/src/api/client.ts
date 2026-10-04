import createClient from "openapi-fetch";
import { mayHaveSession, useSession, type Session } from "../session/store";
import { errorFrom, toApiError } from "./errors";
import type { paths as AccountPaths } from "./gen/account";
import type { paths as AuthPaths } from "./gen/auth";
import type { paths as DerivativesPaths } from "./gen/derivatives";
import type { paths as GatewayPaths } from "./gen/gateway";
import type { paths as MarketPaths } from "./gen/market";
import type { paths as NotificationPaths } from "./gen/notification";
import type { paths as PlatformPaths } from "./gen/platform";
import type { paths as TradingPaths } from "./gen/trading";
import type { paths as UserPaths } from "./gen/user";
import type { paths as WalletPaths } from "./gen/wallet";

// Typed clients of the public API (api/openapi, generated into ./gen).
// Every site serves /v1 itself (nginx proxies it to the gateway), so the
// API is same-origin; unit tests run without a location.

type TokenResponse = {
  user_id: string;
  session_id: string;
  access_token: string;
  scope: string;
  expires_at: string;
};

/** sessionFrom turns a token response into the in-memory session. */
export function sessionFrom(t: TokenResponse): Session {
  return {
    accessToken: t.access_token,
    userId: t.user_id,
    sessionId: t.session_id,
    scope: t.scope,
    expiresAt: Date.parse(t.expires_at),
  };
}

const baseUrl = globalThis.location?.origin ?? "http://localhost";

let refreshing: Promise<Session | null> | null = null;

/**
 * refresh rotates the refresh cookie once, however many requests ask for it
 * at the same time. A 401 AUTH_TOKEN_EXPIRED from refresh means another tab
 * rotated the cookie a moment ago: wait and retry once (§5.2). A failure
 * does not sign out a session set meanwhile (a login finishing while the
 * start-up refresh was still running).
 */
export function refresh(): Promise<Session | null> {
  if (!refreshing) {
    refreshing = (async () => {
      const before = useSession.getState().session;
      for (let attempt = 0; attempt < 2; attempt++) {
        const res = await fetch(`${baseUrl}/v1/auth/token/refresh`, { method: "POST", credentials: "include" });
        if (res.ok) {
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

/**
 * authFetch adds the access token and, when it expired (or the account
 * status changed, which the gateway reports the same way), refreshes and
 * retries the request once.
 */
async function authFetch(input: Request): Promise<Response> {
  const retry = input.clone();
  const send = (req: Request, token?: string) => {
    if (token) req.headers.set("Authorization", `Bearer ${token}`);
    return fetch(req);
  };
  const res = await send(input, useSession.getState().session?.accessToken);
  if (res.status !== 401) return res;
  const err = await toApiError(res);
  if (err.code !== "AUTH_TOKEN_EXPIRED") {
    if (err.code === "AUTH_SESSION_REVOKED") useSession.getState().set(null);
    return res;
  }
  const s = await refresh();
  return s ? send(retry, s.accessToken) : res;
}

const options = { baseUrl, credentials: "include" as const, fetch: authFetch };

export const authApi = createClient<AuthPaths>(options);
export const userApi = createClient<UserPaths>(options);
export const accountApi = createClient<AccountPaths>(options);
export const marketApi = createClient<MarketPaths>(options);
export const notificationApi = createClient<NotificationPaths>(options);
export const gatewayApi = createClient<GatewayPaths>(options);
export const tradingApi = createClient<TradingPaths>(options);
export const walletApi = createClient<WalletPaths>(options);
export const derivativesApi = createClient<DerivativesPaths>(options);
export const platformApi = createClient<PlatformPaths>(options);

/**
 * unwrap turns an openapi-fetch result into data or a thrown ApiError;
 * openapi-fetch has already read and parsed the error body into `error`.
 */
export async function unwrap<T>(p: Promise<{ data?: T; error?: unknown; response: Response }>): Promise<T> {
  const { data, error, response } = await p;
  if (!response.ok) throw errorFrom(response.status, response.statusText, error);
  return data as T;
}

/**
 * restoreSession tries the refresh cookie once at start-up (when a session
 * may exist) and marks the session store as settled either way.
 */
export async function restoreSession(): Promise<void> {
  try {
    if (mayHaveSession()) await refresh();
  } finally {
    useSession.getState().doneRestoring();
  }
}

/** signOut ends the session on the server and forgets it here. */
export async function signOut(): Promise<void> {
  try {
    await authApi.POST("/v1/auth/logout");
  } finally {
    useSession.getState().set(null);
  }
}
