import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation, useNavigate } from "react-router";
import { ApiError, authApi, sessionFrom, unwrap } from "../api/client";
import { OtpStep } from "../components/OtpStep";
import { Turnstile } from "../components/Turnstile";
import { Button, Card, ErrorText, Field, Notice } from "../components/ui";
import { errorText } from "../i18n";
import { deviceId } from "../lib/device";
import { useSession } from "../store/session";

function AuthShell({ title, children }: { title: string; children: React.ReactNode }) {
  const { t } = useTranslation();
  return (
    <div className="mx-auto flex min-h-screen max-w-sm flex-col justify-center gap-4 p-4">
      <div className="text-center">
        <h1 className="text-2xl font-bold text-[#f0b90b]">{t("app.name")}</h1>
        <p className="text-xs text-gray-500">{t("app.tagline")}</p>
      </div>
      <Card title={title}>{children}</Card>
    </div>
  );
}

type Tokens = Parameters<typeof sessionFrom>[0];

export function LoginPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const setSession = useSession((s) => s.set);
  const [identifier, setIdentifier] = useState("");
  const [password, setPassword] = useState("");
  const [needCaptcha, setNeedCaptcha] = useState(false);
  const [captcha, setCaptcha] = useState("");
  const [generation, setGeneration] = useState(0);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await unwrap(
        authApi.POST("/v1/auth/login/password", {
          body: { identifier: identifier.trim(), password, device_id: deviceId(), captcha_token: captcha || undefined },
        }),
      );
      setSession(sessionFrom(res as Tokens));
      navigate("/");
    } catch (err) {
      if (err instanceof ApiError && err.code === "AUTH_LOGIN_CHALLENGE_REQUIRED") {
        navigate("/login/challenge", { state: { id: err.details.login_challenge_id, channels: err.details.channels } });
        return;
      }
      if (err instanceof ApiError && (err.code === "AUTH_CAPTCHA_REQUIRED" || err.code === "AUTH_CAPTCHA_FAILED" || needCaptcha)) {
        setNeedCaptcha(true);
        setGeneration((g) => g + 1);
      }
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthShell title={t("auth.login")}>
      <form className="space-y-3" onSubmit={submit}>
        <Field label={t("auth.identifier")} autoComplete="username" value={identifier} onChange={(e) => setIdentifier(e.target.value)} />
        <Field label={t("common.password")} type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        {needCaptcha && <Turnstile onToken={setCaptcha} generation={generation} />}
        <ErrorText text={error} />
        <Button className="w-full" disabled={busy || !identifier || !password || (needCaptcha && !captcha)}>
          {t("auth.login")}
        </Button>
      </form>
      <div className="mt-4 flex justify-between text-xs text-gray-400">
        <Link to="/register">{t("auth.noAccount")}</Link>
        <Link to="/reset">{t("auth.forgot")}</Link>
      </div>
    </AuthShell>
  );
}

export function ChallengePage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const setSession = useSession((s) => s.set);
  const state = useLocation().state as { id?: string; channels?: { channel: string; target: string }[] } | null;
  const [error, setError] = useState("");
  if (!state?.id) return <AuthShell title={t("auth.challengeTitle")}><Link to="/login">{t("common.back")}</Link></AuthShell>;
  const target = state.channels?.find((c) => c.channel === "EMAIL")?.target;

  async function complete(ticket: string) {
    try {
      const res = await unwrap(
        authApi.POST("/v1/auth/login/challenge", { body: { otp_ticket: ticket, login_challenge_id: state!.id, device_id: deviceId() } }),
      );
      setSession(sessionFrom(res as Tokens));
      navigate("/");
    } catch (e) {
      setError(errorText(e));
    }
  }
  return (
    <AuthShell title={t("auth.challengeTitle")}>
      <p className="mb-3 text-sm text-gray-400">{t("auth.challengeHint")}</p>
      <OtpStep scene="LOGIN_CHALLENGE" identifier={target} loginChallengeId={state.id} onTicket={complete} />
      <ErrorText text={error} />
    </AuthShell>
  );
}

export function RegisterPage() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const setSession = useSession((s) => s.set);
  const [email, setEmail] = useState("");
  const [ticket, setTicket] = useState("");
  const [password, setPassword] = useState("");
  const [country, setCountry] = useState("SG");
  const [agree, setAgree] = useState(false);
  const [terms, setTerms] = useState<{ terms_version: string; risk_disclosure_version: string } | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function onTicket(tk: string) {
    setTicket(tk);
    try {
      setTerms(await unwrap(authApi.GET("/v1/auth/terms")));
    } catch (e) {
      setError(errorText(e));
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!terms) return;
    setBusy(true);
    setError("");
    try {
      const res = await unwrap(
        authApi.POST("/v1/auth/register/complete", {
          body: {
            otp_ticket: ticket, password, country: country.trim().toUpperCase(), language: i18n.language,
            timezone: Intl.DateTimeFormat().resolvedOptions().timeZone, terms_version: terms.terms_version,
            risk_disclosure_version: terms.risk_disclosure_version, device_id: deviceId(),
          },
        }),
      );
      setSession(sessionFrom(res as Tokens));
      navigate("/");
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthShell title={t("auth.register")}>
      {!ticket ? (
        <div className="space-y-3">
          <Field label={t("common.email")} type="email" autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} />
          {/@/.test(email) && <OtpStep scene="REGISTER" identifier={email.trim()} onTicket={onTicket} />}
        </div>
      ) : (
        <form className="space-y-3" onSubmit={submit}>
          <Field label={t("auth.newPassword")} type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
          <Field label={t("auth.country")} maxLength={2} value={country} onChange={(e) => setCountry(e.target.value)} />
          {terms && (
            <label className="flex items-start gap-2 text-xs text-gray-400">
              <input type="checkbox" checked={agree} onChange={(e) => setAgree(e.target.checked)} />
              {t("auth.agree", { terms: terms.terms_version, risk: terms.risk_disclosure_version })}
            </label>
          )}
          <ErrorText text={error} />
          <Button className="w-full" disabled={busy || !agree || password.length < 10}>
            {t("auth.register")}
          </Button>
        </form>
      )}
      <ErrorText text={!ticket ? error : ""} />
      <div className="mt-4 text-xs text-gray-400">
        <Link to="/login">{t("auth.haveAccount")}</Link>
      </div>
    </AuthShell>
  );
}

export function ResetPage() {
  const { t } = useTranslation();
  const [email, setEmail] = useState("");
  const [ticket, setTicket] = useState("");
  const [password, setPassword] = useState("");
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    try {
      await unwrap(authApi.POST("/v1/auth/password/reset/complete", { body: { otp_ticket: ticket, new_password: password, device_id: deviceId() } }));
      setDone(true);
    } catch (err) {
      setError(errorText(err));
    }
  }

  return (
    <AuthShell title={t("auth.resetTitle")}>
      {done ? (
        <div className="space-y-3">
          <Notice text={t("auth.resetDone")} />
          <Link to="/login" className="text-sm text-[#f0b90b]">{t("auth.login")}</Link>
        </div>
      ) : !ticket ? (
        <div className="space-y-3">
          <Field label={t("common.email")} type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
          {/@/.test(email) && <OtpStep scene="PASSWORD_RESET" identifier={email.trim()} onTicket={setTicket} />}
        </div>
      ) : (
        <form className="space-y-3" onSubmit={submit}>
          <Field label={t("auth.newPassword")} type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
          <ErrorText text={error} />
          <Button className="w-full" disabled={password.length < 10}>{t("common.submit")}</Button>
        </form>
      )}
      <div className="mt-4 text-xs text-gray-400">
        <Link to="/login">{t("common.back")}</Link>
      </div>
    </AuthShell>
  );
}
