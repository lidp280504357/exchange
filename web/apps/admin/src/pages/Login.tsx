import { errorText } from "@exchange/core";
import { adminApi, adminData, type Admin } from "@exchange/core/api/admin";
import { cn } from "@exchange/ui";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, BellRing, ScrollText, ShieldCheck } from "lucide-react";
import { useState, type FormEvent, type InputHTMLAttributes } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { Check } from "../kit/Check";
import { Wordmark } from "../layout/Brand";
import { CodeInput } from "./login/CodeInput";

/**
 * Login (design 2026-10-02 §6): the brand on the left half (a grid with
 * two drifting lights and the wordmark drawn), the form on the right.
 * Email, password and a one-time authenticator code in six boxes (each
 * code works once); the code is left out while the flag
 * admin.login_without_totp is on (GET /admin/v1/login-options). Signing
 * in shows a progress bar, then a check before the console opens.
 */
export default function Login() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
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
      // The check is drawn before the console takes over.
      const open = (admin: Admin) => {
        qc.setQueryData(["admin", "me"], admin);
        navigate("/", { replace: true });
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
            <p role="alert" className="mb-3 text-sm text-danger">
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

/** Field is a labelled input whose underline grows from the middle when it has the focus. */
function Field({ label, ...input }: { label: string } & InputHTMLAttributes<HTMLInputElement>) {
  return (
    <label className="group mb-4 block">
      <span className="text-sm text-fg-2">{label}</span>
      <span className="relative mt-1.5 block">
        <input
          {...input}
          className="h-11 w-full rounded-2 border border-line-2 bg-bg-1 px-3 text-base text-fg-1 outline-none transition-colors placeholder:text-fg-3 hover:border-fg-3 focus-visible:outline-none"
        />
        <span
          aria-hidden
          className="pointer-events-none absolute inset-x-1.5 bottom-0 h-0.5 origin-center scale-x-0 rounded-full bg-brand transition-transform duration-[var(--t-base)] ease-out group-focus-within:scale-x-100"
        />
      </span>
    </label>
  );
}

/** BrandPanel is the left half: a grid fading out, two slow lights, the wordmark drawn. */
function BrandPanel() {
  const { t } = useTranslation();
  return (
    <aside data-theme="dark" className="relative hidden overflow-hidden bg-bg-0 text-fg-1 lg:flex lg:flex-col lg:justify-between lg:p-12">
      <div aria-hidden className="login-grid absolute inset-0" />
      <div aria-hidden className="absolute -left-32 top-[12%] size-[30rem] rounded-full bg-brand opacity-20 blur-[100px] animate-drift-a will-change-transform" />
      <div aria-hidden className="absolute -right-24 bottom-[-10%] size-[26rem] rounded-full bg-info opacity-20 blur-[110px] animate-drift-b will-change-transform" />
      <Wordmark animated className="relative h-11 self-start" />
      <div className="relative max-w-md">
        <h1 className="text-2xl font-semibold leading-snug">{t("admin.brandTitle")}</h1>
        <p className="mt-3 text-sm leading-relaxed text-fg-2">{t("admin.brandText")}</p>
        <ul className="mt-8 flex flex-col gap-3 text-sm text-fg-2">
          {[
            { icon: ShieldCheck, key: "brandApprovals" },
            { icon: BellRing, key: "brandLive" },
            { icon: ScrollText, key: "brandAudit" },
          ].map((f, i) => (
            <li key={f.key} className="flex items-center gap-3 animate-[rise_480ms_var(--ease)_backwards]" style={{ animationDelay: `${600 + i * 120}ms` }}>
              <span className="grid size-8 place-items-center rounded-2 bg-brand-soft text-brand">
                <f.icon size={16} />
              </span>
              {t(`admin.${f.key}`)}
            </li>
          ))}
        </ul>
      </div>
      <div className="relative text-xs text-fg-3">{t("admin.brandFoot")}</div>
    </aside>
  );
}
