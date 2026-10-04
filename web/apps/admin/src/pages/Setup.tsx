import { ApiError, errorText } from "@exchange/core";
import { adminApi, adminData } from "@exchange/core/api/admin";
import { Button, CopyButton, ErrorState, QrCode, Spinner } from "@exchange/ui";
import { useMutation, useQuery } from "@tanstack/react-query";
import { ArrowRight } from "lucide-react";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { Check } from "../kit/Check";
import { useTimeText } from "../kit/format";
import { CodeInput } from "./login/CodeInput";
import { BrandPanel, Field } from "./login/parts";

const MIN_PASSWORD = 12;

/**
 * Setup (C5.5 ⑪): the one-time link (/setup#token=…) an ADMIN hands over
 * after creating an account or resetting its password or authenticator.
 * No session: the token is the proof. The token comes in the fragment
 * (no server log has it) and leaves the address bar once read. The page
 * shows the account, asks for the new password twice when the link sets
 * one and shows the new authenticator (QR code and secret) with a field
 * for its code when it binds one. Done, the link is spent and the sign-in
 * follows; whoever handed the link over never learns what signs in.
 */
export default function Setup() {
  const { t } = useTranslation();
  const time = useTimeText();
  const [token] = useState(() => new URLSearchParams(location.hash.slice(1)).get("token") ?? "");
  useEffect(() => {
    if (location.hash) history.replaceState(null, "", location.pathname);
  }, []);
  const view = useQuery({
    queryKey: ["admin", "setup"],
    queryFn: async () => adminData(await adminApi.POST("/admin/v1/setup/inspect", { body: { token } })),
    enabled: token !== "",
    retry: false,
    staleTime: Infinity,
    gcTime: 0,
    refetchOnWindowFocus: false,
  });
  const [pw, setPw] = useState("");
  const [again, setAgain] = useState("");
  const [code, setCode] = useState("");
  const complete = useMutation({
    mutationFn: async () => {
      const v = view.data!;
      const res = await adminApi.POST("/admin/v1/setup", {
        body: { token, ...(v.sets_password ? { password: pw } : {}), ...(v.totp_secret ? { totp_code: code } : {}) },
      });
      if (!res.response.ok) adminData(res);
    },
    onError: () => setCode(""),
  });

  let body: ReactNode;
  if (!token) body = <Gone text={t("admin.setup.missing")} />;
  else if (view.isPending) body = <Spinner size={24} className="mx-auto text-fg-3" />;
  else if (view.isError)
    body =
      view.error instanceof ApiError && view.error.code === "ADMIN_SETUP_INVALID" ? (
        <Gone text={t("admin.setup.invalid")} />
      ) : (
        <ErrorState message={errorText(view.error)} onRetry={() => void view.refetch()} />
      );
  else if (complete.isSuccess)
    body = (
      <div className="flex flex-col items-center gap-3 py-4 text-center" data-testid="setup-done">
        <span className="grid size-12 place-items-center rounded-full bg-success/10">
          <Check size={24} className="text-success" />
        </span>
        <div className="text-lg font-semibold">{t("admin.setup.done")}</div>
        <p className="text-sm text-fg-3">{t(view.data.kind === "TOTP" ? "admin.setup.doneTotp" : "admin.setup.donePassword")}</p>
        <Link to="/login" className="mt-2 inline-flex items-center gap-1.5 text-sm font-medium text-brand-strong hover:underline">
          {t("admin.setup.toLogin")}
          <ArrowRight size={14} />
        </Link>
      </div>
    );
  else {
    const v = view.data;
    const short = v.sets_password && pw.length > 0 && pw.length < MIN_PASSWORD;
    const mismatch = v.sets_password && again.length > 0 && again !== pw;
    const ready = (!v.sets_password || (pw.length >= MIN_PASSWORD && again === pw)) && (!v.totp_secret || code.length === 6);
    const submit = (e: FormEvent) => {
      e.preventDefault();
      if (ready && !complete.isPending) complete.mutate();
    };
    body = (
      <form onSubmit={submit} className="flex flex-col">
        <div className="mb-1 text-lg font-semibold">{t(`admin.setup.title_${v.kind}`)}</div>
        <p className="mb-5 text-sm text-fg-3">{t("admin.setup.until", { time: time(v.expires_at, "datetime") })}</p>
        <div className="mb-5 rounded-2 border border-line-1 bg-bg-2 px-4 py-3 text-sm">
          <div className="font-medium text-fg-1" data-testid="setup-email">
            {v.email}
          </div>
          {v.name && <div className="text-fg-3">{v.name}</div>}
        </div>
        {v.sets_password && (
          <>
            <Field
              label={t("admin.setup.password", { n: MIN_PASSWORD })}
              type="password"
              autoComplete="new-password"
              required
              minLength={MIN_PASSWORD}
              value={pw}
              onChange={(e) => setPw(e.target.value)}
              aria-invalid={short || undefined}
              data-testid="setup-password"
            />
            {short && <p className="-mt-3 mb-3 text-xs text-danger">{t("admin.setup.short", { n: MIN_PASSWORD })}</p>}
            <Field
              label={t("admin.setup.again")}
              type="password"
              autoComplete="new-password"
              required
              value={again}
              onChange={(e) => setAgain(e.target.value)}
              aria-invalid={mismatch || undefined}
              data-testid="setup-again"
            />
            {mismatch && <p className="-mt-3 mb-3 text-xs text-danger">{t("admin.setup.mismatch")}</p>}
          </>
        )}
        {v.totp_secret && v.totp_uri && (
          <div className="mb-4 flex flex-col gap-3">
            <div className="text-sm text-fg-2">{t("admin.setup.scan")}</div>
            <div className="flex items-center gap-4">
              <QrCode value={v.totp_uri} size={128} label={t("admin.setup.secret")} />
              <div className="min-w-0 flex-1">
                <div className="text-xs text-fg-3">{t("admin.setup.secret")}</div>
                <div className="mt-1 flex items-center gap-2">
                  <span className="select-all break-all font-mono text-sm tracking-wider" data-testid="setup-secret">
                    {v.totp_secret}
                  </span>
                  <CopyButton value={v.totp_secret} size={14} />
                </div>
              </div>
            </div>
            <div>
              <div className="mb-1.5 text-sm text-fg-2">{t("admin.setup.code")}</div>
              <CodeInput value={code} onChange={setCode} label={t("admin.setup.code")} />
            </div>
          </div>
        )}
        {complete.isError && (
          <p role="alert" className="mb-3 text-sm text-danger">
            {errorText(complete.error)}
          </p>
        )}
        <Button type="submit" block size="lg" loading={complete.isPending} disabled={!ready} className="mt-2" data-testid="setup-submit">
          {t("admin.setup.submit")}
        </Button>
        <p className="mt-5 text-xs leading-relaxed text-fg-3">{t("admin.setup.hint")}</p>
      </form>
    );
  }
  return (
    <div className="grid min-h-dvh bg-bg-0 lg:grid-cols-[minmax(0,1.1fr)_minmax(0,1fr)]">
      <BrandPanel />
      <main className="grid place-items-center p-6">
        <div className="card w-full max-w-md p-8 animate-[rise_480ms_var(--ease)_120ms_backwards]">{body}</div>
      </main>
    </div>
  );
}

/** Gone says the link cannot be used and leads to the sign-in. */
function Gone({ text }: { text: string }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-3 py-2" data-testid="setup-invalid">
      <div className="text-lg font-semibold">{t("admin.setup.invalidTitle")}</div>
      <p className="text-sm text-fg-3">{text}</p>
      <Link to="/login" className="inline-flex items-center gap-1.5 text-sm font-medium text-brand-strong hover:underline">
        {t("admin.setup.toLogin")}
        <ArrowRight size={14} />
      </Link>
    </div>
  );
}
