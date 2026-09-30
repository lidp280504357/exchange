import { errorText, useOtp, type OtpTarget } from "@exchange/core";
import { Button, Input, Turnstile } from "@exchange/ui";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

export type OtpStepProps = {
  target: OtpTarget;
  /** Receives the ticket once the code checks out. */
  onTicket: (ticket: string) => void;
  /** Where the code went, shown after sending (e.g. the email); defaults to "the bound email or phone". */
  sentTo?: string;
  /** Label of the confirm button (default "确认"). */
  confirmLabel?: string;
};

/**
 * OtpStep: the human check, "send code", then the six digits with a resend
 * countdown (requirements §6.1). The ticket goes to onTicket; errors show
 * in place by their code.
 */
export function OtpStep({ target, onTicket, sentTo, confirmLabel }: OtpStepProps) {
  const { t } = useTranslation();
  const otp = useOtp(target);
  const [captcha, setCaptcha] = useState("");
  const [generation, setGeneration] = useState(0);
  const [code, setCode] = useState("");

  const send = async () => {
    const ok = await otp.send(captcha);
    // A token works once: the next send needs a fresh widget either way.
    setGeneration((g) => g + 1);
    if (ok) setCode("");
  };
  const verify = async (e: FormEvent) => {
    e.preventDefault();
    const ticket = await otp.verify(code);
    if (ticket) onTicket(ticket);
    else setCode("");
  };

  return (
    <div className="flex flex-col gap-3">
      {!otp.sent || otp.wait === 0 ? <Turnstile onToken={setCaptcha} generation={generation} /> : null}
      {!otp.sent ? (
        <Button block size="lg" loading={otp.busy === "send"} disabled={!captcha} onClick={() => void send()}>
          {t("mAuth.sendCode")}
        </Button>
      ) : (
        <form onSubmit={verify} className="flex flex-col gap-3">
          <p className="text-sm text-fg-2">{sentTo ? t("mAuth.codeSentTo", { target: sentTo }) : t("mAuth.codeSent")}</p>
          <Input
            size="lg"
            inputMode="numeric"
            enterKeyHint="done"
            autoComplete="one-time-code"
            maxLength={6}
            autoFocus
            placeholder={t("mAuth.codePlaceholder")}
            aria-label={t("mAuth.code")}
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
            className="tracking-[0.4em] tabular-nums"
            suffix={
              <button
                type="button"
                disabled={otp.wait > 0 || !captcha || otp.busy !== ""}
                onClick={() => void send()}
                className="-my-2 min-h-11 whitespace-nowrap px-2 text-sm text-brand disabled:text-fg-3"
              >
                {otp.wait > 0 ? t("mAuth.resendIn", { seconds: otp.wait }) : t("mAuth.resend")}
              </button>
            }
          />
          <Button type="submit" block size="lg" loading={otp.busy === "verify"} disabled={code.length !== 6}>
            {confirmLabel ?? t("common.confirm")}
          </Button>
        </form>
      )}
      {otp.error != null && (
        <p role="alert" className="text-sm text-danger">
          {errorText(otp.error)}
        </p>
      )}
    </div>
  );
}
