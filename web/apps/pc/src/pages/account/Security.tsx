import { enumLabel, errorText, formatDecimal, formatTime, routes, timeZoneOf, useSettings, useTotpStatus } from "@exchange/core";
import { regionName } from "@exchange/core/auth/register";
import { maskCode, useProfile } from "@exchange/core/user/profile";
import {
  disableTotp, refreshTotp, securitySummary, setupTotp, useBoundIdentities, type SecurityFactors, type SecurityLevel, type TotpSetup,
} from "@exchange/core/user/security";
import { useSessions } from "@exchange/core/user/sessions";
import { useWithdrawLimits } from "@exchange/core/wallet/hooks";
import type { WithdrawLimits } from "@exchange/core/wallet/networks";
import { Badge, Button, CopyButton, Dialog, IconButton, Progress, Skeleton, TimeText, cn, toast } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import {
  ArrowRight, Eye, EyeOff, Fish, History, KeyRound, Mail, MonitorSmartphone, ShieldAlert, ShieldCheck, ShieldEllipsis, Smartphone,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { useStepUp } from "../../features/auth/StepUp";
import { AccountLayout, Section, shortId } from "./parts/AccountLayout";
import { AntiPhishingDialog } from "./parts/AntiPhishingDialog";
import { ChangePasswordDialog } from "./parts/ChangePasswordDialog";
import { IdentityDialog, type IdentityTask } from "./parts/IdentityDialog";
import { SecurityItem } from "./parts/SecurityItem";
import { BindTotpDialog } from "./parts/TotpDialogs";

type Open = null | "password" | "totpUnbind" | "antiPhishing";

const levelTone: Record<SecurityLevel, { text: string; bg: string; progress: "danger" | "warn" | "success" }> = {
  low: { text: "text-danger", bg: "bg-danger/10", progress: "danger" },
  medium: { text: "text-warn", bg: "bg-warn/10", progress: "warn" },
  high: { text: "text-success", bg: "bg-success/10", progress: "success" },
};

/**
 * Security (design §6.2 账户): the security centre — a level summary with
 * what is left to do, then cards for the authenticator app, email, phone,
 * password, anti-phishing code, devices and sign-in history, each with
 * its status and action. Sensitive changes ask for a step-up.
 */
export default function Security() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const stepUp = useStepUp();
  const profile = useProfile();
  const totp = useTotpStatus();
  const limits = useWithdrawLimits();
  const ids = useBoundIdentities();
  const sessions = useSessions();
  const [open, setOpen] = useState<Open>(null);
  const [identity, setIdentity] = useState<IdentityTask | null>(null);
  const [setup, setSetup] = useState<TotpSetup | null>(null);
  const [busy, setBusy] = useState<"" | "totpBind" | "totpUnbind">("");

  const bound = ids.data ?? {};
  const totpOn = totp.data?.enabled === true;
  const factors: SecurityFactors = {
    totp: totpOn,
    email: Boolean(bound.EMAIL),
    phone: Boolean(bound.PHONE),
    antiPhishing: Boolean(profile.data?.anti_phishing_code),
  };
  const summaryLoading = totp.isPending || ids.isPending || profile.isPending;

  const startTotp = async () => {
    setBusy("totpBind");
    try {
      const token = await stepUp.ask();
      if (token) setSetup(await setupTotp(token));
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy("");
    }
  };

  const removeTotp = async () => {
    setBusy("totpUnbind");
    try {
      const token = await stepUp.ask();
      if (!token) return;
      await disableTotp(token);
      await refreshTotp(qc);
      toast.success(t("pcAccount.totp.removed"));
      setOpen(null);
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy("");
    }
  };

  const identityAction = (kind: "EMAIL" | "PHONE") => setIdentity({ kind, action: bound[kind] ? "rebind" : "bind" });

  const suggest: Record<keyof SecurityFactors, () => void> = {
    totp: () => void startTotp(),
    phone: () => identityAction("PHONE"),
    email: () => identityAction("EMAIL"),
    antiPhishing: () => setOpen("antiPhishing"),
  };

  return (
    <AccountLayout title={t("pcAccount.security.title")} subtitle={t("pcAccount.security.subtitle")}>
      <Summary factors={factors} loading={summaryLoading} onSuggest={(k) => suggest[k]()} profile={profile} />

      <Section title={t("pcAccount.security.twoFactor")}>
        <div className="flex flex-col gap-3">
          <SecurityItem
            index={0}
            icon={<ShieldEllipsis size={20} />}
            title={t("pcAccount.security.totp.title")}
            desc={
              <>
                {t("pcAccount.security.totp.desc")}
                <TotpLimitsHint limits={limits.data} />
              </>
            }
            loading={totp.isPending}
            error={totp.error}
            onRetry={() => void totp.refetch()}
            status={
              totpOn ? (
                <Badge tone="success" dot>
                  {t("pcAccount.status.on")}
                </Badge>
              ) : totp.data?.pending ? (
                <Badge tone="warn" dot>
                  {t("pcAccount.status.pending")}
                </Badge>
              ) : (
                <Badge>{t("pcAccount.status.off")}</Badge>
              )
            }
            action={
              totpOn ? (
                <Button size="sm" variant="secondary" onClick={() => setOpen("totpUnbind")}>
                  {t("pcAccount.security.totp.unbind")}
                </Button>
              ) : (
                <Button size="sm" loading={busy === "totpBind"} onClick={() => void startTotp()}>
                  {totp.data?.pending ? t("pcAccount.security.totp.continueBind") : t("pcAccount.security.totp.bind")}
                </Button>
              )
            }
          />
          {(["EMAIL", "PHONE"] as const).map((kind, i) => (
            <SecurityItem
              key={kind}
              index={i + 1}
              icon={kind === "EMAIL" ? <Mail size={20} /> : <Smartphone size={20} />}
              title={t(`pcAccount.security.${kind === "EMAIL" ? "email" : "phone"}.title`)}
              desc={t(`pcAccount.security.${kind === "EMAIL" ? "email" : "phone"}.desc`)}
              // The step-up of a binding depends on the authenticator app too.
              loading={ids.isPending || totp.isPending}
              error={ids.error}
              onRetry={() => void ids.refetch()}
              status={
                bound[kind] ? (
                  <Badge tone="success" dot>
                    {t("pcAccount.status.bound")}
                  </Badge>
                ) : (
                  <Badge>{t("pcAccount.status.unbound")}</Badge>
                )
              }
              detail={bound[kind] ?? null}
              action={
                <Button size="sm" variant={bound[kind] ? "secondary" : "primary"} onClick={() => identityAction(kind)}>
                  {bound[kind] ? t("pcAccount.security.change") : t("pcAccount.security.bind")}
                </Button>
              }
            />
          ))}
          <p className="text-xs text-fg-3">{t("pcAccount.security.identitiesNote")}</p>
        </div>
      </Section>

      <Section title={t("pcAccount.security.advanced")}>
        <div className="flex flex-col gap-3">
          <SecurityItem
            index={3}
            icon={<KeyRound size={20} />}
            title={t("pcAccount.security.password.title")}
            desc={t("pcAccount.security.password.desc")}
            status={
              <Badge tone="success" dot>
                {t("pcAccount.status.set")}
              </Badge>
            }
            action={
              <Button size="sm" variant="secondary" onClick={() => setOpen("password")}>
                {t("pcAccount.security.password.action")}
              </Button>
            }
          />
          <SecurityItem
            index={4}
            icon={<Fish size={20} />}
            title={t("pcAccount.security.antiPhishing.title")}
            desc={t("pcAccount.security.antiPhishing.desc")}
            loading={profile.isPending}
            error={profile.error}
            onRetry={() => void profile.refetch()}
            status={
              profile.data?.anti_phishing_code ? (
                <Badge tone="success" dot>
                  {t("pcAccount.status.set")}
                </Badge>
              ) : (
                <Badge>{t("pcAccount.status.notSet")}</Badge>
              )
            }
            detail={profile.data?.anti_phishing_code ? <SecretText value={profile.data.anti_phishing_code} /> : null}
            action={
              <Button size="sm" variant={profile.data?.anti_phishing_code ? "secondary" : "primary"} onClick={() => setOpen("antiPhishing")}>
                {profile.data?.anti_phishing_code ? t("pcAccount.security.antiPhishing.change") : t("pcAccount.security.antiPhishing.set")}
              </Button>
            }
          />
          <SecurityItem
            index={5}
            icon={<MonitorSmartphone size={20} />}
            title={t("pcAccount.security.devices.title")}
            desc={t("pcAccount.security.devices.desc")}
            loading={sessions.isPending}
            error={sessions.error}
            onRetry={() => void sessions.refetch()}
            detail={sessions.data ? t("pcAccount.security.devices.count", { count: sessions.data.length }) : null}
            action={<LinkButton to={routes.sessions}>{t("pcAccount.security.devices.action")}</LinkButton>}
          />
          <SecurityItem
            index={6}
            icon={<History size={20} />}
            title={t("pcAccount.security.history.title")}
            desc={t("pcAccount.security.history.desc")}
            action={<LinkButton to={`${routes.sessions}#history`}>{t("pcAccount.security.history.action")}</LinkButton>}
          />
        </div>
      </Section>

      <ChangePasswordDialog open={open === "password"} onClose={() => setOpen(null)} ask={stepUp.ask} />
      <AntiPhishingDialog
        open={open === "antiPhishing"}
        current={profile.data?.anti_phishing_code ?? ""}
        onClose={() => setOpen(null)}
        ask={stepUp.ask}
      />
      <BindTotpDialog setup={setup} onClose={() => setSetup(null)} />
      <IdentityDialog task={identity} bound={bound} totp={totpOn} onClose={() => setIdentity(null)} />
      <Dialog
        open={open === "totpUnbind"}
        onOpenChange={(o) => !o && busy === "" && setOpen(null)}
        title={t("pcAccount.totp.unbindTitle")}
        description={t("pcAccount.totp.unbindWarn")}
        size="sm"
        confirmVariant="danger"
        confirmText={t("pcAccount.totp.unbindConfirm")}
        confirmLoading={busy === "totpUnbind"}
        onConfirm={() => void removeTotp()}
      />
      {stepUp.dialog}
    </AccountLayout>
  );
}

function Summary({
  factors, loading, onSuggest, profile,
}: {
  factors: SecurityFactors;
  loading: boolean;
  onSuggest: (k: keyof SecurityFactors) => void;
  profile: ReturnType<typeof useProfile>;
}) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const s = securitySummary(factors);
  const tone = levelTone[s.level];
  const p = profile.data;
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-6 overflow-hidden rounded-3 border border-line-1 bg-bg-1 p-6">
      {loading ? (
        <div className="flex items-center gap-5">
          <Skeleton round className="size-16" />
          <div className="flex flex-1 flex-col gap-3">
            <Skeleton className="h-5 w-48" />
            <Skeleton className="h-2 w-full max-w-md" />
            <Skeleton className="h-4 w-64" />
          </div>
        </div>
      ) : (
        <div className="flex min-w-0 items-center gap-5">
          <span className={cn("grid size-16 shrink-0 place-items-center rounded-full", tone.bg, tone.text)}>
            {s.level === "low" ? <ShieldAlert size={30} /> : <ShieldCheck size={30} />}
          </span>
          <div className="flex min-w-0 flex-1 flex-col gap-2">
            <div className="flex flex-wrap items-baseline gap-2">
              <span className="text-sm text-fg-3">{t("pcAccount.security.level")}</span>
              <span className={cn("text-lg font-semibold", tone.text)}>{t(`pcAccount.security.levels.${s.level}`)}</span>
              <span className="text-xs text-fg-3 tabular-nums">{t("pcAccount.security.score", { score: s.score, max: s.max })}</span>
            </div>
            <Progress value={s.score} max={s.max} tone={tone.progress} className="max-w-md" aria-label={t("pcAccount.security.level")} />
            <p className="text-sm text-fg-2">{t(`pcAccount.security.levelHint.${s.level}`)}</p>
            {s.missing.length > 0 && (
              <div className="flex flex-wrap gap-2 pt-1">
                {s.missing.map((k) => (
                  <button
                    key={k}
                    type="button"
                    onClick={() => onSuggest(k)}
                    className="inline-flex h-7 items-center gap-1 rounded-full border border-line-2 px-3 text-xs text-fg-2 transition-colors hover:border-brand hover:text-brand"
                  >
                    {t(`pcAccount.security.suggest.${k}`)} <ArrowRight size={12} />
                  </button>
                ))}
              </div>
            )}
          </div>
        </div>
      )}
      <dl className="grid min-w-56 content-center gap-x-6 gap-y-3 border-l border-line-1 pl-6 text-sm">
        <Info failed={profile.isError} label={t("pcAccount.uid")}>
          {p ? (
            <span className="inline-flex items-center gap-1 tabular-nums">
              {shortId(p.user_id)}
              <CopyButton value={p.user_id} size={12} />
            </span>
          ) : null}
        </Info>
        <Info failed={profile.isError} label={t("pcAccount.security.info.status")}>
          {p ? (
            <Badge tone={p.status === "ACTIVE" ? "success" : p.status === "CLOSED" ? "neutral" : "warn"} dot>
              {enumLabel(p.status, "accountStatus")}
            </Badge>
          ) : null}
        </Info>
        <Info failed={profile.isError} label={t("pcAccount.security.info.region")}>{p ? regionName(p.region, locale) : null}</Info>
        <Info failed={profile.isError} label={t("pcAccount.security.info.joined")}>{p ? <TimeText value={p.created_at} format="date" /> : null}</Info>
      </dl>
    </div>
  );
}

function Info({ label, failed, children }: { label: ReactNode; failed: boolean; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-6">
      <dt className="text-fg-3">{label}</dt>
      <dd className="text-right text-fg-1">{children ?? (failed ? "—" : <Skeleton className="h-4 w-20" />)}</dd>
    </div>
  );
}

/** SecretText shows a code masked, with an eye to reveal it. */
function SecretText({ value }: { value: string }) {
  const { t } = useTranslation();
  const [shown, setShown] = useState(false);
  return (
    <span className="inline-flex items-center gap-1 font-mono">
      {shown ? value : maskCode(value)}
      <IconButton
        size="xs"
        icon={shown ? <EyeOff /> : <Eye />}
        label={shown ? t("pcAccount.security.antiPhishing.conceal") : t("pcAccount.security.antiPhishing.reveal")}
        onClick={() => setShown((s) => !s)}
      />
    </span>
  );
}

function LinkButton({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Button asChild size="sm" variant="secondary">
      <Link to={to}>
        {children}
        <ArrowRight size={14} />
      </Link>
    </Button>
  );
}

/**
 * TotpLimitsHint says how an authenticator app raises the withdrawal limits: when it will, while one bound
 * within the settling time waits, or after how long one would. The numbers and the hours come from GET
 * /v1/wallet/limits, never from the copy.
 */
function TotpLimitsHint({ limits }: { limits?: WithdrawLimits }) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const zone = useSettings((s) => timeZoneOf(s));
  if (!limits) return null;
  const params = {
    hours: limits.totp_settling_hours,
    daily: formatDecimal(limits.full_daily_limit, { decimals: 0 }),
    monthly: formatDecimal(limits.full_monthly_limit, { decimals: 0 }),
  };
  if (limits.full_limits_at) {
    return (
      <span className="mt-1 block">
        {t("pcAccount.security.totp.limitsSoon", { ...params, time: formatTime(limits.full_limits_at, "datetime", locale, zone) })}
      </span>
    );
  }
  if (!limits.totp_enabled) return <span className="mt-1 block">{t("pcAccount.security.totp.limitsAfterBind", params)}</span>;
  return null;
}
