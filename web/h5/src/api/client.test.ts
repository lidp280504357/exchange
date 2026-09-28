import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useSession } from "../store/session";
import { ApiError, accountApi, refresh, unwrap } from "./client";

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });

const tokens = (token: string) => ({
  user_id: "u-1",
  session_id: "s-1",
  access_token: token,
  scope: "full",
  expires_at: new Date(Date.now() + 15 * 60_000).toISOString(),
});

describe("api client", () => {
  let calls: { url: string; auth: string | null }[];

  beforeEach(() => {
    calls = [];
    useSession.getState().set({ accessToken: "old", userId: "u-1", sessionId: "s-1", scope: "full", expiresAt: Date.now() });
  });
  afterEach(() => vi.unstubAllGlobals());

  it("maps error bodies to ApiError with the server's code", async () => {
    vi.stubGlobal("fetch", async () =>
      json(422, { code: "LEDGER_INSUFFICIENT_BALANCE", message: "insufficient balance", trace_id: "t1", details: { transfer_id: "x" } }),
    );
    const err = await unwrap(accountApi.GET("/v1/account/balances")).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 422, code: "LEDGER_INSUFFICIENT_BALANCE", traceId: "t1", details: { transfer_id: "x" } });
  });

  it("keeps the status when the body is not the unified error", async () => {
    vi.stubGlobal("fetch", async () => new Response("<html>bad gateway</html>", { status: 502, statusText: "Bad Gateway" }));
    const err = await unwrap(accountApi.GET("/v1/account/balances")).catch((e: unknown) => e);
    expect(err).toMatchObject({ status: 502, code: "COMMON_UNAVAILABLE" });
  });

  it("refreshes an expired token once and retries the request", async () => {
    vi.stubGlobal("fetch", async (input: Request | string) => {
      const req = typeof input === "string" ? new Request(input) : input;
      calls.push({ url: new URL(req.url).pathname, auth: req.headers.get("Authorization") });
      if (req.url.endsWith("/v1/auth/token/refresh")) return json(200, tokens("new"));
      if (req.headers.get("Authorization") === "Bearer old") return json(401, { code: "AUTH_TOKEN_EXPIRED", message: "expired" });
      return json(200, { balances: [] });
    });
    const data = await unwrap(accountApi.GET("/v1/account/balances"));
    expect(data).toEqual({ balances: [] });
    expect(calls.map((c) => `${c.url} ${c.auth ?? ""}`)).toEqual([
      "/v1/account/balances Bearer old",
      "/v1/auth/token/refresh ",
      "/v1/account/balances Bearer new",
    ]);
    expect(useSession.getState().session?.accessToken).toBe("new");
  });

  it("signs out when the session was revoked", async () => {
    vi.stubGlobal("fetch", async () => json(401, { code: "AUTH_SESSION_REVOKED", message: "revoked" }));
    await expect(unwrap(accountApi.GET("/v1/account/balances"))).rejects.toMatchObject({ code: "AUTH_SESSION_REVOKED" });
    expect(useSession.getState().session).toBeNull();
  });

  it("shares one refresh between concurrent callers", async () => {
    let refreshes = 0;
    vi.stubGlobal("fetch", async () => {
      refreshes++;
      await new Promise((r) => setTimeout(r, 10));
      return json(200, tokens("fresh"));
    });
    const [a, b] = await Promise.all([refresh(), refresh()]);
    expect(refreshes).toBe(1);
    expect(a?.accessToken).toBe("fresh");
    expect(b).toBe(a);
  });

  it("does not sign out a session created while a failed refresh was running", async () => {
    useSession.getState().set(null);
    vi.stubGlobal("fetch", async () => {
      // A login completes while the start-up refresh is in flight.
      useSession.getState().set({ accessToken: "login", userId: "u-1", sessionId: "s-2", scope: "full", expiresAt: Date.now() });
      return json(401, { code: "AUTH_SESSION_REVOKED", message: "no cookie" });
    });
    expect(await refresh()).toBeNull();
    expect(useSession.getState().session?.accessToken).toBe("login");
  });
});
