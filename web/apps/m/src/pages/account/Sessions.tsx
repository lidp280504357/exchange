import { enumLabel, errorText, routes, signOut } from "@exchange/core";
import {
  describeDevice, deviceName, flattenHistory, revokeOtherSessions, revokeSession, sessionsKey, useLoginHistory, useSessions,
  type DeviceSession, type HistoryRow,
} from "@exchange/core/user/sessions";
import { Badge, Button, EmptyState, ErrorState, Skeleton, SkeletonLines, TimeText, cn, listItem, toast, type BadgeTone } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { LogOut, Monitor, Smartphone, Tablet } from "lucide-react";
import { motion } from "motion/react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation, useNavigate } from "react-router";
import { PullToRefresh } from "../../components/PullToRefresh";
import { useStepUp } from "../../features/auth/StepUp";
import { usePageHeader } from "../../layout/header";
import { ConfirmSheet } from "./parts/ConfirmSheet";
import { LoadMore } from "./parts/LoadMore";
import { entrance } from "./parts/logic";
import { Group, NavRow, Section } from "./parts/rows";
import { useLast } from "./parts/useLast";

const resultTone: Record<string, BadgeTone> = { SUCCESS: "success", FAILED_PASSWORD: "danger", LOCKED: "danger", CHALLENGE_REQUIRED: "warn" };

/** What the confirmation sheet is about: another device, every other one, or this one. */
type Confirm = DeviceSession | "others" | "here";

/**
 * Sessions (design §7.2 我的 → 设备): the devices signed in as cards, this
 * one marked; signing out one device or every other one (a confirmation
 * sheet, then a step-up), or this one; and the sign-in history as a list
 * that loads more as it scrolls. Pull down to refresh both.
 */
export default function Sessions() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const stepUp = useStepUp();
  const sessions = useSessions();
  const history = useLoginHistory(20);
  const [confirm, setConfirm] = useState<Confirm | null>(null);
  const [busy, setBusy] = useState(false);
  // The sheet keeps its text while it slides out.
  const shown = useLast(confirm);
  const others = (sessions.data ?? []).filter((s) => !s.current).length;
  usePageHeader({ title: t("mAccount.sessions.title"), back: routes.me }, [t]);
  // A link starts at the top; #history (from the security centre) scrolls to the sign-ins instead.
  useEffect(() => {
    if (location.hash === "#history") document.getElementById("history")?.scrollIntoView({ block: "start" });
  }, [location.hash]);

  const act = async () => {
    if (!confirm) return;
    setBusy(true);
    try {
      if (confirm === "here") {
        await signOut();
        navigate(routes.login, { replace: true });
        return;
      }
      const token = await stepUp.ask();
      if (!token) return;
      if (confirm === "others") await revokeOtherSessions(token);
      else await revokeSession(confirm.session_id, token);
      toast.success(confirm === "others" ? t("mAccount.sessions.revokedOthers") : t("mAccount.sessions.revoked"));
      setConfirm(null);
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(false);
      void qc.invalidateQueries({ queryKey: sessionsKey });
    }
  };

  const refresh = async () => {
    const results = await Promise.all([sessions.refetch(), history.refetch()]);
    const failed = results.find((r) => r.isError);
    if (failed) toast.error(errorText(failed.error));
  };

  const device = shown && typeof shown === "object" ? deviceName(describeDevice(shown.user_agent, shown.client_type)) || t("mAccount.sessions.unknownDevice") : "";
  const copy =
    shown === "others"
      ? { title: t("mAccount.sessions.revokeOthersTitle"), desc: t("mAccount.sessions.revokeOthersDesc"), action: t("mAccount.sessions.revokeOthers") }
      : shown === "here"
        ? { title: t("mAccount.sessions.signOutHereTitle"), desc: t("mAccount.sessions.signOutHereDesc"), action: t("mAccount.sessions.signOutHere") }
        : { title: t("mAccount.sessions.revokeTitle"), desc: t("mAccount.sessions.revokeDesc", { device }), action: t("mAccount.sessions.revoke") };

  return (
    <>
      <PullToRefresh onRefresh={refresh}>
        <div className="flex flex-col gap-5 px-4 py-3">
          <Section
            title={t("mAccount.sessions.devices")}
            extra={sessions.data && <span className="text-xs text-fg-3">{t("mAccount.sessions.online", { count: sessions.data.length })}</span>}
          >
            {sessions.isPending ? (
              [0, 1].map((i) => (
                <div key={i} aria-busy className="flex items-start gap-3 rounded-3 bg-bg-1 p-4">
                  <Skeleton className="size-10 rounded-3" />
                  <div className="flex flex-1 flex-col gap-3">
                    <SkeletonLines lines={2} />
                    <Skeleton className="h-8 w-full" />
                  </div>
                </div>
              ))
            ) : sessions.isError ? (
              <div className="rounded-3 bg-bg-1">
                <ErrorState compact message={errorText(sessions.error)} onRetry={() => void sessions.refetch()} />
              </div>
            ) : sessions.data.length === 0 ? (
              <div className="rounded-3 bg-bg-1">
                <EmptyState
                  compact
                  title={t("state.emptyTitle")}
                  action={
                    <Button asChild size="lg" variant="secondary">
                      <Link to={routes.security}>{t("mAccount.security.title")}</Link>
                    </Button>
                  }
                />
              </div>
            ) : (
              <>
                {sessions.data.map((s, i) => (
                  <DeviceCard key={s.session_id} session={s} index={i} onAction={() => setConfirm(s.current ? "here" : s)} />
                ))}
                {others === 0 ? (
                  <p className="px-1 text-xs text-fg-3">{t("mAccount.sessions.onlyThis")}</p>
                ) : (
                  <Group index={sessions.data.length}>
                    <NavRow icon={<LogOut size={18} />} label={t("mAccount.sessions.revokeOthers")} tone="danger" chevron={false} onClick={() => setConfirm("others")} />
                  </Group>
                )}
              </>
            )}
          </Section>

          <Section id="history" title={t("mAccount.sessions.history")} extra={<span className="text-xs text-fg-3">{t("mAccount.sessions.historyHint")}</span>}>
            <History q={history} />
          </Section>
        </div>
      </PullToRefresh>

      <ConfirmSheet
        open={confirm !== null}
        onOpenChange={(o) => !o && setConfirm(null)}
        title={copy.title}
        description={copy.desc}
        tone="danger"
        confirmText={copy.action}
        loading={busy}
        onConfirm={() => void act()}
      />
      {stepUp.sheet}
    </>
  );
}

function DeviceCard({ session: s, index, onAction }: { session: DeviceSession; index: number; onAction: () => void }) {
  const { t } = useTranslation();
  const info = describeDevice(s.user_agent, s.client_type);
  const Icon = info.kind === "mobile" ? Smartphone : info.kind === "tablet" ? Tablet : Monitor;
  return (
    <motion.div
      variants={listItem}
      initial={entrance(index)}
      animate="animate"
      custom={index}
      className={cn("overflow-hidden rounded-3 border bg-bg-1", s.current ? "border-brand/50" : "border-line-1")}
    >
      <div className="flex items-start gap-3 p-4">
        <span aria-hidden className={cn("grid size-10 shrink-0 place-items-center rounded-3", s.current ? "bg-brand-soft text-brand" : "bg-bg-2 text-fg-2")}>
          <Icon size={20} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="min-w-0 truncate font-medium text-fg-1">{deviceName(info) || t("mAccount.sessions.unknownDevice")}</span>
            {s.current && (
              <Badge tone="brand" dot>
                {t("mAccount.sessions.current")}
              </Badge>
            )}
          </div>
          <div className="mt-0.5 truncate text-xs text-fg-3">
            {enumLabel(s.client_type)} · {t("mAccount.sessions.ip")} {s.ip || "—"}
          </div>
          <dl className="mt-3 grid grid-cols-2 gap-3 rounded-2 bg-bg-2 px-3 py-2 text-xs">
            <div className="min-w-0">
              <dt className="text-fg-3">{t("mAccount.sessions.lastActive")}</dt>
              <dd className="mt-0.5 truncate text-fg-1">
                <TimeText value={s.last_seen_at} relative />
              </dd>
            </div>
            <div className="min-w-0">
              <dt className="text-fg-3">{t("mAccount.sessions.firstLogin")}</dt>
              <dd className="mt-0.5 truncate text-fg-1">
                <TimeText value={s.created_at} format="datetime" />
              </dd>
            </div>
          </dl>
        </div>
      </div>
      <button
        type="button"
        onClick={onAction}
        className="flex h-11 w-full items-center justify-center gap-1.5 border-t border-line-1 text-sm font-medium text-danger transition-colors active:bg-bg-2"
      >
        <LogOut size={14} aria-hidden />
        {s.current ? t("mAccount.sessions.signOutHere") : t("mAccount.sessions.revoke")}
      </button>
    </motion.div>
  );
}

function History({ q }: { q: ReturnType<typeof useLoginHistory> }) {
  const { t } = useTranslation();
  const rows = useMemo(() => flattenHistory(q.data?.pages ?? []), [q.data]);
  if (q.isPending) {
    return (
      <ul aria-busy className="flex flex-col divide-y divide-line-1 rounded-3 bg-bg-1">
        {[0, 1, 2, 3].map((i) => (
          <li key={i} className="flex flex-col gap-2 px-4 py-3">
            <div className="flex justify-between gap-3">
              <Skeleton className="h-4 w-36" />
              <Skeleton className="h-5 w-12 rounded-full" />
            </div>
            <Skeleton className="h-3.5 w-48" />
            <Skeleton className="h-3.5 w-full" />
          </li>
        ))}
      </ul>
    );
  }
  if (q.isError) {
    return (
      <div className="rounded-3 bg-bg-1">
        <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} />
      </div>
    );
  }
  if (rows.length === 0) {
    return (
      <div className="rounded-3 bg-bg-1">
        <EmptyState
          compact
          title={t("mAccount.sessions.historyEmpty")}
          description={t("mAccount.sessions.historyEmptyHint")}
          action={
            <Button asChild size="lg" variant="secondary">
              <Link to={routes.security}>{t("mAccount.security.title")}</Link>
            </Button>
          }
        />
      </div>
    );
  }
  return (
    <>
      <ul aria-label={t("mAccount.sessions.history")} className="flex flex-col divide-y divide-line-1 overflow-hidden rounded-3 bg-bg-1">
        {rows.map((e, i) => (
          <HistoryItem key={e.key} event={e} index={i} />
        ))}
      </ul>
      <LoadMore
        hasMore={Boolean(q.hasNextPage)}
        loading={q.isFetchingNextPage}
        failed={q.isFetchNextPageError}
        onMore={() => void q.fetchNextPage()}
        count={rows.length}
      />
    </>
  );
}

function HistoryItem({ event: e, index }: { event: HistoryRow; index: number }) {
  const { t } = useTranslation();
  return (
    <motion.li variants={listItem} initial={entrance(index)} animate="animate" custom={index} className="flex flex-col gap-1.5 px-4 py-3">
      <div className="flex items-center justify-between gap-3">
        <TimeText value={e.created_at} format="datetimeSeconds" className="text-sm text-fg-1" />
        <Badge tone={resultTone[e.result] ?? "neutral"} dot>
          {enumLabel(e.result)}
        </Badge>
      </div>
      <div className="flex min-w-0 items-center gap-1.5 text-xs text-fg-2">
        <span className="min-w-0 truncate">{e.identity || "—"}</span>
        <span className="shrink-0 text-fg-3">· {enumLabel(e.method)}</span>
      </div>
      <div className="flex min-w-0 items-center gap-2 text-xs text-fg-3">
        <span className="min-w-0 truncate">{deviceName(describeDevice(e.user_agent)) || t("mAccount.sessions.unknownDevice")}</span>
        {e.new_device && <Badge tone="warn">{t("mAccount.sessions.newDevice")}</Badge>}
        <span className="ml-auto shrink-0 tabular-nums">
          {t("mAccount.sessions.ip")} {e.ip || "—"}
        </span>
      </div>
    </motion.li>
  );
}
