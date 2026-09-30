import { errorText, redeemStepUp } from "@exchange/core";
import type { StepUpMethod } from "@exchange/core/user/security";
import { Button, Input } from "@exchange/ui";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { OtpStep } from "../../../features/auth/OtpStep";

/**
 * StepUpInline proves it is the user inside a flow, by a chosen method:
 * the authenticator app, or a STEP_UP code by email or SMS. Binding and
 * rebinding need this (requirements §6.4): a rebind must step up through
 * the identity that stays, which the shared dialog (email or app) cannot
 * pick. Resolves through onToken.
 */
export function StepUpInline({ method, mask, onToken }: { method: StepUpMethod; mask?: string; onToken: (token: string) => void }) {
  const { t } = useTranslation();
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [attempt, setAttempt] = useState(0);

  const redeem = async (proof: { otpTicket: string } | { totpCode: string }) => {
    setBusy(true);
    setError(null);
    try {
      onToken(await redeemStepUp(proof));
    } catch (e) {
      setError(e);
      setCode("");
      // A ticket works once: the next try needs a new code.
      if ("otpTicket" in proof) setAttempt((a) => a + 1);
    } finally {
      setBusy(false);
    }
  };

  const kind = t(`pcAccount.kind.${method === "SMS" ? "PHONE" : "EMAIL"}`);
  return (
    <div className="flex flex-col gap-3">
      {method === "TOTP" ? (
        <form
          className="flex flex-col gap-3"
          onSubmit={(e: FormEvent) => {
            e.preventDefault();
            if (code.length === 6) void redeem({ totpCode: code });
          }}
        >
          <p className="text-sm text-fg-2">{t("pcAccount.identity.verifyTotp")}</p>
          <Input
            size="lg"
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={6}
            autoFocus
            aria-label={t("pcAuth.totpCode")}
            placeholder={t("pcAuth.codePlaceholder")}
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
            className="tracking-[0.4em] tabular-nums"
          />
          <Button type="submit" block loading={busy} disabled={code.length !== 6}>
            {t("common.confirm")}
          </Button>
        </form>
      ) : (
        <>
          <p className="text-sm text-fg-2">
            {mask ? t("pcAccount.identity.verifyOtp", { kind, mask }) : t("pcAccount.identity.verifyOtpNoMask", { kind })}
          </p>
          <OtpStep key={attempt} target={{ scene: "STEP_UP", channel: method }} sentTo={mask} onTicket={(ticket) => void redeem({ otpTicket: ticket })} />
        </>
      )}
      {error != null && (
        <p role="alert" className="text-sm text-danger">
          {errorText(error)}
        </p>
      )}
    </div>
  );
}
