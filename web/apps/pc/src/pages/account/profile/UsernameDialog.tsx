import { changeUsername, checkUsername, USERNAME_MAX } from "@exchange/core/user/avatar";
import { keepProfile } from "@exchange/core/user/profile";
import { Dialog, Form, FormError, FormField, FormSubmit, Input, setServerError, toast, useWatch, useZodForm, z } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

/**
 * UsernameDialog changes the username (design 2026-10-07, avatars and
 * usernames §1 #1): the rules checked as typed, the server's refusals
 * (taken, reserved, within 7 days) on the field.
 */
export function UsernameDialog({ open, current, onClose }: { open: boolean; current: string; onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()} title={t("pcProfile.username.dialogTitle")} size="sm" persistent footer={null}>
      {open && <UsernameForm current={current} onClose={onClose} />}
    </Dialog>
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
          if (problem) ctx.addIssue({ code: "custom", message: t(`pcProfile.username.problems.${problem}`) });
        }),
      }),
    [t, current],
  );
  const form = useZodForm(schema, { defaultValues: { username: current } });
  const name = (useWatch({ control: form.control, name: "username" }) ?? "").trim();

  const submit = async (v: { username: string }) => {
    try {
      keepProfile(qc, await changeUsername(v.username.trim()));
      toast.success(t("pcProfile.username.saved"));
      onClose();
    } catch (e) {
      setServerError(form, e, { "USER_USERNAME_*": "username" });
    }
  };

  return (
    <Form form={form} onSubmit={submit} aria-label={t("pcProfile.username.dialogTitle")}>
      <FormError />
      <FormField label={t("pcProfile.username.label")} name="username" hint={t("pcProfile.username.hint")}>
        <Input
          {...form.register("username")}
          autoFocus
          maxLength={USERNAME_MAX}
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          placeholder={t("pcProfile.username.placeholder")}
          unit={`${name.length}/${USERNAME_MAX}`}
        />
      </FormField>
      <FormSubmit block disabled={name === current}>
        {t("pcProfile.username.save")}
      </FormSubmit>
    </Form>
  );
}
