import { authApi, sessionFrom, unwrap } from "../api/client";
import { ApiError } from "../api/errors";
import type { components } from "../api/gen/auth";
import { deviceId, useSession } from "../session/store";
import type { OtpChannel } from "./otp";

// Password sign-in (requirements §6.2, ADR-0009). Failures count per
// identifier: from the third the server wants a human check
// (AUTH_CAPTCHA_REQUIRED) and the tenth locks the identifier for 15
// minutes (AUTH_ACCOUNT_LOCKED). After 7 days without a successful login
// a right password gets 403 AUTH_LOGIN_CHALLENGE_REQUIRED instead: a
// LOGIN_CHALLENGE code finishes it at /v1/auth/login/challenge.

export type Tokens = components["schemas"]["Tokens"];

/** Where a login-challenge code can go: a channel and its masked target. */
export type ChallengeChannel = { channel: OtpChannel; target: string };

export type LoginChallenge = {
  id: string;
  channels: ChallengeChannel[];
  /** Epoch ms; 0 when the server did not say. */
  expiresAt: number;
};

/** challengeFrom reads the challenge out of an AUTH_LOGIN_CHALLENGE_REQUIRED error. */
export function challengeFrom(err: unknown): LoginChallenge | null {
  if (!(err instanceof ApiError) || err.code !== "AUTH_LOGIN_CHALLENGE_REQUIRED") return null;
  const id = err.details.login_challenge_id;
  if (typeof id !== "string" || !id) return null;
  const raw = Array.isArray(err.details.channels) ? (err.details.channels as unknown[]) : [];
  const channels: ChallengeChannel[] = [];
  for (const c of raw) {
    if (typeof c !== "object" || c === null) continue;
    const { channel, target } = c as { channel?: unknown; target?: unknown };
    if ((channel === "EMAIL" || channel === "SMS") && typeof target === "string") channels.push({ channel, target });
  }
  // Email first: SMS may be switched off (auth.sms).
  channels.sort((a, b) => (a.channel === b.channel ? 0 : a.channel === "EMAIL" ? -1 : 1));
  const at = typeof err.details.expires_at === "string" ? Date.parse(err.details.expires_at) : NaN;
  return { id, channels, expiresAt: Number.isNaN(at) ? 0 : at };
}

/** The default lock of AUTH_ACCOUNT_LOCKED when the error does not say (15 minutes). */
export const LOCK_SECONDS = 15 * 60;

/** lockSeconds is how long an AUTH_ACCOUNT_LOCKED error asks to wait. */
export function lockSeconds(err: unknown): number {
  if (!(err instanceof ApiError)) return 0;
  const s = Number(err.details.retry_after_seconds);
  return Number.isFinite(s) && s > 0 ? Math.ceil(s) : LOCK_SECONDS;
}

export type PasswordLogin = { identifier: string; password: string; captchaToken?: string };

/** loginWithPassword signs in with a normalized identifier and a password. */
export function loginWithPassword(input: PasswordLogin): Promise<Tokens> {
  return unwrap(
    authApi.POST("/v1/auth/login/password", {
      body: {
        identifier: input.identifier,
        password: input.password,
        captcha_token: input.captchaToken || undefined,
        device_id: deviceId(),
      },
    }),
  );
}

/** completeLoginChallenge finishes a challenged login with its LOGIN_CHALLENGE ticket. */
export function completeLoginChallenge(challengeId: string, ticket: string): Promise<Tokens> {
  return unwrap(
    authApi.POST("/v1/auth/login/challenge", { body: { otp_ticket: ticket, login_challenge_id: challengeId, device_id: deviceId() } }),
  );
}

/** loginWithTicket signs in by code: a LOGIN ticket instead of the password. */
export function loginWithTicket(ticket: string): Promise<Tokens> {
  return unwrap(authApi.POST("/v1/auth/login/complete", { body: { otp_ticket: ticket, device_id: deviceId() } }));
}

/** signIn keeps the session of a token response (the WebSocket follows it). */
export function signIn(tokens: Tokens): void {
  useSession.getState().set(sessionFrom(tokens));
}

// The sign-in page's state machine, shared by both user sites.

export type LoginState = {
  step: "password" | "challenge";
  /** A human check must come with the next attempt. */
  captcha: boolean;
  /** Bumped whenever the widget must produce a fresh token (each works once). */
  generation: number;
  /** The last attempt only lacked a token: send it again once one arrives. */
  retryOnToken: boolean;
  /** Epoch ms until which password sign-in is locked (0 = not locked). */
  lockedUntil: number;
  /** The identifier the lock is on ("" = whichever was tried): the server counts failures per identifier. */
  lockedId: string;
  challenge: LoginChallenge | null;
  /** The challenge ran out; the password step says so. */
  challengeExpired: boolean;
};

export const initialLoginState: LoginState = {
  step: "password",
  captcha: false,
  generation: 0,
  retryOnToken: false,
  lockedUntil: 0,
  lockedId: "",
  challenge: null,
  challengeExpired: false,
};

export type LoginAction =
  /** An attempt starts (retryOnToken is spent). */
  | { type: "submit" }
  /** The password step failed with this error at `now` (epoch ms), for this (normalized) identifier. */
  | { type: "failed"; error: unknown; now: number; identifier?: string }
  /** Finishing the challenge failed. */
  | { type: "challengeFailed"; error: unknown }
  /** Back from the challenge to the password. */
  | { type: "back" };

export function loginReducer(s: LoginState, a: LoginAction): LoginState {
  switch (a.type) {
    case "submit":
      return { ...s, retryOnToken: false, challengeExpired: false };
    case "failed": {
      const challenge = challengeFrom(a.error);
      if (challenge) return { ...s, step: "challenge", challenge, retryOnToken: false };
      const code = a.error instanceof ApiError ? a.error.code : "";
      // Whatever happened, a token that went with the attempt is spent.
      const fresh = s.captcha ? s.generation + 1 : s.generation;
      switch (code) {
        case "AUTH_CAPTCHA_REQUIRED":
          return { ...s, captcha: true, generation: s.generation + 1, retryOnToken: true };
        case "AUTH_CAPTCHA_FAILED":
          return { ...s, captcha: true, generation: s.generation + 1, retryOnToken: false };
        case "AUTH_ACCOUNT_LOCKED":
          return {
            ...s, generation: fresh, retryOnToken: false, lockedUntil: a.now + lockSeconds(a.error) * 1000, lockedId: a.identifier ?? "",
          };
        default:
          return { ...s, generation: fresh, retryOnToken: false };
      }
    }
    case "challengeFailed":
      if (a.error instanceof ApiError && a.error.code === "AUTH_LOGIN_CHALLENGE_INVALID") {
        return { ...s, step: "password", challenge: null, challengeExpired: true };
      }
      return s;
    case "back":
      return { ...s, step: "password", challenge: null, challengeExpired: false };
  }
}

/**
 * lockLeft is how many seconds password sign-in stays locked for this
 * (normalized) identifier at `now`: 0 when it is not locked, or when the
 * lock is on another identifier.
 */
export function lockLeft(s: Pick<LoginState, "lockedUntil" | "lockedId">, identifier: string, now: number): number {
  if (s.lockedUntil <= now) return 0;
  if (s.lockedId && identifier && s.lockedId !== identifier) return 0;
  return Math.ceil((s.lockedUntil - now) / 1000);
}
