import { useState } from "react";
import { useTranslation } from "react-i18next";
import { authApi, unwrap } from "../api/client";
import { errorText } from "../i18n";
import { deviceId } from "../lib/device";
import { Turnstile } from "./Turnstile";
import { Button, ErrorText, Field, Notice } from "./ui";

type Scene = "REGISTER" | "LOGIN" | "LOGIN_CHALLENGE" | "PASSWORD_RESET" | "STEP_UP";

// OtpStep requests a code for a scene and trades it for a ticket (§6.1):
// human check, send, enter the six digits, verify.
export function OtpStep({
  scene,
  identifier,
  loginChallengeId,
  onTicket,
}: {
  scene: Scene;
  identifier?: string;
  loginChallengeId?: string;
  onTicket: (ticket: string) => void;
}) {
  const { t, i18n } = useTranslation();
  const [captcha, setCaptcha] = useState("");
  const [generation, setGeneration] = useState(0);
  const [challenge, setChallenge] = useState("");
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function send() {
    setBusy(true);
    setError("");
    try {
      const res = await unwrap(
        authApi.POST("/v1/auth/otp/request", {
          body: {
            scene,
            channel: "EMAIL",
            identifier,
            login_challenge_id: loginChallengeId,
            captcha_token: captcha,
            device_id: deviceId(),
            language: i18n.language,
          },
        }),
      );
      setChallenge(res.challenge_id);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
      setGeneration((g) => g + 1);
    }
  }

  async function verify() {
    setBusy(true);
    setError("");
    try {
      const res = await unwrap(
        authApi.POST("/v1/auth/otp/verify", { body: { challenge_id: challenge, code: code.trim(), device_id: deviceId() } }),
      );
      onTicket(res.otp_ticket);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-3">
      {!challenge ? (
        <>
          <Turnstile onToken={setCaptcha} generation={generation} />
          <Button className="w-full" disabled={busy || !captcha} onClick={send}>
            {t("common.sendCode")}
          </Button>
        </>
      ) : (
        <>
          <Notice text={identifier ? t("auth.codeSent", { target: identifier }) : t("auth.codeSentBound")} />
          <Field
            label={t("common.code")}
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={6}
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
          />
          <div className="flex gap-2">
            <Button className="flex-1" disabled={busy || code.length !== 6} onClick={verify}>
              {t("common.confirm")}
            </Button>
            <Button variant="ghost" onClick={() => setChallenge("")}>
              {t("common.resend")}
            </Button>
          </div>
        </>
      )}
      <ErrorText text={error} />
    </div>
  );
}
