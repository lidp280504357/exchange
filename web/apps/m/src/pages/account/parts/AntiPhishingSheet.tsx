import { ApiError } from "@exchange/core";
import { ANTI_PHISHING_MAX, checkAntiPhishing, keepProfile, updateProfile } from "@exchange/core/user/profile";
import { Form, FormError, FormField, FormSubmit, Input, Sheet, setServerError, toast, useWatch, useZodForm, z } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { Mail } from "lucide-react";
import { useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";

/**
 * AntiPhishingSheet sets, changes or clears the anti-phishing code that
 * every mail carries, with a preview of such a mail; saving needs a
 * step-up (ask, from useStepUp).
 */
export function AntiPhishingSheet({ open, current, onClose, ask }: { open: boolean; current: string; onClose: () => void; ask: () => Promise<string | null> }) {
  const { t } = useTranslation();
  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()} title={t("mAccount.antiPhishing.title")} closeButton>
      <AntiPhishingForm current={current} onClose={onClose} ask={ask} />
    </Sheet>
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
          if (problem) ctx.addIssue({ code: "custom", message: t(`mAccount.antiPhishing.problem.${problem}`) });
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
      toast.success(value ? t("mAccount.antiPhishing.saved") : t("mAccount.antiPhishing.cleared"));
      onClose();
    } catch (e) {
      // The profile is validated before the token is spent.
      if (!(e instanceof ApiError && e.code === "COMMON_INVALID_ARGUMENT")) token.current = null;
      setServerError(form, e, { COMMON_INVALID_ARGUMENT: "code" });
    }
  };

  return (
    <Form form={form} onSubmit={submit} aria-label={t("mAccount.antiPhishing.title")}>
      <FormError />
      <FormField label={t("mAccount.antiPhishing.code")} name="code" hint={t("mAccount.antiPhishing.hint")}>
        <Input
          {...form.register("code")}
          size="lg"
          maxLength={ANTI_PHISHING_MAX}
          autoComplete="off"
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          enterKeyHint="done"
          placeholder={t("mAccount.antiPhishing.placeholder")}
          unit={`${code.length}/${ANTI_PHISHING_MAX}`}
        />
      </FormField>
      <div className="flex flex-col gap-2">
        <span className="text-xs text-fg-3">{t("mAccount.antiPhishing.preview")}</span>
        <div className="overflow-hidden rounded-3 border border-line-1 bg-bg-2">
          <div className="flex items-center gap-2 border-b border-line-1 px-4 py-2.5 text-sm font-medium text-fg-1">
            <Mail size={14} className="shrink-0 text-fg-3" />
            <span className="truncate">{t("mAccount.antiPhishing.previewSubject")}</span>
          </div>
          <div className="flex flex-col gap-2 px-4 py-3 text-xs leading-relaxed text-fg-2">
            <span className="w-fit max-w-full break-all rounded-1 bg-brand-soft px-2 py-1 font-medium text-brand">
              {t("mAccount.antiPhishing.previewLine", { code: code || "••••" })}
            </span>
            <p>{t("mAccount.antiPhishing.previewBody")}</p>
          </div>
        </div>
      </div>
      <FormSubmit block size="lg" disabled={code === current}>
        {t("mAccount.antiPhishing.save")}
      </FormSubmit>
    </Form>
  );
}
