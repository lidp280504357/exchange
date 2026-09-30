import { ApiError, errorText, routes } from "@exchange/core";
import { kindOf } from "@exchange/core/auth/identity";
import { initialResetState, passwordChecks, resetPassword, resetReducer } from "@exchange/core/auth/password";
import { Button, Form, FormError, FormField, FormSubmit, Input, Stepper, setServerError, toast, useWatch, useZodForm, z } from "@exchange/ui";
import { ArrowLeft, KeyRound, TriangleAlert } from "lucide-react";
import { useMemo, useReducer, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "react-router";
import { OtpStep } from "../../features/auth/OtpStep";
import { AuthCard } from "./parts/AuthCard";
import { parsedOrNull } from "./parts/fields";
import { PasswordInput } from "./parts/PasswordInput";
import { PasswordStrength } from "./parts/PasswordStrength";
import { identifierField } from "./parts/schemas";

const STEP_INDEX = { identify: 0, verify: 1, password: 2 } as const;

/**
 * Reset (design §6.2 认证): the account, its PASSWORD_RESET code, then a
 * new password twice with its strength; every session ends and the
 * sign-in page opens with the account filled in.
 */
export default function Reset() {
  const { t } = useTranslation();
  const [state, dispatch] = useReducer(resetReducer, initialResetState);
  const [codeError, setCodeError] = useState<unknown>(null);

  return (
    <AuthCard
      icon={<KeyRound size={22} />}
      title={t("pcAuth.resetTitle")}
      subtitle={t("pcAuth.resetSubtitle")}
      footer={
        <Link to={routes.login} className="inline-flex items-center gap-1 text-fg-2 hover:text-fg-1">
          <ArrowLeft size={14} /> {t("pcAuth.backToLogin")}
        </Link>
      }
    >
      <div className="flex flex-col gap-6">
        <Stepper
          size="sm"
          current={STEP_INDEX[state.step]}
          onStepClick={(i) => i === 0 && dispatch({ type: "back" })}
          steps={[
            { key: "account", title: t("pcAuth.resetStepAccount") },
            { key: "code", title: t("pcAuth.resetStepCode") },
            { key: "password", title: t("pcAuth.resetStepPassword") },
          ]}
        />
        {state.step === "identify" && <AccountStep initial={state.identifier} onDone={(id) => dispatch({ type: "identified", identifier: id })} />}
        {state.step === "verify" && (
          <div className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-3 rounded-2 bg-bg-1 px-3 py-2 text-sm">
              <span className="min-w-0 truncate text-fg-2">{t("pcAuth.codeGoesTo", { target: state.identifier })}</span>
              <Button size="sm" variant="ghost" onClick={() => dispatch({ type: "back" })}>
                {t("pcAuth.change")}
              </Button>
            </div>
            <OtpStep
              key={`${state.identifier}-${state.attempt}`}
              target={{ scene: "PASSWORD_RESET", channel: kindOf(state.identifier) === "PHONE" ? "SMS" : "EMAIL", identifier: state.identifier }}
              sentTo={state.identifier}
              confirmLabel={t("common.next")}
              onTicket={(ticket) => {
                setCodeError(null);
                dispatch({ type: "ticket", ticket });
              }}
            />
            {codeError != null && (
              <p role="alert" className="text-sm text-danger">
                {errorText(codeError)}
              </p>
            )}
            <p className="text-xs text-fg-3">{t("pcAuth.noCodeHint")}</p>
          </div>
        )}
        {state.step === "password" && (
          <PasswordStep
            identifier={state.identifier}
            ticket={state.ticket}
            onTicketSpent={(e) => {
              setCodeError(e);
              dispatch({ type: "failed", error: e });
            }}
          />
        )}
      </div>
    </AuthCard>
  );
}

function AccountStep({ initial, onDone }: { initial: string; onDone: (identifier: string) => void }) {
  const { t } = useTranslation();
  const schema = useMemo(() => z.object({ identifier: identifierField(t) }), [t]);
  const form = useZodForm(schema, { defaultValues: { identifier: initial } });
  return (
    <Form form={form} onSubmit={(v) => onDone(parsedOrNull(v.identifier)?.value ?? v.identifier)} aria-label={t("pcAuth.resetStepAccount")}>
      <FormField label={t("pcAuth.identifier")} name="identifier" required>
        <Input
          {...form.register("identifier")}
          size="lg"
          autoFocus
          autoComplete="username"
          placeholder={t("pcAuth.identifierPlaceholder")}
        />
      </FormField>
      <FormSubmit block size="lg">
        {t("pcAuth.continue")}
      </FormSubmit>
    </Form>
  );
}

function PasswordStep({ identifier, ticket, onTicketSpent }: { identifier: string; ticket: string; onTicketSpent: (e: unknown) => void }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const schema = useMemo(
    () =>
      z
        .object({ password: z.string(), confirm: z.string() })
        .superRefine((v, ctx) => {
          const failed = passwordChecks(v.password, [identifier]).find((c) => !c.ok);
          if (failed) ctx.addIssue({ code: "custom", path: ["password"], message: t(`pcAuth.rule.${failed.rule}`) });
          if (v.confirm !== v.password) ctx.addIssue({ code: "custom", path: ["confirm"], message: t("pcAuth.mismatch") });
        }),
    [t, identifier],
  );
  const form = useZodForm(schema, { defaultValues: { password: "", confirm: "" } });
  const password = useWatch({ control: form.control, name: "password" });

  const submit = async (v: { password: string }) => {
    try {
      await resetPassword(ticket, v.password);
      toast.success(t("pcAuth.resetDone"));
      navigate(routes.login, { replace: true, state: { identifier } });
    } catch (e) {
      const code = e instanceof ApiError ? e.code : "";
      if (code === "AUTH_TICKET_INVALID" || code.startsWith("AUTH_OTP_")) {
        onTicketSpent(e);
        return;
      }
      setServerError(form, e, { AUTH_PASSWORD_WEAK: "password" });
    }
  };

  return (
    <Form form={form} onSubmit={submit} aria-label={t("pcAuth.resetStepPassword")}>
      <FormError />
      <FormField label={t("pcAuth.resetStepPassword")} name="password" required>
        <PasswordInput {...form.register("password")} size="lg" autoFocus autoComplete="new-password" placeholder={t("pcAuth.newPasswordPlaceholder")} />
      </FormField>
      <PasswordStrength password={password ?? ""} identifiers={[identifier]} />
      <FormField label={t("pcAuth.confirmPassword")} name="confirm" required>
        <PasswordInput {...form.register("confirm")} size="lg" autoComplete="new-password" placeholder={t("pcAuth.confirmPlaceholder")} />
      </FormField>
      <p className="flex items-start gap-2 rounded-2 bg-warn/10 p-3 text-xs leading-relaxed text-warn">
        <TriangleAlert size={14} className="mt-px shrink-0" />
        {t("pcAuth.resetNote")}
      </p>
      <FormSubmit block size="lg">
        {t("pcAuth.resetSubmit")}
      </FormSubmit>
    </Form>
  );
}
