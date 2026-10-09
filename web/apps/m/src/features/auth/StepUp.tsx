import { errorText, redeemStepUp, useTotpStatus } from "@exchange/core";
import type { OtpChannel } from "@exchange/core/auth/otp";
import { firstChannel, stepUpChannels, useBoundIdentities } from "@exchange/core/user/security";
import { Button, ChannelCards, Input, Sheet, Skeleton } from "@exchange/ui";
import { useCallback, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { OtpStep } from "./OtpStep";

/**
 * useStepUp asks the user to prove it is them before a sensitive action
 * (requirements §6.5), in a bottom sheet: ask() resolves to a step-up token,
 * or null when the user closes the sheet. Render `sheet` once in the page.
 * With an authenticator app bound the sheet takes its code; otherwise a
 * code by email or SMS, picked on a card each (F32).
 *
 *   const stepUp = useStepUp();
 *   const token = await stepUp.ask();
 *   if (token) await walletApi.POST(..., { params: { header: stepUpHeaders(token) } });
 */
export function useStepUp(): { ask: () => Promise<string | null>; sheet: ReactNode } {
  const [open, setOpen] = useState(false);
  const pending = useRef<((token: string | null) => void) | null>(null);

  const settle = useCallback((token: string | null) => {
    pending.current?.(token);
    pending.current = null;
    setOpen(false);
  }, []);

  const ask = useCallback(() => {
    pending.current?.(null);
    setOpen(true);
    return new Promise<string | null>((resolve) => {
      pending.current = resolve;
    });
  }, []);

  const sheet = <StepUpSheet open={open} onToken={(tok) => settle(tok)} onClose={() => settle(null)} />;
  return { ask, sheet };
}

function StepUpSheet({ open, onToken, onClose }: { open: boolean; onToken: (token: string) => void; onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()} title={t("mAuth.stepUpTitle")}>
      {open && <StepUpBody onToken={onToken} />}
    </Sheet>
  );
}

function StepUpBody({ onToken }: { onToken: (token: string) => void }) {
  const { t } = useTranslation();
  const totp = useTotpStatus();
  const ids = useBoundIdentities();
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  // The account's email and phone: one it lacks is greyed, and it starts on
  // one it has (a phone-only account by SMS; the server refuses the other).
  const channels = stepUpChannels(ids.data);
  const [picked, setPicked] = useState<OtpChannel | null>(null);
  const channel = picked ?? firstChannel(channels);

  const redeem = async (proof: { otpTicket: string } | { totpCode: string }) => {
    setBusy(true);
    setError(null);
    try {
      onToken(await redeemStepUp(proof));
    } catch (e) {
      setError(e);
      setCode("");
    } finally {
      setBusy(false);
    }
  };

  if (totp.isPending || (totp.data?.enabled !== true && ids.isLoading)) return <Skeleton className="h-24 w-full" />;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    void redeem({ totpCode: code });
  };
  return (
    <div className="flex flex-col gap-3 pb-2">
      {totp.data?.enabled === true ? (
        <form onSubmit={submit} className="flex flex-col gap-3">
          <p className="text-sm text-fg-2">{t("mAuth.stepUpTotpHint")}</p>
          <Input
            size="lg"
            inputMode="numeric"
            autoComplete="one-time-code"
            enterKeyHint="done"
            maxLength={6}
            autoFocus
            aria-label={t("mAuth.totpCode")}
            placeholder={t("mAuth.codePlaceholder")}
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
            className="tracking-[0.4em] tabular-nums"
          />
          <Button type="submit" size="lg" block loading={busy} disabled={code.length !== 6}>
            {t("common.confirm")}
          </Button>
        </form>
      ) : (
        <>
          <p className="text-sm text-fg-2">{t("mAuth.stepUpOtpHint")}</p>
          <ChannelCards options={channels} value={channel} onValueChange={setPicked} size="lg" />
          <OtpStep
            key={channel}
            target={{ scene: "STEP_UP", channel }}
            sentTo={channels.find((c) => c.channel === channel)?.target}
            onTicket={(ticket) => void redeem({ otpTicket: ticket })}
          />
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
