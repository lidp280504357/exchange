import { ApiError, errorText, routes, selectRestoring, selectSignedIn, useSession } from "@exchange/core";
import type { ParsedIdentifier } from "@exchange/core/auth/identity";
import {
  completeLoginChallenge, initialLoginState, lockLeft, loginReducer, loginWithPassword, loginWithTicket, signIn,
  type LoginAction, type LoginChallenge, type LoginState, type Tokens,
} from "@exchange/core/auth/login";
import {
  Form, FormError, FormField, FormSubmit, Input, Segmented, Tabs, Turnstile, setServerError, toast, useNow, useWatch, useZodForm, z,
} from "@exchange/ui";
import { ArrowLeft, Mail, Smartphone, TriangleAlert } from "lucide-react";
import { useMemo, useReducer, useRef, useState, type Dispatch } from "react";
import { useTranslation } from "react-i18next";
import { Navigate, useLocation, useNavigate, useSearchParams } from "react-router";
import { OtpStep } from "../../features/auth/OtpStep";
import { safeNext } from "../../routing";
import { AuthPending, AuthScreen } from "./parts/AuthScreen";
import { clock, parsedOrNull } from "./parts/fields";
import { PasswordInput } from "./parts/PasswordInput";
import { identifierField } from "./parts/schemas";
import { TextButton, TextLink } from "./parts/TextButton";

/**
 * Login (design §7.2 认证), full screen: password sign-in with an email
 * or phone, the human check from the third failure, the lock countdown,
 * the 7-day sign-in check by a LOGIN_CHALLENGE code, and sign-in by code
 * as the other tab. The flow is the PC site's (core auth/login); success
 * keeps the session and returns to ?next=.
 */
export default function Login() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const location = useLocation();
  const signedIn = useSession(selectSignedIn);
  const restoring = useSession(selectRestoring);
  const [state, dispatch] = useReducer(loginReducer, initialLoginState);
  const [tab, setTab] = useState<"password" | "code">("password");
  // The account last tried, kept when the page comes back from the sign-in check.
  const [lastId, setLastId] = useState("");
  // Set while this page signs in, so the "already signed in" redirect
  // below does not race the page's own navigation.
  const completing = useRef(false);
  const dest = safeNext(params.get("next"));
  const remembered = (location.state as { identifier?: string } | null)?.identifier ?? "";

  const done = (tokens: Tokens) => {
    completing.current = true;
    signIn(tokens);
    if (tokens.scope === "read") toast.info(t("errors.USER_FROZEN"));
    navigate(dest, { replace: true });
  };

  // A session may still be restoring from the refresh cookie: wait rather than flash the form.
  if (restoring) return <AuthPending />;
  if (signedIn && !completing.current) return <Navigate to={dest} replace />;

  if (state.step === "challenge" && state.challenge) {
    return (
      <AuthScreen
        key="challenge"
        title={t("mAuth.challengeTitle")}
        subtitle={t("mAuth.challengeSubtitle")}
        footer={
          <TextButton tone="muted" onClick={() => dispatch({ type: "back" })}>
            <ArrowLeft size={16} /> {t("mAuth.backToLogin")}
          </TextButton>
        }
      >
        <ChallengeStep challenge={state.challenge} dispatch={dispatch} onDone={done} />
      </AuthScreen>
    );
  }

  return (
    <AuthScreen
      key="login"
      title={t("mAuth.loginTitle")}
      subtitle={t("mAuth.loginSubtitle")}
      footer={
        <>
          <span>{t("mAuth.noAccount")}</span>
          <TextLink to={routes.register}>{t("mAuth.signUpNow")}</TextLink>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <Tabs
          block
          size="lg"
          value={tab}
          onValueChange={(v) => setTab(v as "password" | "code")}
          aria-label={t("mAuth.loginMethod")}
          items={[
            { value: "password", label: t("mAuth.tabPassword") },
            { value: "code", label: t("mAuth.tabCode") },
          ]}
        />
        {tab === "password" ? (
          <PasswordForm state={state} dispatch={dispatch} onDone={done} onTried={setLastId} defaultIdentifier={lastId || remembered} />
        ) : (
          <CodeLogin onDone={done} />
        )}
      </div>
    </AuthScreen>
  );
}

type PasswordValues = { identifier: string; password: string };

function PasswordForm({
  state, dispatch, onDone, onTried, defaultIdentifier,
}: {
  state: LoginState;
  dispatch: Dispatch<LoginAction>;
  onDone: (t: Tokens) => void;
  onTried: (identifier: string) => void;
  defaultIdentifier: string;
}) {
  const { t } = useTranslation();
  const schema = useMemo(
    () => z.object({ identifier: identifierField(t), password: z.string().min(1, t("mAuth.passwordRequired")) }),
    [t],
  );
  const form = useZodForm(schema, { defaultValues: { identifier: defaultIdentifier, password: "" } });
  const [captcha, setCaptcha] = useState("");
  const typed = useWatch({ control: form.control, name: "identifier" });
  const id = parsedOrNull(typed ?? "")?.value ?? "";
  // Tick every second only while a lock counts down.
  const now = useNow(state.lockedUntil > Date.now() ? 1000 : 60_000);
  const lock = lockLeft(state, id, Math.max(now, Date.now()));

  // fresh is a token that just arrived (state has not caught up yet).
  const submit = async (v: PasswordValues, fresh?: string) => {
    const parsed = parsedOrNull(v.identifier);
    if (!parsed || lockLeft(state, parsed.value, Date.now()) > 0) return;
    const token = fresh ?? captcha;
    if (state.captcha && !token) return; // the widget has not answered yet
    setCaptcha("");
    onTried(parsed.value);
    dispatch({ type: "submit" });
    try {
      onDone(await loginWithPassword({ identifier: parsed.value, password: v.password, captchaToken: token || undefined }));
    } catch (e) {
      dispatch({ type: "failed", error: e, now: Date.now(), identifier: parsed.value });
      const code = e instanceof ApiError ? e.code : "";
      // The challenge, the human check and the lock have their own UI.
      if (code === "AUTH_LOGIN_CHALLENGE_REQUIRED" || code === "AUTH_CAPTCHA_REQUIRED" || code === "AUTH_ACCOUNT_LOCKED") return;
      setServerError(form, e, { AUTH_PASSWORD_INVALID: "password", USER_CLOSED: "identifier" });
    }
  };

  // After AUTH_CAPTCHA_REQUIRED the attempt goes again once a token arrives.
  const retry = useRef(submit);
  retry.current = submit;
  const onToken = (token: string) => {
    setCaptcha(token);
    if (token && state.retryOnToken) void form.handleSubmit((v) => retry.current(v, token))();
  };

  return (
    <Form form={form} onSubmit={(v) => submit(v)} aria-label={t("mAuth.tabPassword")}>
      {state.challengeExpired && (
        <p role="alert" className="rounded-2 border border-warn/40 bg-warn/10 p-3 text-sm text-warn">
          {t("mAuth.challengeExpired")}
        </p>
      )}
      <FormError />
      <FormField label={t("mAuth.identifier")} name="identifier" required>
        <Input
          {...form.register("identifier")}
          size="lg"
          autoFocus={!defaultIdentifier}
          autoComplete="username"
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          enterKeyHint="next"
          placeholder={t("mAuth.identifierPlaceholder")}
          prefix={id.startsWith("+") ? <Smartphone size={18} /> : <Mail size={18} />}
        />
      </FormField>
      <div className="flex flex-col">
        <FormField label={t("mAuth.password")} name="password" required>
          <PasswordInput
            {...form.register("password")}
            autoFocus={Boolean(defaultIdentifier)}
            autoComplete="current-password"
            enterKeyHint="go"
            placeholder={t("mAuth.passwordPlaceholder")}
          />
        </FormField>
        {/* After the field, so the keyboard goes identifier, password, submit. */}
        <TextLink to={routes.reset} className="self-end">
          {t("mAuth.forgot")}
        </TextLink>
      </div>
      {lock > 0 && (
        <div role="alert" className="flex items-start gap-2 rounded-2 border border-danger/40 bg-danger/10 px-3 pt-3 text-sm text-danger">
          <TriangleAlert size={16} className="mt-0.5 shrink-0" />
          <div className="flex min-w-0 flex-col">
            {/* Announced once: the ticking clock is not a live update. */}
            <span aria-live="off">{t("mAuth.locked", { time: clock(lock) })}</span>
            <TextLink to={routes.reset} tone="danger" className="underline">
              {t("mAuth.lockedReset")}
            </TextLink>
          </div>
        </div>
      )}
      {state.captcha && lock === 0 && (
        <div className="flex flex-col gap-2">
          <p className="text-xs text-fg-3">{t("mAuth.captchaHint")}</p>
          <Turnstile onToken={onToken} generation={state.generation} />
        </div>
      )}
      <FormSubmit block size="lg" disabled={lock > 0 || (state.captcha && !captcha)}>
        {t("nav.login")}
      </FormSubmit>
    </Form>
  );
}

function ChallengeStep({ challenge, dispatch, onDone }: { challenge: LoginChallenge; dispatch: Dispatch<LoginAction>; onDone: (t: Tokens) => void }) {
  const { t } = useTranslation();
  const channels = challenge.channels.length > 0 ? challenge.channels : [{ channel: "EMAIL" as const, target: "" }];
  const [channel, setChannel] = useState(channels[0]!.channel);
  const [attempt, setAttempt] = useState(0);
  const [error, setError] = useState<unknown>(null);
  const target = channels.find((c) => c.channel === channel)?.target || undefined;

  const finish = async (ticket: string) => {
    setError(null);
    try {
      onDone(await completeLoginChallenge(challenge.id, ticket));
    } catch (e) {
      setError(e);
      setAttempt((a) => a + 1);
      dispatch({ type: "challengeFailed", error: e });
    }
  };

  return (
    <div className="flex flex-col gap-4">
      {channels.length > 1 && (
        <div className="flex flex-col gap-2">
          <span className="text-sm text-fg-2">{t("mAuth.challengeVia")}</span>
          <Segmented
            block
            size="lg"
            value={channel}
            onValueChange={(v) => setChannel(v as typeof channel)}
            aria-label={t("mAuth.challengeVia")}
            items={channels.map((c) => ({
              value: c.channel,
              label: c.channel === "EMAIL" ? t("mAuth.email") : t("mAuth.phone"),
              icon: c.channel === "EMAIL" ? <Mail size={16} /> : <Smartphone size={16} />,
            }))}
          />
        </div>
      )}
      {target && <p className="break-all rounded-2 bg-bg-1 px-3 py-2.5 text-sm text-fg-2">{t("mAuth.codeGoesTo", { target })}</p>}
      <OtpStep
        key={`${channel}-${attempt}`}
        target={{ scene: "LOGIN_CHALLENGE", loginChallengeId: challenge.id, channel }}
        sentTo={target}
        confirmLabel={t("nav.login")}
        onTicket={(ticket) => void finish(ticket)}
      />
      {error != null && (
        <p role="alert" className="text-sm text-danger">
          {errorText(error)}
        </p>
      )}
    </div>
  );
}

function CodeLogin({ onDone }: { onDone: (t: Tokens) => void }) {
  const { t } = useTranslation();
  const [target, setTarget] = useState<ParsedIdentifier | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [error, setError] = useState<unknown>(null);
  const schema = useMemo(() => z.object({ identifier: identifierField(t) }), [t]);
  const form = useZodForm(schema, { defaultValues: { identifier: "" } });

  const finish = async (ticket: string) => {
    setError(null);
    try {
      onDone(await loginWithTicket(ticket));
    } catch (e) {
      setError(e);
      setAttempt((a) => a + 1);
    }
  };

  if (!target) {
    return (
      <Form form={form} onSubmit={(v) => setTarget(parsedOrNull(v.identifier))} aria-label={t("mAuth.tabCode")}>
        <p className="text-sm text-fg-3">{t("mAuth.codeLoginHint")}</p>
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

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-3 rounded-2 bg-bg-1 pl-3 pr-1 text-sm">
        <span className="min-w-0 truncate text-fg-2">{t("mAuth.codeGoesTo", { target: target.value })}</span>
        <TextButton onClick={() => setTarget(null)} className="px-2">
          {t("mAuth.change")}
        </TextButton>
      </div>
      <OtpStep
        key={attempt}
        target={{ scene: "LOGIN", channel: target.channel, identifier: target.value }}
        sentTo={target.value}
        confirmLabel={t("nav.login")}
        onTicket={(ticket) => void finish(ticket)}
      />
      {error != null && (
        <p role="alert" className="text-sm text-danger">
          {errorText(error)}
        </p>
      )}
      <p className="text-xs text-fg-3">{t("mAuth.noCodeHint")}</p>
    </div>
  );
}
