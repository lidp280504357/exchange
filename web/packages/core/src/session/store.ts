import { create } from "zustand";

// The access token lives only in memory (requirements §6.6); the refresh
// token is an HttpOnly cookie the browser sends to /v1/auth/token/refresh.
// Components read the session through selectors (useSession((s) => ...)),
// so a token refresh re-renders only what shows the user.
export type Session = {
  accessToken: string;
  userId: string;
  sessionId: string;
  scope: "full" | "read" | string;
  expiresAt: number; // epoch ms
};

// The page cannot see the HttpOnly cookie, so it remembers whether a
// session may exist, sparing anonymous visitors a refresh that must fail.
const HINT = "exchange.signed_in";

function storeHint(on: boolean) {
  try {
    if (on) localStorage.setItem(HINT, "1");
    else localStorage.removeItem(HINT);
  } catch {
    // no storage (tests, private modes): always try to refresh
  }
}

/** mayHaveSession reports whether a refresh cookie may exist. */
export function mayHaveSession(): boolean {
  try {
    return localStorage.getItem(HINT) === "1";
  } catch {
    return true;
  }
}

type SessionState = {
  session: Session | null;
  /** restoring is true until the first refresh attempt at start-up ends. */
  restoring: boolean;
  set: (s: Session | null) => void;
  doneRestoring: () => void;
};

export const useSession = create<SessionState>((set) => ({
  session: null,
  restoring: true,
  set: (session) => {
    storeHint(session !== null);
    set({ session });
  },
  doneRestoring: () => set({ restoring: false }),
}));

/** Selectors, so components subscribe to one field. */
export const selectSignedIn = (s: SessionState) => s.session !== null;
export const selectUserId = (s: SessionState) => s.session?.userId ?? "";
export const selectRestoring = (s: SessionState) => s.restoring;

const DEVICE = "exchange.device_id";

/** deviceId is a random ID the browser keeps for good (§6.1). */
export function deviceId(): string {
  try {
    let id = localStorage.getItem(DEVICE);
    if (!id) {
      id = `web-${crypto.randomUUID()}`;
      localStorage.setItem(DEVICE, id);
    }
    return id;
  } catch {
    return "web-ephemeral";
  }
}
