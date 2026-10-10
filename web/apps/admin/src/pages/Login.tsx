import { errorText } from "@exchange/core";
import { adminApi, adminData, type Admin } from "@exchange/core/api/admin";
import { cn } from "@exchange/ui";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router";
import { Check } from "../kit/Check";
import { nextPath } from "../next";
import { CodeInput } from "./login/CodeInput";
import { BrandPanel, Field } from "./login/parts";

/**
 * Login (design 2026-10-02 §6): the brand on the left half (a grid with
 * two drifting lights and the wordmark drawn), the form on the right.
 * Email, password and a one-time authenticator code in six boxes (each
 * code works once); the code is left out while the setting
 * admin.require_totp is off (GET /admin/v1/login-options, N1). Signing
 * in shows a progress bar, then a check before the console opens.
 */
export default function Login() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { search } = useLocation();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [done, setDone] = useState(false);
  const options = useQuery({
    queryKey: ["admin", "login-options"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/login-options")),
    staleTime: 60_000,
  });
  // The code is asked for unless the options say otherwise; when they
  // cannot be read the field shows (the server decides anyway). Until they
  // arrive the form waits, so a fast Enter is not stopped by a code field
  // that is about to go away.
  const askCode = options.data?.totp_required ?? options.isError;
  const login = useMutation({
    mutationFn: async () =>
      adminData(await adminApi.POST("/admin/v1/login", { body: askCode ? { email, password, totp_code: code } : { email, password } })),
    onSuccess: (res) => {
      setDone(true);
      // The check is drawn before the console takes over, at the page the
      // visitor was sent here from.
      const open = (admin: Admin) => {
        qc.setQueryData(["admin", "me"], admin);
        navigate(nextPath(search), { replace: true });
      };
      if (matchMedia("(prefers-reduced-motion: reduce)").matches) open(res.admin);
      else setTimeout(() => open(res.admin), 480);
    },
    onError: () => setCode(""),
  });
  const busy = login.isPending || options.isPending;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!busy && !done) login.mutate();
  };
  return (
    <div className="grid min-h-dvh bg-bg-0 lg:grid-cols-[minmax(0,1.1fr)_minmax(0,1fr)]">
      <BrandPanel />
      <main className="grid place-items-center p-6">
        <form onSubmit={submit} className="card w-full max-w-sm p-8 animate-[rise_480ms_var(--ease)_120ms_backwards]">
          <div className="mb-1 text-lg font-semibold">{t("admin.loginTitle")}</div>
          <p className="mb-6 text-sm text-fg-3">{t("admin.loginSubtitle")}</p>
          <Field label={t("admin.email")} type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} />
          <Field
            label={t("admin.password")}
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          {askCode && (
            <div className="mb-4">
              <div className="mb-1.5 text-sm text-fg-2">{t("admin.totp")}</div>
              <CodeInput value={code} onChange={setCode} label={t("admin.totp")} />
            </div>
          )}
          {login.isError && (
            <p role="alert" className="mb-3 text-sm text-danger-strong">
              {errorText(login.error)}
            </p>
          )}
          <button
            type="submit"
            disabled={busy || done}
            className={cn(
              "relative mt-2 flex h-11 w-full items-center justify-center gap-2 overflow-hidden rounded-2 bg-brand font-medium text-brand-fg transition-[filter,transform] duration-[var(--t-fast)]",
              "hover:brightness-110 active:scale-[0.98] disabled:cursor-default disabled:hover:brightness-100",
            )}
          >
            {done ? (
              <>
                <Check size={20} className="text-brand-fg" />
                {t("admin.loggedIn")}
              </>
            ) : login.isPending ? (
              t("admin.loggingIn")
            ) : (
              <>
                {t("admin.login")}
                <ArrowRight size={16} />
              </>
            )}
            {login.isPending && (
              <span aria-hidden className="absolute inset-x-0 bottom-0 h-[3px] overflow-hidden bg-brand-fg/15">
                <span className="block h-full w-2/5 animate-indeterminate bg-brand-fg/60" />
              </span>
            )}
          </button>
          <p className="mt-5 text-xs leading-relaxed text-fg-3">{t("admin.loginHint")}</p>
        </form>
      </main>
    </div>
  );
}
