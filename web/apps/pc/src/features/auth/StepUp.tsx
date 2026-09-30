import { errorText, redeemStepUp, useTotpStatus } from "@exchange/core";
import { Button, Dialog, Input, Segmented, Skeleton } from "@exchange/ui";
import { useCallback, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { OtpStep } from "./OtpStep";

/**
 * useStepUp asks the user to prove it is them before a sensitive action
 * (requirements §6.5): ask() opens the dialog and resolves to a step-up
 * token, or null when the user closes it. Render `dialog` once in the
 * page. With an authenticator app bound the dialog takes its code;
 * otherwise a code by mail or SMS.
 *
 *   const stepUp = useStepUp();
 *   const token = await stepUp.ask();
 *   if (token) await walletApi.POST(..., { params: { header: stepUpHeaders(token) } });
 */
export function useStepUp(): { ask: () => Promise<string | null>; dialog: ReactNode } {
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

  const dialog = <StepUpDialog open={open} onToken={(tok) => settle(tok)} onClose={() => settle(null)} />;
  return { ask, dialog };
}

function StepUpDialog({ open, onToken, onClose }: { open: boolean; onToken: (token: string) => void; onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()} title={t("pcAuth.stepUpTitle")} size="sm" persistent footer={null}>
      {open && <StepUpBody onToken={onToken} />}
    </Dialog>
  );
}

function StepUpBody({ onToken }: { onToken: (token: string) => void }) {
  const { t } = useTranslation();
  const totp = useTotpStatus();
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  // A phone-only account gets its code by SMS; the server refuses a channel that is not bound.
  const [channel, setChannel] = useState<"EMAIL" | "SMS">("EMAIL");

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

  if (totp.isPending) return <Skeleton className="h-24 w-full" />;
  const withApp = totp.data?.enabled === true;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    void redeem({ totpCode: code });
  };
  return (
    <div className="flex flex-col gap-3">
      {withApp ? (
        <form onSubmit={submit} className="flex flex-col gap-3">
          <p className="text-sm text-fg-2">{t("pcAuth.stepUpTotpHint")}</p>
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
          <p className="text-sm text-fg-2">{t("pcAuth.stepUpOtpHint")}</p>
          <Segmented
            size="sm"
            value={channel}
            onValueChange={(c) => setChannel(c as "EMAIL" | "SMS")}
            items={[
              { value: "EMAIL", label: t("pcAuth.viaEmail") },
              { value: "SMS", label: t("pcAuth.viaSms") },
            ]}
            aria-label={t("pcAuth.channel")}
          />
          <OtpStep key={channel} target={{ scene: "STEP_UP", channel }} onTicket={(ticket) => void redeem({ otpTicket: ticket })} />
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
