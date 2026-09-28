import { useState } from "react";
import { useTranslation } from "react-i18next";
import { authApi, unwrap } from "../api/client";
import { errorText } from "../i18n";
import { deviceId } from "../lib/device";
import { OtpStep } from "./OtpStep";
import { Button, Card, ErrorText } from "./ui";

// StepUp asks for a fresh code and returns a step-up token (§6.5), good
// for one sensitive action within 10 minutes.
export function StepUp({ onToken, onCancel }: { onToken: (token: string) => void; onCancel: () => void }) {
  const { t } = useTranslation();
  const [error, setError] = useState("");
  async function redeem(ticket: string) {
    try {
      const res = await unwrap(authApi.POST("/v1/auth/step-up", { body: { otp_ticket: ticket, device_id: deviceId() } }));
      onToken(res.step_up_token);
    } catch (e) {
      setError(errorText(e));
    }
  }
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4">
      <div className="w-full max-w-sm">
        <Card title={t("security.stepUp")} actions={<Button variant="ghost" onClick={onCancel}>{t("common.cancel")}</Button>}>
          <p className="mb-3 text-sm text-gray-400">{t("security.stepUpHint")}</p>
          <OtpStep scene="STEP_UP" onTicket={redeem} />
          <ErrorText text={error} />
        </Card>
      </div>
    </div>
  );
}
