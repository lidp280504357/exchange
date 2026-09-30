import { ApiError } from "@exchange/core";
import { changePassword, passwordChecks } from "@exchange/core/auth/password";
import { sessionsKey } from "@exchange/core/user/sessions";
import { Dialog, Form, FormError, FormField, FormSubmit, setServerError, toast, useWatch, useZodForm, z } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { Info } from "lucide-react";
import { useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";
import { PasswordInput } from "../../auth/parts/PasswordInput";
import { PasswordStrength } from "../../auth/parts/PasswordStrength";

// Errors the server finds before it spends the step-up token (the
// transaction rolls back): the same token may try again.
const KEEPS_TOKEN = new Set(["AUTH_PASSWORD_INVALID", "AUTH_PASSWORD_WEAK"]);

/**
 * ChangePasswordDialog changes the password: the current one, the new one
 * twice with its strength, then a step-up (useStepUp); the other sessions
 * end.
 */
export function ChangePasswordDialog({ open, onClose, ask }: { open: boolean; onClose: () => void; ask: () => Promise<string | null> }) {
  const { t } = useTranslation();
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()} title={t("pcAccount.password.title")} size="md" persistent footer={null}>
      {open && <ChangePasswordForm onClose={onClose} ask={ask} />}
    </Dialog>
  );
}

function ChangePasswordForm({ onClose, ask }: { onClose: () => void; ask: () => Promise<string | null> }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const token = useRef<string | null>(null);
  const schema = useMemo(
    () =>
      z
        .object({ current: z.string().min(1, t("pcAuth.passwordRequired")), next: z.string(), confirm: z.string() })
        .superRefine((v, ctx) => {
          const failed = passwordChecks(v.next).find((c) => !c.ok);
          if (failed) ctx.addIssue({ code: "custom", path: ["next"], message: t(`pcAuth.rule.${failed.rule}`) });
          if (v.confirm !== v.next) ctx.addIssue({ code: "custom", path: ["confirm"], message: t("pcAuth.mismatch") });
        }),
    [t],
  );
  const form = useZodForm(schema, { defaultValues: { current: "", next: "", confirm: "" } });
  const next = useWatch({ control: form.control, name: "next" });

  const submit = async (v: { current: string; next: string }) => {
    token.current ??= await ask();
    if (!token.current) return;
    try {
      await changePassword(v.current, v.next, token.current);
      toast.success(t("pcAccount.password.done"));
      void qc.invalidateQueries({ queryKey: sessionsKey });
      onClose();
    } catch (e) {
      if (!(e instanceof ApiError && KEEPS_TOKEN.has(e.code))) token.current = null;
      setServerError(form, e, { AUTH_PASSWORD_INVALID: "current", AUTH_PASSWORD_WEAK: "next" });
    }
  };

  return (
    <Form form={form} onSubmit={submit} aria-label={t("pcAccount.password.title")}>
      <FormError />
      <FormField label={t("pcAccount.password.current")} name="current" required>
        <PasswordInput {...form.register("current")} autoFocus autoComplete="current-password" />
      </FormField>
      <FormField label={t("pcAccount.password.new")} name="next" required>
        <PasswordInput {...form.register("next")} autoComplete="new-password" placeholder={t("pcAuth.newPasswordPlaceholder")} />
      </FormField>
      <PasswordStrength password={next ?? ""} />
      <FormField label={t("pcAccount.password.confirm")} name="confirm" required>
        <PasswordInput {...form.register("confirm")} autoComplete="new-password" placeholder={t("pcAuth.confirmPlaceholder")} />
      </FormField>
      <p className="flex items-start gap-2 text-xs leading-relaxed text-fg-3">
        <Info size={14} className="mt-px shrink-0" />
        {t("pcAccount.password.note")}
      </p>
      <FormSubmit block>{t("pcAccount.password.submit")}</FormSubmit>
    </Form>
  );
}
