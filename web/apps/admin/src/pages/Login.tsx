import { errorText } from "@exchange/core";
import { adminApi, adminData } from "@exchange/core/api/admin";
import { Button } from "@exchange/ui";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";

/**
 * Login: email, password and a one-time authenticator code (each code
 * works once). The code field is left out while the flag
 * admin.login_without_totp is on (GET /admin/v1/login-options).
 */
export default function Login() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
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
      qc.setQueryData(["admin", "me"], res.admin);
      navigate("/", { replace: true });
    },
    onError: () => setCode(""),
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    login.mutate();
  };
  const field = "h-10 w-full rounded-2 border border-line-2 bg-bg-1 px-3 text-base text-fg-1 outline-none transition-colors focus:border-brand";
  return (
    <div className="grid min-h-dvh place-items-center bg-bg-0 p-4">
      <form onSubmit={submit} className="w-full max-w-sm rounded-3 border border-line-1 bg-bg-1 p-8 shadow-pop">
        <div className="mb-6 flex items-center gap-2 text-lg font-semibold">
          <span className="grid size-8 place-items-center rounded-2 bg-brand text-brand-fg">A</span>
          {t("admin.title")}
        </div>
        <label className="mb-3 block text-sm text-fg-2">
          {t("admin.email")}
          <input className={field} type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} />
        </label>
        <label className="mb-3 block text-sm text-fg-2">
          {t("admin.password")}
          <input className={field} type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        </label>
        {askCode && (
          <label className="mb-4 block text-sm text-fg-2">
            {t("admin.totp")}
            <input
              className={field}
              inputMode="numeric"
              autoComplete="one-time-code"
              pattern="[0-9]{6}"
              maxLength={6}
              required
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
            />
          </label>
        )}
        {login.isError && (
          <p role="alert" className="mb-3 text-sm text-danger">
            {errorText(login.error)}
          </p>
        )}
        <Button type="submit" block loading={login.isPending || options.isPending}>
          {login.isPending ? t("admin.loggingIn") : t("admin.login")}
        </Button>
        <p className="mt-4 text-xs text-fg-3">{t("admin.loginHint")}</p>
      </form>
    </div>
  );
}
