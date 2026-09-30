import { describe, expect, it } from "vitest";
import { ApiError } from "../api/errors";
import { challengeFrom, initialLoginState, LOCK_SECONDS, lockLeft, lockSeconds, loginReducer, type LoginState } from "./login";

const err = (code: string, details: Record<string, unknown> = {}) => new ApiError(403, code, code, details);

const challengeError = err("AUTH_LOGIN_CHALLENGE_REQUIRED", {
  login_challenge_id: "0192e4c8-7a3b-7c1d-9e2f-3a4b5c6d7e8f",
  expires_at: "2026-09-30T12:10:00Z",
  channels: [
    { channel: "SMS", target: "+86138****1234" },
    { channel: "EMAIL", target: "a***@example.com" },
    { channel: "PIGEON", target: "x" },
  ],
});

describe("login challenge", () => {
  it("reads the challenge, email first", () => {
    expect(challengeFrom(challengeError)).toEqual({
      id: "0192e4c8-7a3b-7c1d-9e2f-3a4b5c6d7e8f",
      channels: [
        { channel: "EMAIL", target: "a***@example.com" },
        { channel: "SMS", target: "+86138****1234" },
      ],
      expiresAt: Date.parse("2026-09-30T12:10:00Z"),
    });
    expect(challengeFrom(err("AUTH_LOGIN_CHALLENGE_REQUIRED"))).toBeNull();
    expect(challengeFrom(err("AUTH_PASSWORD_INVALID"))).toBeNull();
    expect(challengeFrom(new Error("x"))).toBeNull();
  });

  it("knows how long a lock lasts", () => {
    expect(lockSeconds(err("AUTH_ACCOUNT_LOCKED", { retry_after_seconds: 61.2 }))).toBe(62);
    expect(lockSeconds(err("AUTH_ACCOUNT_LOCKED"))).toBe(LOCK_SECONDS);
    expect(lockSeconds("nope")).toBe(0);
  });
});

describe("login flow", () => {
  const fail = (s: LoginState, code: string, details?: Record<string, unknown>, now = 1_000) =>
    loginReducer(s, { type: "failed", error: err(code, details), now });

  it("asks for a human check from the third failure and retries once a token arrives", () => {
    let s = fail(initialLoginState, "AUTH_PASSWORD_INVALID");
    expect(s).toMatchObject({ captcha: false, generation: 0, retryOnToken: false });
    s = fail(s, "AUTH_CAPTCHA_REQUIRED");
    expect(s).toMatchObject({ captcha: true, generation: 1, retryOnToken: true });
    s = loginReducer(s, { type: "submit" });
    expect(s.retryOnToken).toBe(false);
    // The token went with a wrong password: the widget must renew it.
    s = fail(s, "AUTH_PASSWORD_INVALID");
    expect(s).toMatchObject({ captcha: true, generation: 2, retryOnToken: false });
    s = fail(s, "AUTH_CAPTCHA_FAILED");
    expect(s).toMatchObject({ generation: 3, retryOnToken: false });
  });

  it("locks for the time the server gives", () => {
    const s = fail(initialLoginState, "AUTH_ACCOUNT_LOCKED", { retry_after_seconds: 900 }, 5_000);
    expect(s.lockedUntil).toBe(5_000 + 900_000);
    expect(s.step).toBe("password");
  });

  it("keeps the lock on the identifier that was locked", () => {
    const s = loginReducer(initialLoginState, {
      type: "failed", error: err("AUTH_ACCOUNT_LOCKED", { retry_after_seconds: 90 }), now: 0, identifier: "ann@example.com",
    });
    expect(s.lockedId).toBe("ann@example.com");
    expect(lockLeft(s, "ann@example.com", 0)).toBe(90);
    expect(lockLeft(s, "ann@example.com", 89_500)).toBe(1);
    expect(lockLeft(s, "ann@example.com", 90_000)).toBe(0);
    // Failures count per identifier: another account is not locked.
    expect(lockLeft(s, "bob@example.com", 1_000)).toBe(0);
    expect(lockLeft(initialLoginState, "ann@example.com", 0)).toBe(0);
  });

  it("moves to the challenge and back", () => {
    let s = loginReducer(initialLoginState, { type: "failed", error: challengeError, now: 0 });
    expect(s.step).toBe("challenge");
    expect(s.challenge?.channels[0]?.channel).toBe("EMAIL");
    // A wrong code keeps the challenge; an expired challenge ends it.
    expect(loginReducer(s, { type: "challengeFailed", error: err("AUTH_TICKET_INVALID") })).toBe(s);
    const expired = loginReducer(s, { type: "challengeFailed", error: err("AUTH_LOGIN_CHALLENGE_INVALID") });
    expect(expired).toMatchObject({ step: "password", challenge: null, challengeExpired: true });
    expect(loginReducer(expired, { type: "submit" }).challengeExpired).toBe(false);
    s = loginReducer(s, { type: "back" });
    expect(s).toMatchObject({ step: "password", challenge: null, challengeExpired: false });
  });
});
