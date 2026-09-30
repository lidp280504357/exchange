import { ApiError, errorText, routes, selectRestoring, selectSignedIn, useSession, useSettings } from "@exchange/core";
import { channelOf, type IdentityKind } from "@exchange/core/auth/identity";
import { signIn } from "@exchange/core/auth/login";
import { passwordChecks } from "@exchange/core/auth/password";
import {
  browserRegion, initialRegisterState, REGIONS, register, registerReducer, regionName, termsFrom, termsKey, useTerms, type RegisterAction,
} from "@exchange/core/auth/register";
import { browserTimeZone } from "@exchange/core/user/preferences";
import {
  Button, Checkbox, Combobox, Controller, Form, FormError, FormField, FormSubmit, Input, Segmented, Skeleton, Spinner, setServerError, toast,
  useWatch, useZodForm, z, type ComboboxItem, type UseFormReturn,
} from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Info, Mail, Smartphone, UserPlus } from "lucide-react";
import { useMemo, useReducer, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, Navigate, useNavigate } from "react-router";
import { OtpStep } from "../../features/auth/OtpStep";
import { AuthCard, AuthPending } from "./parts/AuthCard";
import { parsedOrNull } from "./parts/fields";
import { PasswordInput } from "./parts/PasswordInput";
import { PasswordStrength } from "./parts/PasswordStrength";
import { identifierField } from "./parts/schemas";

type Values = { identifier: string; password: string; region: string; agree: boolean };

const REGION_SET = new Set(REGIONS);

/**
 * Register (design §6.2 认证): email or phone (email by default: SMS may
 * be switched off), a password with its strength and the live rules, the
 * region for email sign-ups and the current terms; then the REGISTER
 * code, the account, and the assets page with the welcome funds.
 */
export default function Register() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const signedIn = useSession(selectSignedIn);
  const restoring = useSession(selectRestoring);
  const locale = useSettings((s) => s.locale);
  const terms = useTerms();
  const [state, dispatch] = useReducer(registerReducer, initialRegisterState);
  const [kind, setKind] = useState<IdentityKind>("EMAIL");
  const [verifyError, setVerifyError] = useState<unknown>(null);
  const completing = useRef(false);

  const schema = useMemo(
    () =>
      z
        .object({ identifier: identifierField(t, kind), password: z.string(), region: z.string(), agree: z.boolean() })
        .superRefine((v, ctx) => {
          const failed = passwordChecks(v.password, [parsedOrNull(v.identifier, kind)?.value ?? v.identifier]).find((c) => !c.ok);
          if (failed) ctx.addIssue({ code: "custom", path: ["password"], message: t(`pcAuth.rule.${failed.rule}`) });
          if (kind === "EMAIL" && !REGION_SET.has(v.region)) ctx.addIssue({ code: "custom", path: ["region"], message: t("pcAuth.regionRequired") });
          if (!v.agree) ctx.addIssue({ code: "custom", path: ["agree"], message: t("pcAuth.termsRequired") });
        }),
    [t, kind],
  );
  const form = useZodForm(schema, { defaultValues: { identifier: "", password: "", region: browserRegion(), agree: false } });
  const [identifier, password] = useWatch({ control: form.control, name: ["identifier", "password"] });

  const act = (a: RegisterAction) => {
    const next = registerReducer(state, a);
    dispatch(a);
    return next;
  };

  const create = async (ticket: string) => {
    const v = form.getValues();
    if (!terms.data) return;
    try {
      const tokens = await register({
        ticket,
        password: v.password,
        country: v.region,
        language: locale,
        timeZone: useSettings.getState().timeZone || browserTimeZone(),
        terms: terms.data,
      });
      completing.current = true;
      signIn(tokens);
      toast.success(t("pcAuth.welcomeTitle"), { description: t("pcAuth.welcomeBody"), duration: 6000 });
      navigate(routes.assets, { replace: true });
    } catch (e) {
      act({ type: "failed", error: e });
      const fresh = termsFrom(e);
      if (fresh) {
        qc.setQueryData(termsKey, fresh);
        form.setValue("agree", false);
      }
      const code = e instanceof ApiError ? e.code : "";
      if (code === "AUTH_TICKET_INVALID" || code.startsWith("AUTH_OTP_")) {
        setVerifyError(e);
        return;
      }
      setServerError(form, e, { AUTH_PASSWORD_WEAK: "password", AUTH_TERMS_OUTDATED: "agree", AUTH_IDENTITY_TAKEN: "identifier" });
    }
  };

  const onContinue = (v: Values) => {
    const parsed = parsedOrNull(v.identifier, kind);
    if (!parsed || !terms.data) return;
    setVerifyError(null);
    const next = act({ type: "continue", identifier: parsed.value, now: Date.now() });
    if (next.step === "submitting") return create(next.ticket);
  };

  const onTicket = (ticket: string) => {
    act({ type: "ticket", ticket, now: Date.now() });
    void create(ticket);
  };

  const switchKind = (k: IdentityKind) => {
    setKind(k);
    form.setValue("identifier", "");
    form.clearErrors();
  };

  // A session may still be restoring from the refresh cookie: wait rather than flash the form.
  if (restoring) return <AuthPending />;
  if (signedIn && !completing.current) return <Navigate to={routes.assets} replace />;

  const footer = (
    <p>
      {t("pcAuth.haveAccount")}{" "}
      <Link to={routes.login} className="font-medium text-brand hover:underline">
        {t("nav.login")}
      </Link>
    </p>
  );

  if (state.step === "submitting") {
    return (
      <AuthCard key="submitting" icon={<UserPlus size={22} />} title={t("pcAuth.registerTitle")} footer={footer}>
        <div role="status" className="flex flex-col items-center gap-3 rounded-3 border border-line-1 bg-bg-1 py-12 text-sm text-fg-2">
          <Spinner size={24} />
          {t("pcAuth.creating")}
        </div>
      </AuthCard>
    );
  }

  if (state.step === "verify") {
    return (
      <AuthCard
        key="verify"
        icon={kind === "EMAIL" ? <Mail size={22} /> : <Smartphone size={22} />}
        title={kind === "EMAIL" ? t("pcAuth.verifyEmailTitle") : t("pcAuth.verifyPhoneTitle")}
        subtitle={t("pcAuth.verifyHint", { target: state.identifier })}
        footer={
          <button type="button" onClick={() => act({ type: "back" })} className="inline-flex items-center gap-1 text-fg-2 hover:text-fg-1">
            <ArrowLeft size={14} /> {t("pcAuth.editForm")}
          </button>
        }
      >
        <div className="flex flex-col gap-4">
          <OtpStep
            key={`${state.identifier}-${state.attempt}`}
            target={{ scene: "REGISTER", channel: channelOf(kind), identifier: state.identifier }}
            sentTo={state.identifier}
            confirmLabel={t("pcAuth.createAccount")}
            onTicket={onTicket}
          />
          {verifyError != null && (
            <p role="alert" className="text-sm text-danger">
              {errorText(verifyError)}
            </p>
          )}
          <p className="text-xs leading-relaxed text-fg-3">{t("pcAuth.takenHint")}</p>
          {kind === "PHONE" && (
            <Button
              variant="secondary"
              icon={<Mail size={14} />}
              onClick={() => {
                switchKind("EMAIL");
                act({ type: "back" });
              }}
            >
              {t("pcAuth.useEmail")}
            </Button>
          )}
        </div>
      </AuthCard>
    );
  }

  return (
    <AuthCard key="form" icon={<UserPlus size={22} />} title={t("pcAuth.registerTitle")} subtitle={t("pcAuth.registerSubtitle")} footer={footer}>
      <Form form={form} onSubmit={onContinue} aria-label={t("pcAuth.registerTitle")}>
        <Segmented
          block
          size="md"
          value={kind}
          onValueChange={(v) => switchKind(v as IdentityKind)}
          aria-label={t("pcAuth.signUpWith")}
          items={[
            { value: "EMAIL", label: t("pcAuth.email"), icon: <Mail size={14} /> },
            { value: "PHONE", label: t("pcAuth.phone"), icon: <Smartphone size={14} /> },
          ]}
        />
        <FormError />
        <FormField
          label={kind === "EMAIL" ? t("pcAuth.email") : t("pcAuth.phone")}
          name="identifier"
          required
          hint={kind === "PHONE" ? t("pcAuth.phoneHint") : undefined}
        >
          <Input
            {...form.register("identifier")}
            key={kind}
            size="lg"
            autoFocus
            type={kind === "EMAIL" ? "email" : "tel"}
            inputMode={kind === "EMAIL" ? "email" : "tel"}
            autoComplete={kind === "EMAIL" ? "email" : "tel"}
            placeholder={kind === "EMAIL" ? t("pcAuth.emailPlaceholder") : t("pcAuth.phonePlaceholder")}
            prefix={kind === "EMAIL" ? <Mail size={16} /> : <Smartphone size={16} />}
          />
        </FormField>
        {kind === "PHONE" && (
          <p className="flex items-start gap-2 rounded-2 bg-info/10 p-3 text-xs leading-relaxed text-info">
            <Info size={14} className="mt-px shrink-0" />
            {t("pcAuth.smsHint")}
          </p>
        )}
        <FormField label={t("pcAuth.newPassword")} name="password" required>
          <PasswordInput {...form.register("password")} size="lg" autoComplete="new-password" placeholder={t("pcAuth.newPasswordPlaceholder")} />
        </FormField>
        <PasswordStrength password={password ?? ""} identifiers={identifier ? [parsedOrNull(identifier, kind)?.value ?? identifier] : []} />
        {kind === "EMAIL" && (
          <FormField label={t("pcAuth.region")} name="region" required hint={t("pcAuth.regionHint")}>
            {() => (
              <Controller
                control={form.control}
                name="region"
                render={({ field }) => <RegionPicker value={field.value} onChange={field.onChange} locale={locale} />}
              />
            )}
          </FormField>
        )}
        <TermsField form={form} terms={terms} />
        <FormSubmit block size="lg" disabled={!terms.data}>
          {t("pcAuth.continue")}
        </FormSubmit>
      </Form>
    </AuthCard>
  );
}

function RegionPicker({ value, onChange, locale }: { value: string; onChange: (v: string) => void; locale: string }) {
  const { t } = useTranslation();
  const items = useMemo<ComboboxItem[]>(() => {
    const other = locale === "en" ? "zh-CN" : "en";
    const collator = new Intl.Collator(locale);
    return REGIONS.map((code) => ({
      value: code,
      label: regionName(code, locale),
      description: code,
      keywords: [code, regionName(code, other)],
    })).sort((a, b) => collator.compare(a.label, b.label));
  }, [locale]);
  return (
    <Combobox
      items={items}
      value={value}
      onValueChange={onChange}
      size="lg"
      className="w-full"
      searchPlaceholder={t("pcAuth.regionSearch")}
      aria-label={t("pcAuth.region")}
    />
  );
}

function TermsField({ form, terms }: { form: UseFormReturn<Values>; terms: ReturnType<typeof useTerms> }) {
  const { t } = useTranslation();
  if (terms.isPending) return <Skeleton className="h-5 w-full" />;
  if (terms.isError) {
    return (
      <div role="alert" className="flex items-center justify-between gap-2 rounded-2 border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger">
        <span>
          {t("pcAuth.termsLoadFailed")}: {errorText(terms.error)}
        </span>
        <Button size="sm" variant="ghost" onClick={() => void terms.refetch()}>
          {t("common.retry")}
        </Button>
      </div>
    );
  }
  return (
    <Controller
      control={form.control}
      name="agree"
      render={({ field, fieldState }) => (
        <div className="flex flex-col gap-1">
          <Checkbox
            checked={field.value}
            onCheckedChange={field.onChange}
            invalid={Boolean(fieldState.error)}
            label={
              <span className="text-fg-2">
                {/* The strings carry their own spaces (none in Chinese). */}
                {t("pcAuth.agreeBefore")}
                <span className="text-fg-1">{t("pcAuth.termsName")}</span>
                {t("pcAuth.version", { v: terms.data.terms_version })}
                {t("pcAuth.and")}
                <span className="text-fg-1">{t("pcAuth.riskName")}</span>
                {t("pcAuth.version", { v: terms.data.risk_disclosure_version })}
              </span>
            }
          />
          {fieldState.error && (
            <p role="alert" className="text-xs text-danger">
              {fieldState.error.message}
            </p>
          )}
        </div>
      )}
    />
  );
}
