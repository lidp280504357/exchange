import { refresh } from "./api/client";
import { MarketStore } from "./market/store";
import { useSession } from "./session/store";
import { WsClient } from "./ws/client";

/**
 * createLive builds the app's one WebSocket client and market store, and
 * keeps the connection signed in as the session changes (sign-in,
 * refreshed token, sign-out).
 */
export function createLive(): { ws: WsClient; market: MarketStore } {
  const ws = new WsClient({
    token: () => useSession.getState().session?.accessToken,
    refresh: async () => (await refresh())?.accessToken,
  });
  let token = useSession.getState().session?.accessToken;
  useSession.subscribe((s) => {
    const next = s.session?.accessToken;
    if (next === token) return;
    token = next;
    ws.authenticate(next);
  });
  return { ws, market: new MarketStore(ws) };
}
