import { errorText } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, CopyButton, DataTable, Dialog, ErrorState, Skeleton, type ColumnDef } from "@exchange/ui";
import { KeyRound, LogOut, ShieldCheck } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, lastFour } from "../../kit/actions";
import { EnumBadge, EnumText } from "../../kit/enums";
import { Fields } from "../../kit/fields";
import { IdText, TimeText } from "../../kit/format";
import { ListTable, pageSize, useCursorList } from "../../kit/lists";
import { Card } from "../../kit/Page";
import { useSecurity } from "./data";

type Security = AdminSchemas["Security"];
type Session = Security["sessions"][number];
type Device = Security["devices"][number];
type Login = AdminSchemas["LoginEntry"];

/**
 * SecurityTab is an account's sign-in security (design 2026-10-02 §4.1):
 * the authenticator and the password with what may be done to them, the
 * live sessions and devices, and the sign-in history.
 */
export function SecurityTab({ admin, userId }: { admin: Admin; userId: string }) {
  const { t } = useTranslation();
  const sec = useSecurity(userId);
  if (sec.isError) return <ErrorState message={errorText(sec.error)} onRetry={() => void sec.refetch()} />;
  const s = sec.data;
  const act = can(admin, "users.security");
  return (
    <div className="flex flex-col gap-5">
      {s && s.pending_identity_requests > 0 && (
        <p className="rounded-2 border border-warn/40 bg-warn/10 px-3 py-2 text-sm">
          {t("admin.user.sec.pendingRequests", { n: s.pending_identity_requests })}
        </p>
      )}
      <div className="grid gap-4 lg:grid-cols-2">
        <Card title={t("admin.user.sec.totp")} extra={<ShieldCheck size={16} className="text-fg-3" />}>
          {s ? (
            <div className="flex flex-col gap-3">
              <Fields
                label={t("admin.user.sec.totp")}
                items={[
                  { label: t("admin.common.status"), value: <EnumBadge group="totpStatus" code={s.totp.status} /> },
                  { label: t("admin.user.sec.totpSince"), value: <TimeText value={s.totp.activated_at} /> },
                  {
                    label: t("admin.user.sec.totpRemoved"),
                    value: s.totp.changed_at ? (
                      <span className="flex flex-col">
                        <TimeText value={s.totp.changed_at} />
                        {Date.now() - Date.parse(s.totp.changed_at) < 24 * 3600_000 && (
                          <span className="text-xs text-warn-strong" data-testid="totp-review-window">{t("admin.user.sec.totpReviewWindow")}</span>
                        )}
                      </span>
                    ) : (
                      t("admin.user.sec.never")
                    ),
                  },
                ]}
              />
              {act && s.totp.status !== "NONE" && (
                <div className="flex justify-end">
                  <ResetTotp userId={userId} />
                </div>
              )}
            </div>
          ) : (
            <Skeleton className="h-20 w-full" />
          )}
        </Card>
        <Card title={t("admin.user.sec.password")} extra={<KeyRound size={16} className="text-fg-3" />}>
          {s ? (
            <div className="flex flex-col gap-3">
              <Fields
                label={t("admin.user.sec.password")}
                items={[
                  { label: t("admin.user.sec.passwordChanged"), value: s.password_changed_at ? <TimeText value={s.password_changed_at} /> : t("admin.user.sec.never") },
                  { label: t("admin.user.lastLogin"), value: <TimeText value={s.last_login_at} /> },
                  {
                    label: t("admin.user.sec.lock"),
                    value: s.locked_seconds > 0 ? <Badge tone="danger">{t("admin.user.sec.locked", { n: s.locked_seconds })}</Badge> : t("admin.user.sec.notLocked"),
                  },
                ]}
              />
              {act && (
                <div className="flex justify-end">
                  <TemporaryPassword userId={userId} />
                </div>
              )}
            </div>
          ) : (
            <Skeleton className="h-24 w-full" />
          )}
        </Card>
      </div>
      <Sessions admin={admin} userId={userId} sessions={s?.sessions} loading={sec.isPending} />
      <Devices devices={s?.devices} loading={sec.isPending} />
      <Logins userId={userId} />
    </div>
  );
}

const securityKeys = (userId: string) => [["admin", "user", userId, "security"], ["admin", "user", userId, "logins"], ["admin", "audit"]];

function ResetTotp({ userId }: { userId: string }) {
  const { t } = useTranslation();
  return (
    <DangerAction
      trigger={(open) => (
        <Button size="sm" variant="danger" onClick={open}>
          {t("admin.user.sec.resetTotp")}
        </Button>
      )}
      title={t("admin.user.sec.resetTotpTitle")}
      description={t("admin.user.sec.resetTotpDesc")}
      target={<span className="font-mono text-xs">{userId}</span>}
      confirmWord={lastFour(userId)}
      run={async (reason) =>
        adminData(await adminApi.POST("/admin/v1/users/{id}/totp-reset", { params: { path: { id: userId } }, body: { reason } }))
      }
      success={t("admin.user.sec.totpReset")}
      invalidate={securityKeys(userId)}
    />
  );
}

type Temporary = { temporary_password: string; sessions_revoked: number };

/** TemporaryPassword sets a temporary password and shows it once. */
function TemporaryPassword({ userId }: { userId: string }) {
  const { t } = useTranslation();
  const [shown, setShown] = useState<Temporary | null>(null);
  return (
    <>
      <DangerAction
        trigger={(open) => (
          <Button size="sm" variant="danger" onClick={open}>
            {t("admin.user.sec.temporary")}
          </Button>
        )}
        title={t("admin.user.sec.temporaryTitle")}
        description={t("admin.user.sec.temporaryDesc")}
        target={<span className="font-mono text-xs">{userId}</span>}
        confirmWord={lastFour(userId)}
        run={async (reason) =>
          adminData(await adminApi.POST("/admin/v1/users/{id}/password-reset", { params: { path: { id: userId } }, body: { reason } }))
        }
        success={t("admin.user.sec.temporarySet")}
        invalidate={securityKeys(userId)}
        onDone={(r) => setShown(r as Temporary)}
      />
      <Dialog
        open={shown !== null}
        onOpenChange={(o) => !o && setShown(null)}
        title={t("admin.user.sec.temporaryShown")}
        description={t("admin.user.sec.temporaryOnce")}
        persistent
        footer={
          <Button variant="primary" onClick={() => setShown(null)}>
            {t("admin.user.sec.temporaryDone")}
          </Button>
        }
      >
        {shown && (
          <div className="flex flex-col gap-3">
            <div className="flex items-center justify-between gap-3 rounded-2 border border-line-1 bg-bg-2 px-4 py-3">
              <span className="select-all font-mono text-lg tracking-wider" data-testid="temporary-password">
                {shown.temporary_password}
              </span>
              <CopyButton value={shown.temporary_password} size={16} />
            </div>
            <p className="text-sm text-fg-3">{t("admin.user.sec.revokedN", { n: shown.sessions_revoked })}</p>
          </div>
        )}
      </Dialog>
    </>
  );
}

function Sessions({ admin, userId, sessions, loading }: { admin: Admin; userId: string; sessions?: Session[]; loading: boolean }) {
  const { t } = useTranslation();
  const act = can(admin, "users.security");
  const [ending, setEnding] = useState<Session | null>(null);
  const columns = useMemo<ColumnDef<Session, unknown>[]>(
    () => [
      {
        id: "device", header: t("admin.user.sec.device"),
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-1.5">
            <IdText value={row.original.device_id} chars={10} />
            <Badge tone="neutral" title={t("admin.user.sec.client")}>
              {row.original.client_type}
            </Badge>
          </span>
        ),
      },
      {
        id: "ua", header: t("admin.user.sec.userAgent"),
        cell: ({ row }) => (
          <span className="block max-w-40 truncate text-xs text-fg-2" title={row.original.user_agent}>
            {row.original.user_agent || "—"}
          </span>
        ),
      },
      { accessorKey: "ip", header: "IP", cell: ({ row }) => <span className="font-mono text-xs">{row.original.ip || "—"}</span> },
      { id: "created", header: t("admin.user.sec.signedIn"), cell: ({ row }) => <TimeText value={row.original.created_at} style="datetime" /> },
      { id: "seen", header: t("admin.user.sec.lastSeen"), cell: ({ row }) => <TimeText value={row.original.last_seen_at} style="datetime" /> },
      ...(act
        ? [
            {
              id: "end", header: "", meta: { align: "right" as const },
              cell: ({ row }: { row: { original: Session } }) => (
                <Button size="sm" variant="secondary" onClick={() => setEnding(row.original)}>
                  {t("admin.user.sec.end")}
                </Button>
              ),
            },
          ]
        : []),
    ],
    [t, act],
  );
  return (
    <Card
      title={t("admin.user.sec.sessions")}
      extra={
        act && (sessions?.length ?? 0) > 0 ? (
          <DangerAction
            trigger={(open) => (
              <Button size="sm" variant="danger" onClick={open} icon={<LogOut size={14} />}>
                {t("admin.user.sec.endAll")}
              </Button>
            )}
            title={t("admin.user.sec.endAllTitle")}
            target={<span className="font-mono text-xs">{userId}</span>}
            confirmWord={lastFour(userId)}
            run={async (reason) =>
              adminData(await adminApi.POST("/admin/v1/users/{id}/sessions/revoke", { params: { path: { id: userId } }, body: { reason } }))
            }
            success={t("admin.user.sec.ended")}
            invalidate={securityKeys(userId)}
          />
        ) : undefined
      }
    >
      <DataTable
        columns={columns}
        data={sessions ?? []}
        getRowId={(s) => s.id}
        loading={loading}
        density="compact"
        empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.user.sec.noSessions")}</p>}
      />
      {ending && (
        <DangerAction
          open
          onOpenChange={(o) => !o && setEnding(null)}
          title={t("admin.user.sec.endTitle")}
          target={<span className="font-mono text-xs">{ending.id}</span>}
          confirmWord={lastFour(ending.id)}
          run={async (reason) =>
            adminData(
              await adminApi.POST("/admin/v1/users/{id}/sessions/revoke", { params: { path: { id: userId } }, body: { session_id: ending.id, reason } }),
            )
          }
          success={t("admin.user.sec.ended")}
          invalidate={securityKeys(userId)}
        />
      )}
    </Card>
  );
}

function Devices({ devices, loading }: { devices?: Device[]; loading: boolean }) {
  const { t } = useTranslation();
  const columns = useMemo<ColumnDef<Device, unknown>[]>(
    () => [
      { id: "device", header: t("admin.user.sec.device"), cell: ({ row }) => <IdText value={row.original.device_id} chars={16} /> },
      { id: "first", header: t("admin.user.sec.firstSeen"), cell: ({ row }) => <TimeText value={row.original.first_seen_at} /> },
      { id: "last", header: t("admin.user.sec.lastSeen"), cell: ({ row }) => <TimeText value={row.original.last_seen_at} /> },
    ],
    [t],
  );
  return (
    <Card title={t("admin.user.sec.devices")}>
      <DataTable
        columns={columns}
        data={devices ?? []}
        getRowId={(d) => d.device_id}
        loading={loading}
        density="compact"
        empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.user.sec.noDevices")}</p>}
      />
    </Card>
  );
}

function Logins({ userId }: { userId: string }) {
  const { t } = useTranslation();
  const list = useCursorList<Login>(["admin", "user", userId, "logins"], async (cursor) =>
    adminData(await adminApi.GET("/admin/v1/users/{id}/login-history", { params: { path: { id: userId }, query: { cursor, limit: pageSize() } } })),
  );
  const columns = useMemo<ColumnDef<Login, unknown>[]>(
    () => [
      { id: "time", header: t("admin.common.time"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
      { id: "method", header: t("admin.user.sec.method"), cell: ({ row }) => <EnumText group="loginMethod" code={row.original.method} /> },
      {
        id: "result", header: t("admin.user.sec.result"),
        cell: ({ row }) => <Badge tone={row.original.result === "SUCCESS" ? "success" : "danger"}>{row.original.result}</Badge>,
      },
      { accessorKey: "identity_mask", header: t("admin.user.sec.identity") },
      {
        id: "device", header: t("admin.user.sec.device"),
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-1.5">
            <IdText value={row.original.device_id} chars={8} />
            {row.original.new_device && <Badge tone="warn">{t("admin.user.sec.newDevice")}</Badge>}
          </span>
        ),
      },
      { accessorKey: "ip", header: "IP", cell: ({ row }) => <span className="font-mono text-xs">{row.original.ip || "—"}</span> },
    ],
    [t],
  );
  return (
    <Card title={t("admin.user.sec.logins")}>
      <ListTable
        list={list}
        columns={columns}
        getRowId={(e) => String(e.id)}
        aria-label={t("admin.user.sec.logins")}
        empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.user.sec.noLogins")}</p>}
      />
    </Card>
  );
}
