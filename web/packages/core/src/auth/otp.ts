import { useCallback, useEffect, useState } from "react";
import { authApi, unwrap } from "../api/client";
import type { components } from "../api/gen/auth";
import { deviceId } from "../session/store";
import { useSettings } from "../settings/store";

// One-time codes (requirements §6.1): a code is requested for a scene
// after the human check, arrives by mail (or SMS where allowed) and is
// traded for a single-use ticket that the next call (register, reset,
// step-up, binding) spends. Both user sites drive it through useOtp.

export type OtpScene = components["schemas"]["Scene"];
export type OtpChannel = components["schemas"]["Channel"];

/** The server lets a target have one code a minute. */
export const OTP_RESEND_SECONDS = 60;

export type OtpTarget = {
  scene: OtpScene;
  channel?: OtpChannel;
  /** The email or phone number; omitted for STEP_UP, WITHDRAW_CONFIRM and LOGIN_CHALLENGE. */
  identifier?: string;
  /** For LOGIN_CHALLENGE. */
  loginChallengeId?: string;
};

/** requestOtp asks for a code and returns the challenge ID. */
export async function requestOtp(target: OtpTarget, captchaToken: string, language: string): Promise<string> {
  const res = await unwrap(
    authApi.POST("/v1/auth/otp/request", {
      body: {
        scene: target.scene,
        channel: target.channel ?? "EMAIL",
        identifier: target.identifier || undefined,
        login_challenge_id: target.loginChallengeId,
        captcha_token: captchaToken,
        device_id: deviceId(),
        language,
      },
    }),
  );
  return res.challenge_id;
}

/** verifyOtp trades a code for a ticket (single use, 5 minutes, same device and scene). */
export async function verifyOtp(challengeId: string, code: string): Promise<string> {
  const res = await unwrap(authApi.POST("/v1/auth/otp/verify", { body: { challenge_id: challengeId, code: code.trim(), device_id: deviceId() } }));
  return res.otp_ticket;
}

export type OtpState = {
  /** The challenge of the last code sent ("" before the first). */
  challengeId: string;
  sent: boolean;
  /** Seconds before another code may be asked for (0 = now). */
  wait: number;
  busy: "" | "send" | "verify";
  error: unknown;
  /** send asks for a code with a fresh human-check token. */
  send: (captchaToken: string) => Promise<boolean>;
  /** verify returns the ticket, or null (the error is in the state). */
  verify: (code: string) => Promise<string | null>;
  /** reset forgets the challenge (another target, or start over). */
  reset: () => void;
};

/**
 * useOtp drives one exchange for a target: send, wait out the resend
 * countdown, verify. A change of target (another email) starts over.
 */
export function useOtp(target: OtpTarget): OtpState {
  const locale = useSettings((s) => s.locale);
  const [challengeId, setChallengeId] = useState("");
  const [sentAt, setSentAt] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  const [busy, setBusy] = useState<OtpState["busy"]>("");
  const [error, setError] = useState<unknown>(null);
  const key = `${target.scene}|${target.channel ?? "EMAIL"}|${target.identifier ?? ""}|${target.loginChallengeId ?? ""}`;

  const reset = useCallback(() => {
    setChallengeId("");
    setSentAt(0);
    setError(null);
  }, []);
  useEffect(reset, [key, reset]);

  const wait = sentAt ? Math.max(0, OTP_RESEND_SECONDS - Math.floor((now - sentAt) / 1000)) : 0;
  const counting = wait > 0;
  useEffect(() => {
    if (!counting) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [counting]);

  const send = async (captchaToken: string) => {
    setBusy("send");
    setError(null);
    try {
      setChallengeId(await requestOtp(target, captchaToken, locale));
      const t = Date.now();
      setSentAt(t);
      setNow(t);
      return true;
    } catch (e) {
      setError(e);
      return false;
    } finally {
      setBusy("");
    }
  };

  const verify = async (code: string) => {
    setBusy("verify");
    setError(null);
    try {
      return await verifyOtp(challengeId, code);
    } catch (e) {
      setError(e);
      return null;
    } finally {
      setBusy("");
    }
  };

  return { challengeId, sent: challengeId !== "", wait, busy, error, send, verify, reset };
}
