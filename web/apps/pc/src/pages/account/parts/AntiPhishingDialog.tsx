import { ApiError } from "@exchange/core";
import { ANTI_PHISHING_MAX, checkAntiPhishing, keepProfile, updateProfile } from "@exchange/core/user/profile";
import { Dialog, Form, FormError, FormField, FormSubmit, Input, setServerError, toast, useWatch, useZodForm, z } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { Mail } from "lucide-react";
import { useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";

/**
 * AntiPhishingDialog sets, changes or clears the anti-phishing code that
 * every mail carries, with a preview of such a mail; saving needs a
 * step-up (useStepUp).
 */
export function AntiPhishingDialog({ open, current, onClose, ask }: { open: boolean; current: string; onClose: () => void; ask: () => Promise<string | null> }) {
  const { t } = useTranslation();
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()} title={t("pcAccount.antiPhishing.title")} size="md" persistent footer={null}>
      {open && <AntiPhishingForm current={current} onClose={onClose} ask={ask} />}
    </Dialog>
  );
}

function AntiPhishingForm({ current, onClose, ask }: { current: string; onClose: () => void; ask: () => Promise<string | null> }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const token = useRef<string | null>(null);
  const schema = useMemo(
    () =>
      z.object({
        code: z.string().superRefine((v, ctx) => {
          const problem = checkAntiPhishing(v.trim());
          if (problem) ctx.addIssue({ code: "custom", message: t(`pcAccount.antiPhishing.problem.${problem}`) });
        }),
      }),
    [t],
  );
  const form = useZodForm(schema, { defaultValues: { code: current } });
  const code = (useWatch({ control: form.control, name: "code" }) ?? "").trim();

  const submit = async (v: { code: string }) => {
    const value = v.code.trim();
    token.current ??= await ask();
    if (!token.current) return;
    try {
      keepProfile(qc, await updateProfile({ anti_phishing_code: value }, token.current));
      toast.success(value ? t("pcAccount.antiPhishing.saved") : t("pcAccount.antiPhishing.cleared"));
      onClose();
    } catch (e) {
      // The profile is validated before the token is spent.
      if (!(e instanceof ApiError && e.code === "COMMON_INVALID_ARGUMENT")) token.current = null;
      setServerError(form, e, { COMMON_INVALID_ARGUMENT: "code" });
    }
  };

  return (
    <Form form={form} onSubmit={submit} aria-label={t("pcAccount.antiPhishing.title")}>
      <FormError />
      <FormField label={t("pcAccount.antiPhishing.code")} name="code" hint={t("pcAccount.antiPhishing.hint")}>
        <Input
          {...form.register("code")}
          autoFocus
          maxLength={ANTI_PHISHING_MAX}
          autoComplete="off"
          spellCheck={false}
          placeholder={t("pcAccount.antiPhishing.placeholder")}
          unit={`${code.length}/${ANTI_PHISHING_MAX}`}
        />
      </FormField>
      <div className="flex flex-col gap-2">
        <span className="text-xs text-fg-3">{t("pcAccount.antiPhishing.preview")}</span>
        <div className="overflow-hidden rounded-3 border border-line-1 bg-bg-2">
          <div className="flex items-center gap-2 border-b border-line-1 px-4 py-2.5 text-sm font-medium text-fg-1">
            <Mail size={14} className="text-fg-3" />
            {t("pcAccount.antiPhishing.previewSubject")}
          </div>
          <div className="flex flex-col gap-2 px-4 py-3 text-xs leading-relaxed text-fg-2">
            <span className="w-fit rounded-1 bg-brand-soft px-2 py-1 font-medium text-brand">
              {t("pcAccount.antiPhishing.previewLine", { code: code || "••••" })}
            </span>
            <p>{t("pcAccount.antiPhishing.previewBody")}</p>
          </div>
        </div>
      </div>
      <FormSubmit block disabled={code === current}>
        {t("pcAccount.antiPhishing.save")}
      </FormSubmit>
    </Form>
  );
}
