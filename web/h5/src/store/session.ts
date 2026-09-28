import { create } from "zustand";

// The access token lives only in memory (§6.6); the refresh token is an
// HttpOnly cookie the browser sends to /v1/auth/token/refresh.
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

// mayHaveSession reports whether a refresh cookie may exist.
export function mayHaveSession(): boolean {
  try {
    return localStorage.getItem(HINT) === "1";
  } catch {
    return true;
  }
}

type SessionState = {
  session: Session | null;
  // restoring is true until the first refresh attempt at start-up ends.
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
