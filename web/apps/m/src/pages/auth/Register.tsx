import { ApiError, errorText, LOCALES, routes, selectRestoring, selectSignedIn, useSession, useSettings } from "@exchange/core";
import { channelOf, type IdentityKind } from "@exchange/core/auth/identity";
import { signIn } from "@exchange/core/auth/login";
import { passwordChecks } from "@exchange/core/auth/password";
import {
  browserRegion, initialRegisterState, REGIONS, register, registerReducer, regionName, termsFrom, termsKey, useTerms, type RegisterAction,
} from "@exchange/core/auth/register";
import { registrationOpen, textOf, useBranding, useWelcomeCredits } from "@exchange/core/platform/index";
import { browserTimeZone } from "@exchange/core/user/preferences";
import {
  Button, Checkbox, Controller, Form, FormError, FormField, FormSubmit, Input, Segmented, Skeleton, Spinner, cn, setServerError, toast,
  useWatch, useZodForm, z, type ComboboxItem, type FieldControlProps, type UseFormReturn,
} from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, ChevronRight, Info, Mail, RotateCcw, Smartphone } from "lucide-react";
import { useId, useMemo, useReducer, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Navigate, useNavigate } from "react-router";
import { OtpStep } from "../../features/auth/OtpStep";
import { AuthPending, AuthScreen } from "./parts/AuthScreen";
import { parsedOrNull } from "./parts/fields";
import { PasswordInput } from "./parts/PasswordInput";
import { PasswordStrength } from "./parts/PasswordStrength";
import { PickerSheet } from "./parts/PickerSheet";
import { identifierField } from "./parts/schemas";
import { TextButton, TextLink } from "./parts/TextButton";

type Values = { identifier: string; password: string; region: string; agree: boolean };

const REGION_SET = new Set(REGIONS);

/**
 * Register (design §7.2 认证), full screen: email or phone (email by
 * default: SMS may be switched off), a password with its strength and the
 * live rules, the region for email sign-ups and the current terms; then
 * the REGISTER code, the account, and the assets page with the welcome
 * funds. The flow is the PC site's (core auth/register).
 */
export default function Register() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const signedIn = useSession(selectSignedIn);
  const restoring = useSession(selectRestoring);
  const locale = useSettings((s) => s.locale);
  const terms = useTerms();
  const profile = useBranding();
  const credits = useWelcomeCredits();
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
          if (failed) ctx.addIssue({ code: "custom", path: ["password"], message: t(`mAuth.rule.${failed.rule}`) });
          if (kind === "EMAIL" && !REGION_SET.has(v.region)) ctx.addIssue({ code: "custom", path: ["region"], message: t("mAuth.regionRequired") });
          if (!v.agree) ctx.addIssue({ code: "custom", path: ["agree"], message: t("mAuth.termsRequired") });
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
      toast.success(t("mAuth.welcomeTitle"), {
        description: credits ? t("mAuth.welcomeBodyCredits", { credits }) : t("mAuth.welcomeBody"),
        duration: 6000,
      });
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
    <>
      <span>{t("mAuth.haveAccount")}</span>
      <TextLink to={routes.login}>{t("nav.login")}</TextLink>
    </>
  );

  // Sign-ups closed in the platform profile (design 2026-10-04 §4.3): its text instead of the form.
  if (!registrationOpen(profile) && state.step === "form") {
    return (
      <AuthScreen key="closed" title={t("mAuth.registerTitle")} footer={footer}>
        <p role="status" className="rounded-3 bg-bg-1 p-4 text-sm leading-relaxed text-fg-2">
          {textOf(profile.registration.closed_text, locale) || t("errors.AUTH_REGISTRATION_CLOSED")}
        </p>
      </AuthScreen>
    );
  }

  if (state.step === "submitting") {
    return (
      <AuthScreen key="submitting" title={t("mAuth.registerTitle")} footer={footer}>
        <div role="status" className="flex flex-col items-center gap-3 rounded-3 bg-bg-1 py-16 text-sm text-fg-2">
          <Spinner size={24} />
          {t("mAuth.creating")}
        </div>
      </AuthScreen>
    );
  }

  if (state.step === "verify") {
    return (
      <AuthScreen
        key="verify"
        title={kind === "EMAIL" ? t("mAuth.verifyEmailTitle") : t("mAuth.verifyPhoneTitle")}
        subtitle={<span className="break-all">{t("mAuth.verifyHint", { target: state.identifier })}</span>}
        footer={
          <TextButton tone="muted" onClick={() => act({ type: "back" })}>
            <ArrowLeft size={16} /> {t("mAuth.editForm")}
          </TextButton>
        }
      >
        <div className="flex flex-col gap-4">
          <OtpStep
            key={`${state.identifier}-${state.attempt}`}
            target={{ scene: "REGISTER", channel: channelOf(kind), identifier: state.identifier }}
            sentTo={state.identifier}
            confirmLabel={t("mAuth.createAccount")}
            onTicket={onTicket}
          />
          {verifyError != null && (
            <p role="alert" className="text-sm text-danger">
              {errorText(verifyError)}
            </p>
          )}
          <p className="text-xs leading-relaxed text-fg-3">{t("mAuth.takenHint")}</p>
          {kind === "PHONE" && (
            <Button
              variant="secondary"
              size="lg"
              block
              icon={<Mail size={16} />}
              onClick={() => {
                switchKind("EMAIL");
                act({ type: "back" });
              }}
            >
              {t("mAuth.useEmail")}
            </Button>
          )}
        </div>
      </AuthScreen>
    );
  }

  return (
    <AuthScreen
      key="form"
      title={t("mAuth.registerTitle")}
      subtitle={credits ? t("mAuth.registerSubtitleCredits", { credits }) : t("mAuth.registerSubtitle")}
      footer={footer}
    >
      <Form form={form} onSubmit={onContinue} aria-label={t("mAuth.registerTitle")}>
        <Segmented
          block
          size="lg"
          value={kind}
          onValueChange={(v) => switchKind(v as IdentityKind)}
          aria-label={t("mAuth.signUpWith")}
          items={[
            { value: "EMAIL", label: t("mAuth.email"), icon: <Mail size={16} /> },
            { value: "PHONE", label: t("mAuth.phone"), icon: <Smartphone size={16} /> },
          ]}
        />
        <FormError />
        <FormField
          label={kind === "EMAIL" ? t("mAuth.email") : t("mAuth.phone")}
          name="identifier"
          required
          hint={kind === "PHONE" ? t("mAuth.phoneHint") : undefined}
        >
          <Input
            {...form.register("identifier")}
            key={kind}
            size="lg"
            type={kind === "EMAIL" ? "email" : "tel"}
            inputMode={kind === "EMAIL" ? "email" : "tel"}
            autoComplete={kind === "EMAIL" ? "email" : "tel"}
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
            enterKeyHint="next"
            placeholder={kind === "EMAIL" ? t("mAuth.emailPlaceholder") : t("mAuth.phonePlaceholder")}
            prefix={kind === "EMAIL" ? <Mail size={18} /> : <Smartphone size={18} />}
          />
        </FormField>
        {kind === "PHONE" && (
          <p className="flex items-start gap-2 rounded-2 bg-info/10 p-3 text-xs leading-relaxed text-info">
            <Info size={14} className="mt-px shrink-0" />
            {t("mAuth.smsHint")}
          </p>
        )}
        <FormField label={t("mAuth.newPassword")} name="password" required>
          <PasswordInput {...form.register("password")} autoComplete="new-password" enterKeyHint="next" placeholder={t("mAuth.newPasswordPlaceholder")} />
        </FormField>
        <PasswordStrength password={password ?? ""} identifiers={identifier ? [parsedOrNull(identifier, kind)?.value ?? identifier] : []} />
        {kind === "EMAIL" && (
          <FormField label={t("mAuth.region")} name="region" required hint={t("mAuth.regionHint")}>
            {(control) => (
              <Controller
                control={form.control}
                name="region"
                render={({ field }) => <RegionField control={control} value={field.value} onChange={field.onChange} locale={locale} />}
              />
            )}
          </FormField>
        )}
        <TermsField form={form} terms={terms} />
        <FormSubmit block size="lg" disabled={!terms.data}>
          {t("mAuth.continue")}
        </FormSubmit>
      </Form>
    </AuthScreen>
  );
}

/** RegionField shows the chosen region and opens the searchable list in a sheet. */
function RegionField({ control, value, onChange, locale }: { control: FieldControlProps; value: string; onChange: (v: string) => void; locale: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const items = useMemo<ComboboxItem[]>(() => {
    const collator = new Intl.Collator(locale);
    return REGIONS.map((code) => ({
      value: code,
      label: regionName(code, locale),
      description: code,
      // Found by its name in any of the languages.
      keywords: [code, ...LOCALES.map((l) => regionName(code, l))],
    })).sort((a, b) => collator.compare(a.label, b.label));
  }, [locale]);
  const known = REGION_SET.has(value);
  return (
    <>
      <button
        type="button"
        id={control.id}
        aria-invalid={control["aria-invalid"]}
        aria-describedby={control["aria-describedby"]}
        aria-haspopup="dialog"
        onClick={() => setOpen(true)}
        className={cn(
          "flex h-tap w-full items-center gap-3 rounded-2 border bg-bg-2 px-4 text-left text-md transition-colors active:bg-bg-3",
          control["aria-invalid"] ? "border-danger" : "border-line-1",
        )}
      >
        <span className={cn("min-w-0 flex-1 truncate", known ? "text-fg-1" : "text-fg-3")}>
          {known ? regionName(value, locale) : t("mAuth.regionPick")}
        </span>
        {known && <span className="shrink-0 text-sm text-fg-3">{value}</span>}
        <ChevronRight size={18} className="shrink-0 text-fg-3" aria-hidden />
      </button>
      <PickerSheet
        open={open}
        onOpenChange={setOpen}
        title={t("mAuth.regionPick")}
        items={items}
        value={value}
        onChoose={onChange}
        searchPlaceholder={t("mAuth.regionSearch")}
      />
    </>
  );
}

function TermsField({ form, terms }: { form: UseFormReturn<Values>; terms: ReturnType<typeof useTerms> }) {
  const { t } = useTranslation();
  const id = useId();
  if (terms.isPending) return <Skeleton className="h-tap w-full" />;
  if (terms.isError) {
    return (
      <div role="alert" className="flex items-center justify-between gap-2 rounded-2 border border-danger/40 bg-danger/10 pl-3 pr-1 text-sm text-danger">
        <span className="min-w-0 py-2">
          {t("mAuth.termsLoadFailed")}: {errorText(terms.error)}
        </span>
        <TextButton tone="danger" onClick={() => void terms.refetch()} className="px-2">
          <RotateCcw size={14} /> {t("common.retry")}
        </TextButton>
      </div>
    );
  }
  return (
    <Controller
      control={form.control}
      name="agree"
      render={({ field, fieldState }) => (
        <div className="flex flex-col gap-1">
          {/* The whole row is the target: the label forwards taps to the box. */}
          <label htmlFor={id} className="flex min-h-tap cursor-pointer items-start gap-3 py-1">
            <span className="flex h-5 items-center">
              <Checkbox id={id} checked={field.value} onCheckedChange={field.onChange} invalid={Boolean(fieldState.error)} />
            </span>
            <span className="text-sm leading-5 text-fg-2">
              {/* The strings carry their own spaces (none in Chinese). */}
              {t("mAuth.agreeBefore")}
              <span className="text-fg-1">{t("mAuth.termsName")}</span>
              {t("mAuth.version", { v: terms.data.terms_version })}
              {t("mAuth.and")}
              <span className="text-fg-1">{t("mAuth.riskName")}</span>
              {t("mAuth.version", { v: terms.data.risk_disclosure_version })}
            </span>
          </label>
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
