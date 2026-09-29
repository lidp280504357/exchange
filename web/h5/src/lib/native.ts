// The desktop app (Tauri 2, desktop/) runs this H5 in a WebView whose
// origin differs from the API's (requirements §6.6): cookies are not sent
// across sites, so there it calls the API at VITE_API_ORIGIN with bearer
// tokens as an APP client, and keeps the refresh token in the system's
// secure storage through the app's commands, sending it in the body.

type Invoke = (cmd: string, args?: Record<string, unknown>) => Promise<unknown>;

function tauri(): Invoke | undefined {
  return (globalThis as { __TAURI_INTERNALS__?: { invoke: Invoke } }).__TAURI_INTERNALS__?.invoke;
}

// native reports whether the H5 runs inside the desktop app.
export const native: boolean = tauri() !== undefined;

// apiOrigin is where the API is: the page's own origin in a browser, the
// configured one in the desktop app.
export const apiOrigin: string = native
  ? (import.meta.env.VITE_API_ORIGIN ?? "https://astras.vip")
  : (globalThis.location?.origin ?? "http://localhost");

// wsURL is the WebSocket endpoint next to the API.
export function wsURL(): string {
  return apiOrigin.replace(/^http/, "ws") + "/v1/ws";
}

// refreshTokens keeps the desktop app's refresh token in the system's
// secure storage (Keychain, Credential Manager, Secret Service).
export const refreshTokens = {
  async get(): Promise<string | null> {
    const t = await tauri()?.("refresh_token_get");
    return typeof t === "string" && t !== "" ? t : null;
  },
  async set(token: string): Promise<void> {
    await tauri()?.("refresh_token_set", { token });
  },
  async clear(): Promise<void> {
    await tauri()?.("refresh_token_clear");
  },
};
