import { enumLabel, errorText, routes, signOut } from "@exchange/core";
import {
  describeDevice, deviceName, flattenHistory, revokeOtherSessions, revokeSession, sessionsKey, useLoginHistory, useSessions,
  type DeviceSession, type HistoryRow,
} from "@exchange/core/user/sessions";
import {
  Badge, Button, DataTable, Dialog, EmptyState, ErrorState, listItem, Skeleton, SkeletonLines, TimeText, cn, toast, type BadgeTone, type ColumnDef,
} from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { LogOut, Monitor, Smartphone, Tablet } from "lucide-react";
import { motion } from "motion/react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation, useNavigate } from "react-router";
import { useStepUp } from "../../features/auth/StepUp";
import { AccountLayout, Section } from "./parts/AccountLayout";

const resultTone: Record<string, BadgeTone> = { SUCCESS: "success", FAILED_PASSWORD: "danger", LOCKED: "danger", CHALLENGE_REQUIRED: "warn" };

/**
 * Sessions (design §6.2 账户): the devices signed in, this one marked;
 * signing out one device or every other one (after a step-up); and the
 * sign-in history, paged as it scrolls.
 */
export default function Sessions() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const stepUp = useStepUp();
  const sessions = useSessions();
  const [confirm, setConfirm] = useState<DeviceSession | "others" | null>(null);
  const [busy, setBusy] = useState(false);
  const others = (sessions.data ?? []).filter((s) => !s.current).length;

  useEffect(() => {
    if (location.hash === "#history") document.getElementById("history")?.scrollIntoView({ block: "start" });
  }, [location.hash]);

  const revoke = async () => {
    if (!confirm) return;
    setBusy(true);
    try {
      const token = await stepUp.ask();
      if (!token) return;
      if (confirm === "others") await revokeOtherSessions(token);
      else await revokeSession(confirm.session_id, token);
      toast.success(confirm === "others" ? t("pcAccount.sessions.revokedOthers") : t("pcAccount.sessions.revoked"));
      setConfirm(null);
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(false);
      void qc.invalidateQueries({ queryKey: sessionsKey });
    }
  };

  const signOutHere = async () => {
    await signOut();
    navigate(routes.login, { replace: true });
  };

  const device = confirm && confirm !== "others" ? deviceName(describeDevice(confirm.user_agent, confirm.client_type)) || t("pcAccount.sessions.unknownDevice") : "";

  return (
    <AccountLayout
      title={t("pcAccount.sessions.title")}
      subtitle={t("pcAccount.sessions.subtitle")}
      actions={
        <Button variant="secondary" icon={<LogOut size={16} />} disabled={others === 0} onClick={() => setConfirm("others")}>
          {t("pcAccount.sessions.revokeOthers")}
        </Button>
      }
    >
      <Section
        title={t("pcAccount.sessions.devices")}
        extra={sessions.data && <span className="text-sm text-fg-3">{t("pcAccount.sessions.online", { count: sessions.data.length })}</span>}
      >
        {sessions.isPending ? (
          <div className="grid grid-cols-2 gap-4 2xl:grid-cols-3">
            {[0, 1].map((i) => (
              <div key={i} className="flex flex-col gap-4 rounded-3 border border-line-1 bg-bg-1 p-4">
                <div className="flex items-center gap-3">
                  <Skeleton className="size-11 rounded-3" />
                  <SkeletonLines lines={2} className="flex-1" />
                </div>
                <Skeleton className="h-8 w-full" />
              </div>
            ))}
          </div>
        ) : sessions.isError ? (
          <div className="rounded-3 border border-line-1 bg-bg-1">
            <ErrorState message={errorText(sessions.error)} onRetry={() => void sessions.refetch()} />
          </div>
        ) : sessions.data.length === 0 ? (
          <div className="rounded-3 border border-line-1 bg-bg-1">
            <EmptyState
              title={t("state.emptyTitle")}
              action={
                <Button asChild size="sm" variant="secondary">
                  <Link to={routes.security}>{t("nav.security")}</Link>
                </Button>
              }
            />
          </div>
        ) : (
          <>
            <div className="grid grid-cols-2 gap-4 2xl:grid-cols-3">
              {sessions.data.map((s, i) => (
                <DeviceCard key={s.session_id} session={s} index={i} onRevoke={() => setConfirm(s)} onSignOut={() => void signOutHere()} />
              ))}
            </div>
            {others === 0 && <p className="text-xs text-fg-3">{t("pcAccount.sessions.onlyThis")}</p>}
          </>
        )}
      </Section>

      <Section id="history" title={t("pcAccount.sessions.history")} extra={<span className="text-xs text-fg-3">{t("pcAccount.sessions.historyHint")}</span>}>
        <HistoryTable />
      </Section>

      <Dialog
        open={confirm !== null}
        onOpenChange={(o) => !o && !busy && setConfirm(null)}
        title={confirm === "others" ? t("pcAccount.sessions.revokeOthersTitle") : t("pcAccount.sessions.revokeTitle")}
        description={confirm === "others" ? t("pcAccount.sessions.revokeOthersDesc") : t("pcAccount.sessions.revokeDesc", { device })}
        size="sm"
        confirmVariant="danger"
        confirmText={confirm === "others" ? t("pcAccount.sessions.revokeOthers") : t("pcAccount.sessions.revoke")}
        confirmLoading={busy}
        onConfirm={() => void revoke()}
      />
      {stepUp.dialog}
    </AccountLayout>
  );
}

function DeviceCard({ session: s, index, onRevoke, onSignOut }: { session: DeviceSession; index: number; onRevoke: () => void; onSignOut: () => void }) {
  const { t } = useTranslation();
  const info = describeDevice(s.user_agent, s.client_type);
  const Icon = info.kind === "mobile" ? Smartphone : info.kind === "tablet" ? Tablet : Monitor;
  return (
    <motion.div
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={index}
      className={cn(
        "flex flex-col gap-4 rounded-3 border bg-bg-1 p-4 transition-[translate,border-color] duration-[var(--t-base)] hover:-translate-y-0.5",
        s.current ? "border-brand/50" : "border-line-1 hover:border-line-2",
      )}
    >
      <div className="flex items-start gap-3">
        <span className={cn("grid size-11 shrink-0 place-items-center rounded-3", s.current ? "bg-brand-soft text-brand" : "bg-bg-2 text-fg-2")}>
          <Icon size={20} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate font-medium text-fg-1" title={s.user_agent}>
              {deviceName(info) || t("pcAccount.sessions.unknownDevice")}
            </span>
            {s.current && (
              <Badge tone="brand" dot>
                {t("pcAccount.sessions.current")}
              </Badge>
            )}
          </div>
          <div className="mt-0.5 truncate text-xs text-fg-3">
            {enumLabel(s.client_type)} · {t("pcAccount.sessions.ip")} {s.ip || "—"}
          </div>
        </div>
      </div>
      <dl className="grid grid-cols-2 gap-3 rounded-2 bg-bg-2 px-3 py-2 text-xs">
        <div className="min-w-0">
          <dt className="text-fg-3">{t("pcAccount.sessions.lastActive")}</dt>
          <dd className="mt-0.5 text-fg-1">
            <TimeText value={s.last_seen_at} relative />
          </dd>
        </div>
        <div className="min-w-0">
          <dt className="text-fg-3">{t("pcAccount.sessions.firstLogin")}</dt>
          <dd className="mt-0.5 truncate text-fg-1">
            <TimeText value={s.created_at} format="datetime" />
          </dd>
        </div>
      </dl>
      <div className="flex justify-end">
        {s.current ? (
          <Button size="sm" variant="ghost" icon={<LogOut size={14} />} onClick={onSignOut}>
            {t("pcAccount.sessions.signOutHere")}
          </Button>
        ) : (
          <Button size="sm" variant="secondary" onClick={onRevoke}>
            {t("pcAccount.sessions.revoke")}
          </Button>
        )}
      </div>
    </motion.div>
  );
}

function HistoryTable() {
  const { t } = useTranslation();
  const q = useLoginHistory(20);
  const rows = useMemo(() => flattenHistory(q.data?.pages ?? []), [q.data]);
  const columns = useMemo<ColumnDef<HistoryRow>[]>(
    () => [
      {
        id: "time",
        header: t("pcAccount.sessions.col.time"),
        cell: ({ row }) => <TimeText value={row.original.created_at} format="datetimeSeconds" />,
        meta: { width: 150 },
      },
      {
        id: "account",
        header: t("pcAccount.sessions.col.account"),
        cell: ({ row }) => (
          <span className="inline-flex max-w-full items-center gap-1.5">
            <span className="truncate">{row.original.identity || "—"}</span>
            <span className="shrink-0 text-fg-3">· {enumLabel(row.original.method)}</span>
          </span>
        ),
      },
      {
        id: "device",
        header: t("pcAccount.sessions.col.device"),
        cell: ({ row }) => {
          const e = row.original;
          return (
            <span className="inline-flex max-w-full items-center gap-2" title={e.user_agent}>
              <span className="truncate">{deviceName(describeDevice(e.user_agent)) || t("pcAccount.sessions.unknownDevice")}</span>
              {e.new_device && <Badge tone="warn">{t("pcAccount.sessions.newDevice")}</Badge>}
            </span>
          );
        },
      },
      { id: "ip", header: t("pcAccount.sessions.col.ip"), cell: ({ row }) => row.original.ip || "—", meta: { width: 112 } },
      {
        id: "result",
        header: t("pcAccount.sessions.col.result"),
        cell: ({ row }) => (
          <Badge tone={resultTone[row.original.result] ?? "neutral"} dot>
            {enumLabel(row.original.result)}
          </Badge>
        ),
        meta: { align: "right", width: 104 },
      },
    ],
    [t],
  );
  return (
    <div className="overflow-hidden rounded-3 border border-line-1 bg-bg-1">
      <DataTable
        columns={columns}
        data={rows}
        getRowId={(r) => r.key}
        loading={q.isPending}
        loadingRows={6}
        error={q.error}
        onRetry={() => void q.refetch()}
        empty={<EmptyState compact title={t("pcAccount.sessions.historyEmpty")} />}
        onEndReached={() => void q.fetchNextPage()}
        hasMore={q.hasNextPage}
        loadingMore={q.isFetchingNextPage}
        density="compact"
        stickyHeader={false}
        aria-label={t("pcAccount.sessions.history")}
      />
    </div>
  );
}
