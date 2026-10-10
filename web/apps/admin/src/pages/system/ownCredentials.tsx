import { errorText } from "@exchange/core";
import { adminApi, adminData } from "@exchange/core/api/admin";
import { Button, CopyButton, Input, QrCode, toast } from "@exchange/ui";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { CodeInput } from "../login/CodeInput";

// One's own password and authenticator (C5.5 ⑪): the current password
// proves it is them; a change ends their other sessions.

const MIN_PASSWORD = 12;

/** PasswordForm changes the signed-in administrator's password. */
export function PasswordForm({ onChanged }: { onChanged?: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const change = useMutation({
    mutationFn: async () => {
      const res = await adminApi.POST("/admin/v1/me/password", { body: { current_password: current, new_password: next } });
      if (!res.response.ok) adminData(res);
    },
    onSuccess: async () => {
      setCurrent("");
      setNext("");
      setAgain("");
      toast.success(t("admin.account.passwordChanged"));
      await qc.invalidateQueries({ queryKey: ["admin", "me"] });
      onChanged?.();
    },
  });
  const short = next.length > 0 && next.length < MIN_PASSWORD;
  const mismatch = again.length > 0 && again !== next;
  const ready = current.length > 0 && next.length >= MIN_PASSWORD && again === next;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (ready && !change.isPending) change.mutate();
  };
  return (
    <form onSubmit={submit} className="flex max-w-sm flex-col gap-3" data-testid="own-password">
      <label className="flex flex-col gap-1.5 text-sm text-fg-2">
        {t("admin.account.current")}
        <Input type="password" autoComplete="current-password" value={current} onValueChange={setCurrent} id="own-current" />
      </label>
      <label className="flex flex-col gap-1.5 text-sm text-fg-2">
        {t("admin.account.newPassword", { n: MIN_PASSWORD })}
        <Input
          type="password"
          autoComplete="new-password"
          value={next}
          onValueChange={setNext}
          id="own-new"
          error={short ? t("admin.setup.short", { n: MIN_PASSWORD }) : undefined}
        />
      </label>
      <label className="flex flex-col gap-1.5 text-sm text-fg-2">
        {t("admin.setup.again")}
        <Input
          type="password"
          autoComplete="new-password"
          value={again}
          onValueChange={setAgain}
          id="own-again"
          error={mismatch ? t("admin.setup.mismatch") : undefined}
        />
      </label>
      {change.isError && (
        <p role="alert" className="text-sm text-danger-strong">
          {errorText(change.error)}
        </p>
      )}
      <div>
        <Button type="submit" loading={change.isPending} disabled={!ready}>
          {t("admin.account.changePassword")}
        </Button>
      </div>
      <p className="text-xs text-fg-3">{t("admin.account.passwordHint")}</p>
    </form>
  );
}

/** useLoginOptions reads whether sign-in asks for the authenticator code (admin.require_totp, N1). */
function useLoginOptions() {
  return useQuery({
    queryKey: ["admin", "login-options"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/login-options")),
    staleTime: 60_000,
  });
}

/**
 * TotpForm moves the signed-in administrator to a new authenticator: the
 * current password (and, while sign-in asks for codes, the current
 * authenticator's code) starts it, the new one's code binds it within ten
 * minutes; the old one signs in until then. Bound, it counts for the
 * sign-in code switch (N1).
 */
export function TotpForm() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const options = useLoginOptions();
  const askCode = options.data?.totp_required ?? true;
  const [current, setCurrent] = useState("");
  const [oldCode, setOldCode] = useState("");
  const [pending, setPending] = useState<{ totp_secret: string; totp_uri: string } | null>(null);
  const [code, setCode] = useState("");
  const start = useMutation({
    mutationFn: async () =>
      adminData(
        await adminApi.POST("/admin/v1/me/totp/start", {
          body: askCode ? { current_password: current, totp_code: oldCode } : { current_password: current },
        }),
      ),
    onSuccess: (res) => {
      setPending(res);
      setCurrent("");
      setOldCode("");
    },
    onError: () => setOldCode(""),
  });
  const bind = useMutation({
    mutationFn: async () => {
      const res = await adminApi.POST("/admin/v1/me/totp", { body: { totp_code: code } });
      if (!res.response.ok) adminData(res);
    },
    onSuccess: async () => {
      setPending(null);
      setCode("");
      toast.success(t("admin.account.totpChanged"));
      await qc.invalidateQueries({ queryKey: ["admin", "me"] });
    },
    onError: () => setCode(""),
  });
  if (pending) {
    const submit = (e: FormEvent) => {
      e.preventDefault();
      if (code.length === 6 && !bind.isPending) bind.mutate();
    };
    return (
      <form onSubmit={submit} className="flex max-w-md flex-col gap-4" data-testid="own-totp-bind">
        <p className="text-sm text-fg-2">{t("admin.setup.scan")}</p>
        <div className="flex items-center gap-4">
          <QrCode value={pending.totp_uri} size={128} label={t("admin.setup.secret")} />
          <div className="min-w-0 flex-1">
            <div className="text-xs text-fg-3">{t("admin.setup.secret")}</div>
            <div className="mt-1 flex items-center gap-2">
              <span className="select-all break-all font-mono text-sm tracking-wider" data-testid="own-totp-secret">
                {pending.totp_secret}
              </span>
              <CopyButton value={pending.totp_secret} size={14} />
            </div>
          </div>
        </div>
        <div>
          <div className="mb-1.5 text-sm text-fg-2">{t("admin.setup.code")}</div>
          <CodeInput value={code} onChange={setCode} label={t("admin.setup.code")} />
        </div>
        {bind.isError && (
          <p role="alert" className="text-sm text-danger-strong">
            {errorText(bind.error)}
          </p>
        )}
        <div className="flex gap-2">
          <Button type="submit" loading={bind.isPending} disabled={code.length !== 6}>
            {t("admin.account.bind")}
          </Button>
          <Button type="button" variant="ghost" onClick={() => setPending(null)}>
            {t("common.cancel")}
          </Button>
        </div>
        <p className="text-xs text-fg-3">{t("admin.account.bindHint")}</p>
      </form>
    );
  }
  const ready = current.length > 0 && (!askCode || oldCode.length === 6);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (ready && !start.isPending && !options.isPending) start.mutate();
  };
  return (
    <form onSubmit={submit} className="flex max-w-sm flex-col gap-3" data-testid="own-totp-start">
      <label className="flex flex-col gap-1.5 text-sm text-fg-2">
        {t("admin.account.current")}
        <Input type="password" autoComplete="current-password" value={current} onValueChange={setCurrent} id="own-totp-current" />
      </label>
      {askCode && (
        <div>
          <div className="mb-1.5 text-sm text-fg-2">{t("admin.account.oldCode")}</div>
          <CodeInput value={oldCode} onChange={setOldCode} label={t("admin.account.oldCode")} />
        </div>
      )}
      {start.isError && (
        <p role="alert" className="text-sm text-danger-strong">
          {errorText(start.error)}
        </p>
      )}
      <div>
        <Button type="submit" loading={start.isPending} disabled={!ready}>
          {t("admin.account.startTotp")}
        </Button>
      </div>
      <p className="text-xs text-fg-3">{t("admin.account.totpHint")}</p>
    </form>
  );
}

/**
 * RemoveTotp unbinds the signed-in administrator's authenticator while
 * sign-in does not ask for its code (N1): the current password and its
 * code prove it is them; while the code is asked, it can only be replaced.
 */
export function RemoveTotp() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const options = useLoginOptions();
  const [current, setCurrent] = useState("");
  const [code, setCode] = useState("");
  const remove = useMutation({
    mutationFn: async () => {
      const res = await adminApi.POST("/admin/v1/me/totp/remove", { body: { current_password: current, totp_code: code } });
      if (!res.response.ok) adminData(res);
    },
    onSuccess: async () => {
      setCurrent("");
      setCode("");
      toast.success(t("admin.account.removed"));
      await qc.invalidateQueries({ queryKey: ["admin", "me"] });
    },
    onError: () => setCode(""),
  });
  if (options.data?.totp_required ?? true) {
    return (
      <p className="text-sm text-fg-3" data-testid="own-totp-remove-locked">
        {t("admin.account.removeLocked")}
      </p>
    );
  }
  const ready = current.length > 0 && code.length === 6;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (ready && !remove.isPending) remove.mutate();
  };
  return (
    <form onSubmit={submit} className="flex max-w-sm flex-col gap-3" data-testid="own-totp-remove">
      <label className="flex flex-col gap-1.5 text-sm text-fg-2">
        {t("admin.account.current")}
        <Input type="password" autoComplete="current-password" value={current} onValueChange={setCurrent} id="own-remove-current" />
      </label>
      <div>
        <div className="mb-1.5 text-sm text-fg-2">{t("admin.account.removeCode")}</div>
        <CodeInput value={code} onChange={setCode} label={t("admin.account.removeCode")} />
      </div>
      {remove.isError && (
        <p role="alert" className="text-sm text-danger-strong">
          {errorText(remove.error)}
        </p>
      )}
      <div>
        <Button type="submit" variant="danger" loading={remove.isPending} disabled={!ready}>
          {t("admin.account.remove")}
        </Button>
      </div>
      <p className="text-xs text-fg-3">{t("admin.account.removeHint")}</p>
    </form>
  );
}
