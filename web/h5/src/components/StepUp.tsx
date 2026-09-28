import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { authApi, unwrap } from "../api/client";
import { errorText } from "../i18n";
import { deviceId } from "../lib/device";
import { OtpStep } from "./OtpStep";
import { Button, Card, ErrorText, Field } from "./ui";

export function useTotpStatus() {
  return useQuery({ queryKey: ["totp"], queryFn: () => unwrap(authApi.GET("/v1/auth/totp")) });
}

// StepUp confirms it is the user and returns a step-up token (§6.5), good
// for one sensitive action within 10 minutes: with the authenticator app
// once one is bound, otherwise with a fresh code by mail or SMS.
export function StepUp({ onToken, onCancel }: { onToken: (token: string) => void; onCancel: () => void }) {
  const { t } = useTranslation();
  const totp = useTotpStatus();
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function redeem(body: { otp_ticket?: string; totp_code?: string }) {
    setBusy(true);
    setError("");
    try {
      const res = await unwrap(authApi.POST("/v1/auth/step-up", { body: { ...body, device_id: deviceId() } }));
      onToken(res.step_up_token);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }
  const withApp = totp.data?.enabled === true;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4">
      <div className="w-full max-w-sm">
        <Card title={t("security.stepUp")} actions={<Button variant="ghost" onClick={onCancel}>{t("common.cancel")}</Button>}>
          {totp.isLoading ? (
            <p className="text-sm text-gray-500">{t("common.loading")}</p>
          ) : withApp ? (
            <form
              className="space-y-3"
              onSubmit={(e) => {
                e.preventDefault();
                void redeem({ totp_code: code });
              }}
            >
              <p className="text-sm text-gray-400">{t("security.totpStepUpHint")}</p>
              <Field
                label={t("security.totpCode")}
                inputMode="numeric"
                autoComplete="one-time-code"
                name="totp_code"
                maxLength={6}
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
              />
              <Button type="submit" className="w-full" disabled={code.length !== 6 || busy}>{t("common.confirm")}</Button>
            </form>
          ) : (
            <>
              <p className="mb-3 text-sm text-gray-400">{t("security.stepUpHint")}</p>
              <OtpStep scene="STEP_UP" onTicket={(ticket) => void redeem({ otp_ticket: ticket })} />
            </>
          )}
          <ErrorText text={error} />
        </Card>
      </div>
    </div>
  );
}
