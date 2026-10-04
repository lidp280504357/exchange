import { enumLabel, errorText, formatDecimal, formatTime, routes, timeZoneOf, useSettings, useTotpStatus } from "@exchange/core";
import { regionName } from "@exchange/core/auth/register";
import { maskCode, useProfile } from "@exchange/core/user/profile";
import {
  disableTotp, refreshTotp, securitySummary, setupTotp, useBoundIdentities, type SecurityFactors, type SecurityLevel, type TotpSetup,
} from "@exchange/core/user/security";
import { useSessions } from "@exchange/core/user/sessions";
import { useWithdrawLimits } from "@exchange/core/wallet/hooks";
import type { WithdrawLimits } from "@exchange/core/wallet/networks";
import { Badge, ErrorState, Progress, Skeleton, TimeText, cn, listItem, toast } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import {
  ChevronRight, Fish, History, KeyRound, Mail, MonitorSmartphone, ShieldAlert, ShieldCheck, ShieldEllipsis, Smartphone,
} from "lucide-react";
import { motion } from "motion/react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { PullToRefresh } from "../../components/PullToRefresh";
import { useStepUp } from "../../features/auth/StepUp";
import { usePageHeader } from "../../layout/header";
import { AntiPhishingSheet } from "./parts/AntiPhishingSheet";
import { ChangePasswordSheet } from "./parts/ChangePasswordSheet";
import { ConfirmSheet } from "./parts/ConfirmSheet";
import { IdentitySheet, type IdentityTask } from "./parts/IdentitySheet";
import { totpState } from "./parts/logic";
import { Section } from "./parts/rows";
import { SecurityCard } from "./parts/SecurityCard";
import { TotpSheet } from "./parts/TotpSheet";

type Open = null | "password" | "totpUnbind" | "antiPhishing";

const levelTone: Record<SecurityLevel, { text: string; bg: string; progress: "danger" | "warn" | "success" }> = {
  low: { text: "text-danger", bg: "bg-danger/10", progress: "danger" },
  medium: { text: "text-warn", bg: "bg-warn/10", progress: "warn" },
  high: { text: "text-success", bg: "bg-success/10", progress: "success" },
};

/**
 * Security (design §7.2 我的 → 安全中心): the level of protection with
 * what is left to do, then the authenticator app, email, phone, password
 * and anti-phishing code as cards; a tap starts the change in a bottom
 * sheet, and sensitive changes ask for a step-up (useStepUp).
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
  usePageHeader({ title: t("mAccount.security.title"), back: routes.me }, [t]);

  const bound = ids.data ?? {};
  const tState = totpState(totp.data);
  const antiPhishing = profile.data?.anti_phishing_code ?? "";
  const factors: SecurityFactors = { totp: tState === "on", email: Boolean(bound.EMAIL), phone: Boolean(bound.PHONE), antiPhishing: Boolean(antiPhishing) };
  const summaryQueries = [totp, ids, profile];
  const summaryError = summaryQueries.find((q) => q.isError)?.error ?? null;

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
      toast.success(t("mAccount.totp.removed"));
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

  const refresh = async () => {
    const results = await Promise.all([totp.refetch(), ids.refetch(), profile.refetch(), sessions.refetch()]);
    const failed = results.find((r) => r.isError);
    if (failed) toast.error(errorText(failed.error));
  };

  const identityFailed = ids.error ?? totp.error;
  const retryIdentities = () => {
    if (ids.isError) void ids.refetch();
    if (totp.isError) void totp.refetch();
  };

  return (
    <>
      <PullToRefresh onRefresh={refresh}>
        <div className="flex flex-col gap-5 px-4 py-3">
          <Summary
            factors={factors}
            loading={summaryQueries.some((q) => q.isPending)}
            error={summaryError}
            onRetry={() => summaryQueries.filter((q) => q.isError).forEach((q) => void q.refetch())}
            onSuggest={(k) => suggest[k]()}
            profile={profile}
          />

          <Section title={t("mAccount.security.twoFactor")}>
            <SecurityCard
              index={1}
              icon={<ShieldEllipsis size={20} />}
              title={t("mAccount.security.totp.title")}
              desc={
              <>
                {t("mAccount.security.totp.desc")}
                <TotpLimitsHint limits={limits.data} />
              </>
            }
              loading={totp.isPending}
              error={totp.error}
              onRetry={() => void totp.refetch()}
              status={
                tState === "on" ? (
                  <Badge tone="success" dot>
                    {t("mAccount.status.on")}
                  </Badge>
                ) : tState === "pending" ? (
                  <Badge tone="warn" dot>
                    {t("mAccount.status.pending")}
                  </Badge>
                ) : (
                  <Badge>{t("mAccount.status.off")}</Badge>
                )
              }
              action={
                tState === "on"
                  ? t("mAccount.security.totp.unbind")
                  : tState === "pending"
                    ? t("mAccount.security.totp.continueBind")
                    : t("mAccount.security.totp.bind")
              }
              emphasis={tState !== "on"}
              busy={busy === "totpBind"}
              onClick={() => (tState === "on" ? setOpen("totpUnbind") : void startTotp())}
            />
            {(["EMAIL", "PHONE"] as const).map((kind, i) => {
              const key = kind === "EMAIL" ? "email" : "phone";
              return (
                <SecurityCard
                  key={kind}
                  index={i + 2}
                  icon={kind === "EMAIL" ? <Mail size={20} /> : <Smartphone size={20} />}
                  title={t(`mAccount.security.${key}.title`)}
                  desc={t(`mAccount.security.${key}.desc`)}
                  // The step-up of a binding depends on the authenticator app too.
                  loading={ids.isPending || totp.isPending}
                  error={identityFailed}
                  onRetry={retryIdentities}
                  status={
                    bound[kind] ? (
                      <Badge tone="success" dot>
                        {t("mAccount.status.bound")}
                      </Badge>
                    ) : (
                      <Badge>{t("mAccount.status.unbound")}</Badge>
                    )
                  }
                  detail={bound[kind] ?? null}
                  action={bound[kind] ? t("mAccount.security.change") : t("mAccount.security.bind")}
                  emphasis={!bound[kind]}
                  onClick={() => identityAction(kind)}
                />
              );
            })}
            <p className="px-1 text-xs leading-relaxed text-fg-3">{t("mAccount.security.identitiesNote")}</p>
          </Section>

          <Section title={t("mAccount.security.advanced")}>
            <SecurityCard
              index={4}
              icon={<KeyRound size={20} />}
              title={t("mAccount.security.password.title")}
              desc={t("mAccount.security.password.desc")}
              status={
                <Badge tone="success" dot>
                  {t("mAccount.status.set")}
                </Badge>
              }
              action={t("mAccount.security.password.action")}
              onClick={() => setOpen("password")}
            />
            <SecurityCard
              index={5}
              icon={<Fish size={20} />}
              title={t("mAccount.security.antiPhishing.title")}
              desc={t("mAccount.security.antiPhishing.desc")}
              loading={profile.isPending}
              error={profile.error}
              onRetry={() => void profile.refetch()}
              status={
                antiPhishing ? (
                  <Badge tone="success" dot>
                    {t("mAccount.status.set")}
                  </Badge>
                ) : (
                  <Badge>{t("mAccount.status.notSet")}</Badge>
                )
              }
              detail={antiPhishing ? <span className="font-mono">{maskCode(antiPhishing)}</span> : null}
              action={antiPhishing ? t("mAccount.security.antiPhishing.change") : t("mAccount.security.antiPhishing.set")}
              emphasis={!antiPhishing}
              onClick={() => setOpen("antiPhishing")}
            />
            <SecurityCard
              index={6}
              icon={<MonitorSmartphone size={20} />}
              title={t("mAccount.security.devices.title")}
              desc={t("mAccount.security.devices.desc")}
              loading={sessions.isPending}
              error={sessions.error}
              onRetry={() => void sessions.refetch()}
              detail={sessions.data ? t("mAccount.security.devices.count", { count: sessions.data.length }) : null}
              action={t("mAccount.security.devices.action")}
              to={routes.sessions}
            />
            <SecurityCard
              index={7}
              icon={<History size={20} />}
              title={t("mAccount.security.history.title")}
              desc={t("mAccount.security.history.desc")}
              action={t("mAccount.security.history.action")}
              to={`${routes.sessions}#history`}
            />
          </Section>
        </div>
      </PullToRefresh>

      {/* Sheets sit outside the pull-to-refresh area: their touches must not pull the page. */}
      <ChangePasswordSheet open={open === "password"} onClose={() => setOpen(null)} ask={stepUp.ask} />
      <AntiPhishingSheet open={open === "antiPhishing"} current={antiPhishing} onClose={() => setOpen(null)} ask={stepUp.ask} />
      <TotpSheet setup={setup} onClose={() => setSetup(null)} />
      <IdentitySheet task={identity} bound={bound} totp={tState === "on"} onClose={() => setIdentity(null)} />
      <ConfirmSheet
        open={open === "totpUnbind"}
        onOpenChange={(o) => !o && setOpen(null)}
        title={t("mAccount.totp.unbindTitle")}
        description={t("mAccount.totp.unbindWarn")}
        tone="danger"
        confirmText={t("mAccount.totp.unbindConfirm")}
        loading={busy === "totpUnbind"}
        onConfirm={() => void removeTotp()}
      />
      {stepUp.sheet}
    </>
  );
}

function Summary({
  factors, loading, error, onRetry, onSuggest, profile,
}: {
  factors: SecurityFactors;
  loading: boolean;
  error: unknown;
  onRetry: () => void;
  onSuggest: (k: keyof SecurityFactors) => void;
  profile: ReturnType<typeof useProfile>;
}) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  if (error != null) {
    return (
      <div className="rounded-3 bg-bg-1">
        <ErrorState compact message={errorText(error)} onRetry={onRetry} />
      </div>
    );
  }
  if (loading || !profile.data) {
    return (
      <div aria-busy className="flex flex-col gap-4 rounded-3 bg-bg-1 p-4">
        <div className="flex items-center gap-4">
          <Skeleton round className="size-14" />
          <div className="flex flex-1 flex-col gap-2">
            <Skeleton className="h-5 w-32" />
            <Skeleton className="h-1.5 w-full" />
          </div>
        </div>
        <Skeleton className="h-4 w-3/4" />
        <Skeleton className="h-10 w-full" />
      </div>
    );
  }
  const s = securitySummary(factors);
  const tone = levelTone[s.level];
  const p = profile.data;
  return (
    <motion.section variants={listItem} initial="initial" animate="animate" custom={0} className="flex flex-col gap-4 rounded-3 bg-bg-1 p-4">
      <div className="flex items-center gap-4">
        <span className={cn("grid size-14 shrink-0 place-items-center rounded-full", tone.bg, tone.text)}>
          {s.level === "low" ? <ShieldAlert size={28} /> : <ShieldCheck size={28} />}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-2">
            <span className="text-sm text-fg-3">{t("mAccount.security.level")}</span>
            <span className={cn("text-lg font-semibold", tone.text)}>{t(`mAccount.security.levels.${s.level}`)}</span>
            <span className="text-xs text-fg-3 tabular-nums">{t("mAccount.security.score", { score: s.score, max: s.max })}</span>
          </div>
          <Progress value={s.score} max={s.max} tone={tone.progress} className="mt-2" aria-label={t("mAccount.security.level")} />
        </div>
      </div>
      <p className="text-sm leading-relaxed text-fg-2">{t(`mAccount.security.levelHint.${s.level}`)}</p>
      {s.missing.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {s.missing.map((k) => (
            <button
              key={k}
              type="button"
              onClick={() => onSuggest(k)}
              className="inline-flex h-tap items-center gap-1 rounded-full border border-line-2 px-4 text-sm text-fg-2 transition-colors active:border-brand active:text-brand"
            >
              {t(`mAccount.security.suggest.${k}`)}
              <ChevronRight size={14} aria-hidden />
            </button>
          ))}
        </div>
      )}
      <dl className="grid grid-cols-3 gap-2 border-t border-line-1 pt-3 text-xs">
        <Info label={t("mAccount.security.info.status")}>{enumLabel(p.status, "accountStatus")}</Info>
        <Info label={t("mAccount.security.info.region")}>{regionName(p.region, locale)}</Info>
        <Info label={t("mAccount.security.info.joined")}>
          <TimeText value={p.created_at} format="date" />
        </Info>
      </dl>
    </motion.section>
  );
}

function Info({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <dt className="truncate text-fg-3">{label}</dt>
      <dd className="truncate text-sm text-fg-1">{children}</dd>
    </div>
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
        {t("mAccount.security.totp.limitsSoon", { ...params, time: formatTime(limits.full_limits_at, "datetime", locale, zone) })}
      </span>
    );
  }
  if (!limits.totp_enabled) return <span className="mt-1 block">{t("mAccount.security.totp.limitsAfterBind", params)}</span>;
  return null;
}
