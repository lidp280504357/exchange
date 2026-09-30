import { confirmTotp, groupSecret, refreshTotp, type TotpSetup } from "@exchange/core/user/security";
import {
  Button, Controller, Form, FormError, FormField, FormSubmit, Input, Sheet, Skeleton, copyText, setServerError, toast, useZodForm, z,
} from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { Copy, ExternalLink, QrCode as QrIcon, TriangleAlert } from "lucide-react";
import { lazy, Suspense, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { digitsOnly } from "../../auth/parts/fields";
import { useLast } from "./useLast";

// The QR code library is its own chunk (design §4.4), loaded when the code is asked for.
const QrCode = lazy(() => import("@exchange/ui/data/QrCode").then((m) => ({ default: m.QrCode })));

/** Numbered is one numbered step of a how-to inside a sheet. */
function Numbered({ n, title, desc, children }: { n: number; title: ReactNode; desc?: ReactNode; children?: ReactNode }) {
  return (
    <li className="flex gap-3">
      <span aria-hidden className="grid size-7 shrink-0 place-items-center rounded-full bg-brand-soft text-sm font-semibold text-brand tabular-nums">
        {n}
      </span>
      <div className="flex min-w-0 flex-1 flex-col gap-3 pt-0.5">
        <div>
          <div className="text-sm font-medium text-fg-1">{title}</div>
          {desc && <p className="mt-0.5 text-xs leading-relaxed text-fg-3">{desc}</p>}
        </div>
        {children}
      </div>
    </li>
  );
}

/**
 * TotpSheet finishes binding an authenticator app after setup, on the
 * phone the app most likely runs on: open the otpauth link in the app or
 * copy the key, the QR code on demand (for a second device), then a first
 * code to confirm it.
 */
export function TotpSheet({ setup, onClose }: { setup: TotpSetup | null; onClose: () => void }) {
  const { t } = useTranslation();
  const shown = useLast(setup);
  return (
    <Sheet open={setup !== null} onOpenChange={(o) => !o && onClose()} title={t("mAccount.totp.bindTitle")} closeButton>
      {shown && <TotpBody setup={shown} onClose={onClose} />}
    </Sheet>
  );
}

function TotpBody({ setup, onClose }: { setup: TotpSetup; onClose: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [qr, setQr] = useState(false);
  const schema = useMemo(() => z.object({ code: z.string().regex(/^\d{6}$/, t("mAccount.totp.codeInvalid")) }), [t]);
  const form = useZodForm(schema, { defaultValues: { code: "" } });

  const copy = async () => {
    if (await copyText(setup.secret)) toast.success(t("mAccount.totp.secretCopied"));
  };

  const submit = async ({ code }: { code: string }) => {
    try {
      await confirmTotp(code);
      await refreshTotp(qc);
      toast.success(t("mAccount.totp.bound"));
      onClose();
    } catch (e) {
      form.setValue("code", "");
      setServerError(form, e, { AUTH_TOTP_INVALID: "code" });
    }
  };

  return (
    <ol className="flex flex-col gap-6 pb-2">
      <Numbered n={1} title={t("mAccount.totp.step1")} desc={t("mAccount.totp.step1Desc")} />
      <Numbered n={2} title={t("mAccount.totp.step2")} desc={t("mAccount.totp.step2Desc")}>
        <Button asChild size="lg" block icon={<ExternalLink size={16} />}>
          <a href={setup.otpauth_uri}>{t("mAccount.totp.openApp")}</a>
        </Button>
        <div className="flex flex-col gap-2 rounded-3 border border-line-1 bg-bg-2 p-3">
          <span className="text-xs text-fg-3">{t("mAccount.totp.secret")}</span>
          <code data-testid="totp-secret" className="break-all font-mono text-md tracking-wider text-fg-1">
            {groupSecret(setup.secret)}
          </code>
          <div className="flex flex-col gap-2">
            <Button variant="secondary" size="lg" block icon={<Copy size={16} />} onClick={() => void copy()}>
              {t("mAccount.totp.copySecret")}
            </Button>
            <Button variant="ghost" size="lg" block icon={<QrIcon size={16} />} aria-expanded={qr} onClick={() => setQr((v) => !v)}>
              {qr ? t("mAccount.totp.hideQr") : t("mAccount.totp.showQr")}
            </Button>
          </div>
          {qr && (
            <Suspense fallback={<Skeleton className="mx-auto mt-1 size-[200px] rounded-2" />}>
              <QrCode value={setup.otpauth_uri} size={176} label={t("mAccount.totp.qr")} className="mx-auto mt-1 animate-fade-in" />
            </Suspense>
          )}
          <p className="flex items-start gap-1.5 text-xs leading-relaxed text-warn">
            <TriangleAlert size={12} className="mt-0.5 shrink-0" />
            {t("mAccount.totp.secretWarn")}
          </p>
        </div>
      </Numbered>
      <Numbered n={3} title={t("mAccount.totp.step3")} desc={t("mAccount.totp.step3Desc")}>
        <Form form={form} onSubmit={submit} aria-label={t("mAccount.totp.step3")} className="gap-3">
          <FormError />
          <FormField name="code">
            {(control) => (
              <Controller
                control={form.control}
                name="code"
                render={({ field }) => (
                  <Input
                    {...control}
                    name={field.name}
                    ref={field.ref}
                    value={field.value}
                    onBlur={field.onBlur}
                    onChange={(e) => field.onChange(digitsOnly(e.target.value))}
                    size="lg"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    enterKeyHint="done"
                    maxLength={6}
                    aria-label={t("mAuth.totpCode")}
                    placeholder={t("mAuth.codePlaceholder")}
                    className="tracking-[0.4em] tabular-nums"
                  />
                )}
              />
            )}
          </FormField>
          <FormSubmit block size="lg">
            {t("mAccount.totp.confirm")}
          </FormSubmit>
        </Form>
      </Numbered>
    </ol>
  );
}
