import { confirmTotp, groupSecret, refreshTotp, type TotpSetup } from "@exchange/core/user/security";
import { Controller, CopyButton, Dialog, Form, FormError, FormField, FormSubmit, Input, QrCode, setServerError, toast, useZodForm, z } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { Smartphone, TriangleAlert } from "lucide-react";
import { useMemo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** Numbered is one numbered step of a how-to inside a dialog. */
export function Numbered({ n, title, desc, children }: { n: number; title: ReactNode; desc?: ReactNode; children?: ReactNode }) {
  return (
    <li className="flex gap-3">
      <span aria-hidden className="grid size-7 shrink-0 place-items-center rounded-full bg-brand-soft text-sm font-semibold text-brand tabular-nums">
        {n}
      </span>
      <div className="flex min-w-0 flex-1 flex-col gap-2 pt-0.5">
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
 * BindTotpDialog finishes binding an authenticator app after setup: the
 * otpauth link as a QR code, the secret to type with a copy button, and a
 * first code to confirm it.
 */
export function BindTotpDialog({ setup, onClose }: { setup: TotpSetup | null; onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <Dialog open={setup !== null} onOpenChange={(o) => !o && onClose()} title={t("pcAccount.totp.bindTitle")} size="xl" persistent footer={null}>
      {setup && <BindTotpBody setup={setup} onClose={onClose} />}
    </Dialog>
  );
}

function BindTotpBody({ setup, onClose }: { setup: TotpSetup; onClose: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const schema = useMemo(() => z.object({ code: z.string().regex(/^\d{6}$/, t("pcAccount.totp.codeInvalid")) }), [t]);
  const form = useZodForm(schema, { defaultValues: { code: "" } });

  const submit = async ({ code }: { code: string }) => {
    try {
      await confirmTotp(code);
      await refreshTotp(qc);
      toast.success(t("pcAccount.totp.bound"));
      onClose();
    } catch (e) {
      form.setValue("code", "");
      setServerError(form, e, { AUTH_TOTP_INVALID: "code" });
    }
  };

  return (
    <ol className="flex flex-col gap-5">
      <Numbered n={1} title={t("pcAccount.totp.step1")} desc={t("pcAccount.totp.step1Desc")} />
      <Numbered n={2} title={t("pcAccount.totp.step2")} desc={t("pcAccount.totp.step2Desc")}>
        <div className="flex items-center gap-5 rounded-3 border border-line-1 bg-bg-2 p-4">
          <QrCode value={setup.otpauth_uri} size={144} label={t("pcAccount.totp.qr")} className="shrink-0" />
          <div className="flex min-w-0 flex-1 flex-col gap-2">
            <span className="text-xs text-fg-3">{t("pcAccount.totp.secret")}</span>
            <div className="flex items-start gap-2 rounded-2 bg-bg-1 px-3 py-2">
              <code data-testid="totp-secret" className="min-w-0 flex-1 break-all font-mono text-sm tracking-wider text-fg-1">
                {groupSecret(setup.secret)}
              </code>
              <CopyButton value={setup.secret} />
            </div>
            <a href={setup.otpauth_uri} className="inline-flex w-fit items-center gap-1 text-xs text-brand hover:underline">
              <Smartphone size={12} /> {t("pcAccount.totp.openApp")}
            </a>
            <p className="flex items-start gap-1.5 text-xs leading-relaxed text-warn">
              <TriangleAlert size={12} className="mt-0.5 shrink-0" />
              {t("pcAccount.totp.secretWarn")}
            </p>
          </div>
        </div>
      </Numbered>
      <Numbered n={3} title={t("pcAccount.totp.step3")} desc={t("pcAccount.totp.step3Desc")}>
        <Form form={form} onSubmit={submit} aria-label={t("pcAccount.totp.step3")} className="gap-3">
          <FormError />
          <div className="flex items-start gap-3">
            <FormField name="code" className="flex-1">
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
                      onChange={(e) => field.onChange(e.target.value.replace(/\D/g, "").slice(0, 6))}
                      autoFocus
                      inputMode="numeric"
                      autoComplete="one-time-code"
                      maxLength={6}
                      aria-label={t("pcAuth.totpCode")}
                      placeholder={t("pcAuth.codePlaceholder")}
                      className="tracking-[0.4em] tabular-nums"
                    />
                  )}
                />
              )}
            </FormField>
            <FormSubmit>{t("pcAccount.totp.confirm")}</FormSubmit>
          </div>
        </Form>
      </Numbered>
    </ol>
  );
}
