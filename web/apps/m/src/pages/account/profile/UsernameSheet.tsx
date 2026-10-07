import { changeUsername, checkUsername, USERNAME_MAX } from "@exchange/core/user/avatar";
import { keepProfile } from "@exchange/core/user/profile";
import { Form, FormError, FormField, FormSubmit, Input, Sheet, setServerError, toast, useWatch, useZodForm, z } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

/**
 * UsernameSheet changes the username on the phone (design 2026-10-07,
 * avatars and usernames §1 #1): the rules checked as typed, the server's
 * refusals (taken, reserved, within 7 days) on the field.
 */
export function UsernameSheet({ open, current, onClose }: { open: boolean; current: string; onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()} title={t("mProfile.username.sheetTitle")} closeButton>
      {open && <UsernameForm current={current} onClose={onClose} />}
    </Sheet>
  );
}

function UsernameForm({ current, onClose }: { current: string; onClose: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const schema = useMemo(
    () =>
      z.object({
        username: z.string().superRefine((v, ctx) => {
          const name = v.trim();
          const problem = name === current ? "same" : checkUsername(name);
          if (problem) ctx.addIssue({ code: "custom", message: t(`mProfile.username.problems.${problem}`) });
        }),
      }),
    [t, current],
  );
  const form = useZodForm(schema, { defaultValues: { username: current } });
  const name = (useWatch({ control: form.control, name: "username" }) ?? "").trim();

  const submit = async (v: { username: string }) => {
    try {
      keepProfile(qc, await changeUsername(v.username.trim()));
      toast.success(t("mProfile.username.saved"));
      onClose();
    } catch (e) {
      setServerError(form, e, { "USER_USERNAME_*": "username" });
    }
  };

  return (
    <Form form={form} onSubmit={submit} aria-label={t("mProfile.username.sheetTitle")}>
      <FormError />
      <FormField label={t("mProfile.username.label")} name="username" hint={t("mProfile.username.hint")}>
        <Input
          {...form.register("username")}
          size="lg"
          maxLength={USERNAME_MAX}
          autoComplete="off"
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          enterKeyHint="done"
          placeholder={t("mProfile.username.placeholder")}
          unit={`${name.length}/${USERNAME_MAX}`}
        />
      </FormField>
      <FormSubmit block size="lg" disabled={name === current}>
        {t("mProfile.username.save")}
      </FormSubmit>
    </Form>
  );
}
