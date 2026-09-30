import { ApiError, errorText, routes } from "@exchange/core";
import { kindOf } from "@exchange/core/auth/identity";
import { initialResetState, passwordChecks, resetPassword, resetReducer } from "@exchange/core/auth/password";
import { Form, FormError, FormField, FormSubmit, Input, Stepper, setServerError, toast, useWatch, useZodForm, z } from "@exchange/ui";
import { ArrowLeft, TriangleAlert } from "lucide-react";
import { useMemo, useReducer, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { OtpStep } from "../../features/auth/OtpStep";
import { AuthScreen } from "./parts/AuthScreen";
import { parsedOrNull } from "./parts/fields";
import { PasswordInput } from "./parts/PasswordInput";
import { PasswordStrength } from "./parts/PasswordStrength";
import { identifierField } from "./parts/schemas";
import { TextButton, TextLink } from "./parts/TextButton";

const STEP_INDEX = { identify: 0, verify: 1, password: 2 } as const;

/**
 * Reset (design §7.2 认证), full screen: the account, its PASSWORD_RESET
 * code, then a new password twice with its strength; every session ends
 * and the sign-in page opens with the account filled in. The flow is the
 * PC site's (core auth/password).
 */
export default function Reset() {
  const { t } = useTranslation();
  const [state, dispatch] = useReducer(resetReducer, initialResetState);
  const [codeError, setCodeError] = useState<unknown>(null);

  return (
    <AuthScreen
      title={t("mAuth.resetTitle")}
      subtitle={t("mAuth.resetSubtitle")}
      footer={
        <TextLink to={routes.login} tone="muted">
          <ArrowLeft size={16} /> {t("mAuth.backToLogin")}
        </TextLink>
      }
    >
      <div className="flex flex-col gap-6">
        <Stepper
          size="sm"
          current={STEP_INDEX[state.step]}
          onStepClick={(i) => i === 0 && dispatch({ type: "back" })}
          steps={[
            { key: "account", title: t("mAuth.resetStepAccount") },
            { key: "code", title: t("mAuth.resetStepCode") },
            { key: "password", title: t("mAuth.resetStepPassword") },
          ]}
        />
        {state.step === "identify" && <AccountStep initial={state.identifier} onDone={(id) => dispatch({ type: "identified", identifier: id })} />}
        {state.step === "verify" && (
          <div className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-3 rounded-2 bg-bg-1 pl-3 pr-1 text-sm">
              <span className="min-w-0 truncate text-fg-2">{t("mAuth.codeGoesTo", { target: state.identifier })}</span>
              <TextButton onClick={() => dispatch({ type: "back" })} className="px-2">
                {t("mAuth.change")}
              </TextButton>
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
            <p className="text-xs text-fg-3">{t("mAuth.noCodeHint")}</p>
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
    </AuthScreen>
  );
}

function AccountStep({ initial, onDone }: { initial: string; onDone: (identifier: string) => void }) {
  const { t } = useTranslation();
  const schema = useMemo(() => z.object({ identifier: identifierField(t) }), [t]);
  const form = useZodForm(schema, { defaultValues: { identifier: initial } });
  return (
    <Form form={form} onSubmit={(v) => onDone(parsedOrNull(v.identifier)?.value ?? v.identifier)} aria-label={t("mAuth.resetStepAccount")}>
      <FormField label={t("mAuth.identifier")} name="identifier" required>
        <Input
          {...form.register("identifier")}
          size="lg"
          autoFocus
          autoComplete="username"
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          enterKeyHint="next"
          placeholder={t("mAuth.identifierPlaceholder")}
        />
      </FormField>
      <FormSubmit block size="lg">
        {t("mAuth.continue")}
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
          if (failed) ctx.addIssue({ code: "custom", path: ["password"], message: t(`mAuth.rule.${failed.rule}`) });
          if (v.confirm !== v.password) ctx.addIssue({ code: "custom", path: ["confirm"], message: t("mAuth.mismatch") });
        }),
    [t, identifier],
  );
  const form = useZodForm(schema, { defaultValues: { password: "", confirm: "" } });
  const password = useWatch({ control: form.control, name: "password" });

  const submit = async (v: { password: string }) => {
    try {
      await resetPassword(ticket, v.password);
      toast.success(t("mAuth.resetDone"));
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
    <Form form={form} onSubmit={submit} aria-label={t("mAuth.resetStepPassword")}>
      <FormError />
      <FormField label={t("mAuth.resetStepPassword")} name="password" required>
        <PasswordInput {...form.register("password")} autoFocus autoComplete="new-password" enterKeyHint="next" placeholder={t("mAuth.newPasswordPlaceholder")} />
      </FormField>
      <PasswordStrength password={password ?? ""} identifiers={[identifier]} />
      <FormField label={t("mAuth.confirmPassword")} name="confirm" required>
        <PasswordInput {...form.register("confirm")} autoComplete="new-password" enterKeyHint="done" placeholder={t("mAuth.confirmPlaceholder")} />
      </FormField>
      <p className="flex items-start gap-2 rounded-2 bg-warn/10 p-3 text-xs leading-relaxed text-warn">
        <TriangleAlert size={14} className="mt-px shrink-0" />
        {t("mAuth.resetNote")}
      </p>
      <FormSubmit block size="lg">
        {t("mAuth.resetSubmit")}
      </FormSubmit>
    </Form>
  );
}
