import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The desktop app's mode (lib/native.ts), with a fake of its secure
// storage commands; the modules are loaded afresh so they see the bridge.

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const tokens = (access: string, refresh: string) => ({
  user_id: "u-1",
  session_id: "s-1",
  access_token: access,
  refresh_token: refresh,
  scope: "full",
  expires_at: new Date(Date.now() + 15 * 60_000).toISOString(),
});

describe("desktop app mode", () => {
  let stored: string | null;
  let invoked: string[];

  beforeEach(() => {
    vi.resetModules();
    stored = "r-0";
    invoked = [];
    vi.stubGlobal("__TAURI_INTERNALS__", {
      invoke: async (cmd: string, args?: { token?: string }) => {
        invoked.push(cmd);
        if (cmd === "refresh_token_get") return stored;
        if (cmd === "refresh_token_set") stored = args?.token ?? null;
        if (cmd === "refresh_token_clear") stored = null;
        return null;
      },
    });
    vi.stubGlobal("localStorage", { getItem: () => "desktop-d1", setItem: () => {}, removeItem: () => {} });
  });
  afterEach(() => vi.unstubAllGlobals());

  it("calls the configured API origin and signs in as an APP client", async () => {
    const seen: Request[] = [];
    vi.stubGlobal("fetch", async (req: Request) => {
      seen.push(req);
      return json(200, tokens("a-1", "r-1"));
    });
    const { authApi, unwrap } = await import("./client");
    await unwrap(authApi.POST("/v1/auth/login/password", { body: { identifier: "a@example.com", password: "x", device_id: "d" } as never }));
    expect(new URL(seen[0]!.url).origin).toBe("https://astras.vip");
    expect(seen[0]!.headers.get("X-Client-Type")).toBe("APP");
    expect(seen[0]!.credentials).toBe("omit");
    expect(stored).toBe("r-1");
  });

  it("refreshes with the stored token in the body and keeps the rotated one", async () => {
    let body: unknown;
    vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
      expect(url).toBe("https://astras.vip/v1/auth/token/refresh");
      expect((init.headers as Record<string, string>)["X-Client-Type"]).toBe("APP");
      body = JSON.parse(String(init.body));
      return json(200, tokens("a-2", "r-2"));
    });
    const { refresh } = await import("./client");
    const s = await refresh();
    expect(body).toEqual({ refresh_token: "r-0", device_id: "desktop-d1" });
    expect(s?.accessToken).toBe("a-2");
    expect(stored).toBe("r-2");
  });

  it("does not ask for tokens it cannot have, and drops a token the server refuses", async () => {
    stored = null;
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    let { refresh } = await import("./client");
    expect(await refresh()).toBeNull();
    expect(fetch).not.toHaveBeenCalled();

    vi.resetModules();
    stored = "r-old";
    vi.stubGlobal("fetch", async () => json(401, { code: "AUTH_SESSION_REVOKED", message: "revoked" }));
    ({ refresh } = await import("./client"));
    expect(await refresh()).toBeNull();
    expect(stored).toBeNull();
    expect(invoked).toContain("refresh_token_clear");
  });

  it("opens the WebSocket next to the API", async () => {
    const { wsURL } = await import("../lib/native");
    expect(wsURL()).toBe("wss://astras.vip/v1/ws");
  });
});
