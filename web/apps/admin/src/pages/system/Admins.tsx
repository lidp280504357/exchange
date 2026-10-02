import { adminApi, adminData, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import {
  Badge, Button, CopyButton, DataTable, Dialog, Drawer, DropdownMenu, ErrorState, Input, QrCode, Select, Skeleton, type ColumnDef,
  type DataColumnMeta, type MenuEntry,
} from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { Check, ChevronDown, Plus } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, FormError, lastFour } from "../../kit/actions";
import { EnumBadge, useEnum } from "../../kit/enums";
import { TimeText } from "../../kit/format";
import { stagger } from "../../kit/motion";
import { Card, Page } from "../../kit/Page";

type Managed = AdminSchemas["ManagedAdmin"];
type Credentials = AdminSchemas["AdminCredentials"];
type Role = AdminSchemas["AdminRole"];
type Session = AdminSchemas["AdminSession"];
type Kind = "disable" | "enable" | "role" | "password" | "totp";

const ROLES: Role[] = ["ADMIN", "OPERATOR", "FINANCE", "AUDITOR"];
const adminsKey = ["admin", "admins"];
const right: DataColumnMeta = { align: "right" };
const emailRe = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/**
 * Administrators and roles (design 2026-10-02 §4.6): the administrators
 * with their roles, sign-ins and sessions; an ADMIN creates them, changes
 * their roles, disables and enables them, resets their passwords and
 * authenticators and ends their sessions, each with a reason. A password
 * or authenticator secret shows once. Nobody changes their own account
 * here; the roles' permissions are shown read-only.
 */
export default function Admins({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const label = useEnum();
  const q = useQuery({ queryKey: adminsKey, queryFn: async () => adminData(await adminApi.GET("/admin/v1/admins")).admins });
  const [creating, setCreating] = useState(false);
  const [action, setAction] = useState<{ kind: Kind; target: Managed } | null>(null);
  const [shown, setShown] = useState<{ email: string; creds: Credentials } | null>(null);
  const [sessionsOf, setSessionsOf] = useState<Managed | null>(null);
  const columns = useMemo<ColumnDef<Managed, unknown>[]>(
    () => [
      {
        id: "who", header: t("admin.admins.email"),
        cell: ({ row: { original: a } }) => (
          <span className="flex flex-col">
            <span className="flex items-center gap-1.5">
              <span className="font-medium text-fg-1">{a.email}</span>
              {a.id === admin.id && <Badge tone="brand">{t("admin.admins.you")}</Badge>}
            </span>
            <span className="text-xs text-fg-3">{a.name}</span>
          </span>
        ),
      },
      { id: "role", header: t("admin.admins.role"), cell: ({ row }) => <span title={row.original.role}>{label("role", row.original.role)}</span> },
      {
        id: "status", header: t("admin.common.status"),
        cell: ({ row: { original: a } }) => (
          <span className="flex flex-col items-start gap-0.5">
            <EnumBadge group="adminStatus" code={a.status} />
            {a.locked_until && new Date(a.locked_until) > new Date() && (
              <span className="text-xs text-warn">{t("admin.admins.locked")} <TimeText value={a.locked_until} /></span>
            )}
            {a.failed_attempts > 0 && <span className="text-xs text-fg-3">{t("admin.admins.failed", { n: a.failed_attempts })}</span>}
          </span>
        ),
      },
      {
        id: "login", header: t("admin.admins.lastLogin"),
        cell: ({ row }) => (row.original.last_login_at ? <TimeText value={row.original.last_login_at} /> : <span className="text-fg-3">{t("admin.admins.never")}</span>),
      },
      {
        id: "sessions", header: t("admin.admins.sessions"), meta: right,
        cell: ({ row: { original: a } }) => (
          <button type="button" className="tabular-nums text-info hover:underline" onClick={() => setSessionsOf(a)}>
            {t("admin.admins.live", { n: a.sessions })}
          </button>
        ),
      },
      { id: "created", header: t("admin.admins.created"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
      {
        id: "actions", header: "", meta: right,
        cell: ({ row: { original: a } }) => {
          const self = a.id === admin.id;
          const items: MenuEntry[] = [
            { key: "role", label: t("admin.admins.changeRole"), onSelect: () => setAction({ kind: "role", target: a }) },
            { key: "password", label: t("admin.admins.resetPassword"), onSelect: () => setAction({ kind: "password", target: a }) },
            { key: "totp", label: t("admin.admins.resetTotp"), onSelect: () => setAction({ kind: "totp", target: a }) },
            { key: "sessions", label: t("admin.admins.viewSessions"), onSelect: () => setSessionsOf(a) },
            { type: "separator", key: "sep" },
            a.status === "ACTIVE"
              ? { key: "disable", label: t("admin.admins.disable"), danger: true, onSelect: () => setAction({ kind: "disable", target: a }) }
              : { key: "enable", label: t("admin.admins.enable"), onSelect: () => setAction({ kind: "enable", target: a }) },
          ];
          return self ? (
            <span className="text-xs text-fg-3">{t("admin.admins.selfHint")}</span>
          ) : (
            <DropdownMenu
              trigger={
                <Button size="sm" variant="ghost" className="whitespace-nowrap" data-testid={`admin-actions-${a.email}`}>
                  {t("admin.common.actions")}
                  <ChevronDown size={12} />
                </Button>
              }
              items={items}
            />
          );
        },
      },
    ],
    [t, label, admin.id],
  );
  return (
    <Page
      title={t("admin.nav.admins")}
      help={t("admin.admins.help")}
      actions={
        <Button icon={<Plus size={14} />} onClick={() => setCreating(true)}>
          {t("admin.admins.new")}
        </Button>
      }
    >
      <Card className="stagger">
        <DataTable
          columns={columns}
          data={q.data ?? []}
          getRowId={(a) => a.id}
          loading={q.isPending}
          error={q.error}
          onRetry={() => void q.refetch()}
          rowClassName={(a) => (a.status === "DISABLED" ? "opacity-60" : undefined)}
          density="compact"
        />
      </Card>
      <RoleMatrix />
      <CreateAdmin open={creating} onOpenChange={setCreating} onCreated={(c) => setShown({ email: c.admin?.email ?? "", creds: c })} />
      {action && (
        <AdminAction
          key={`${action.kind}:${action.target.id}`}
          kind={action.kind}
          target={action.target}
          onClose={() => setAction(null)}
          onCredentials={(creds) => setShown({ email: action.target.email, creds })}
        />
      )}
      <CredentialsDialog shown={shown} onClose={() => setShown(null)} />
      {sessionsOf && <SessionsDrawer target={sessionsOf} onClose={() => setSessionsOf(null)} />}
    </Page>
  );
}

/** CreateAdmin asks for the address, the name and the role; the confirmation word is the role in lower case. */
function CreateAdmin({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (o: boolean) => void; onCreated: (c: Credentials) => void }) {
  const { t } = useTranslation();
  const label = useEnum();
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState<Role>("OPERATOR");
  const close = (o: boolean) => {
    onOpenChange(o);
    if (!o) {
      setEmail("");
      setName("");
      setRole("OPERATOR");
    }
  };
  return (
    <DangerAction
      open={open}
      onOpenChange={close}
      danger={false}
      title={t("admin.admins.createTitle")}
      description={t("admin.admins.createDesc")}
      target={
        <span>
          {email.trim() || "—"} · {label("role", role)}
        </span>
      }
      confirmWord={role.toLowerCase()}
      run={async (reason) => {
        if (!emailRe.test(email.trim())) throw new FormError(t("admin.admins.invalidEmail"));
        if (!name.trim()) throw new FormError(t("admin.admins.invalidName"));
        return adminData(await adminApi.POST("/admin/v1/admins", { body: { email: email.trim(), name: name.trim(), role, reason } }));
      }}
      success={t("admin.admins.createdOk")}
      invalidate={[adminsKey]}
      onDone={(r) => onCreated(r as Credentials)}
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.admins.email")}
          <Input
            id="new-admin-email"
            value={email}
            onValueChange={setEmail}
            type="email"
            autoComplete="off"
            error={email && !emailRe.test(email.trim()) ? t("admin.admins.invalidEmail") : undefined}
          />
        </label>
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.admins.name")}
          <Input id="new-admin-name" value={name} onValueChange={setName} autoComplete="off" />
        </label>
        <label className="flex flex-col gap-1.5 text-sm text-fg-2 sm:col-span-2">
          {t("admin.admins.role")}
          <Select
            aria-label={t("admin.admins.role")}
            value={role}
            onValueChange={(v) => setRole(v as Role)}
            options={ROLES.map((r) => ({ value: r, label: `${label("role", r)} · ${r}` }))}
          />
        </label>
      </div>
    </DangerAction>
  );
}

/** AdminAction confirms one change of another administrator; a reset hands its credentials on. */
function AdminAction({ kind, target, onClose, onCredentials }: { kind: Kind; target: Managed; onClose: () => void; onCredentials: (c: Credentials) => void }) {
  const { t } = useTranslation();
  const label = useEnum();
  const [role, setRole] = useState<Role>(target.role);
  const path = { params: { path: { id: target.id } } };
  const runs: Record<Kind, (reason: string) => Promise<unknown>> = {
    disable: async (reason) => adminData(await adminApi.POST("/admin/v1/admins/{id}/status", { ...path, body: { enabled: false, reason } })),
    enable: async (reason) => adminData(await adminApi.POST("/admin/v1/admins/{id}/status", { ...path, body: { enabled: true, reason } })),
    role: async (reason) => {
      if (role === target.role) throw new FormError(t("admin.admins.sameRole"));
      return adminData(await adminApi.POST("/admin/v1/admins/{id}/role", { ...path, body: { role, reason } }));
    },
    password: async (reason) => adminData(await adminApi.POST("/admin/v1/admins/{id}/password-reset", { ...path, body: { reason } })),
    totp: async (reason) => adminData(await adminApi.POST("/admin/v1/admins/{id}/totp-reset", { ...path, body: { reason } })),
  };
  const texts: Record<Kind, { title: string; desc: string; success: string }> = {
    disable: { title: "disableTitle", desc: "disableDesc", success: "disabledOk" },
    enable: { title: "enableTitle", desc: "enableDesc", success: "enabledOk" },
    role: { title: "roleTitle", desc: "roleDesc", success: "roleChanged" },
    password: { title: "resetPasswordTitle", desc: "resetPasswordDesc", success: "passwordReset" },
    totp: { title: "resetTotpTitle", desc: "resetTotpDesc", success: "totpReset" },
  };
  const x = texts[kind];
  return (
    <DangerAction
      open
      onOpenChange={(o) => !o && onClose()}
      danger={kind !== "enable"}
      title={t(`admin.admins.${x.title}`)}
      description={t(`admin.admins.${x.desc}`)}
      target={
        <span className="flex flex-col">
          <span>
            {target.email} · {label("role", target.role)}
          </span>
          <span className="font-mono text-xs text-fg-3">{target.id}</span>
        </span>
      }
      confirmWord={lastFour(target.id)}
      run={runs[kind]}
      success={t(`admin.admins.${x.success}`)}
      invalidate={[adminsKey, ["admin", "admins", target.id, "sessions"]]}
      onDone={(r) => {
        onClose();
        if (kind === "password" || kind === "totp") onCredentials(r as Credentials);
      }}
    >
      {kind === "role" && (
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.admins.newRole")}
          <Select
            aria-label={t("admin.admins.newRole")}
            value={role}
            onValueChange={(v) => setRole(v as Role)}
            options={ROLES.map((r) => ({ value: r, label: `${label("role", r)} · ${r}`, disabled: r === target.role }))}
          />
        </label>
      )}
    </DangerAction>
  );
}

/** CredentialsDialog shows a password and an authenticator secret once; nothing keeps them. */
function CredentialsDialog({ shown, onClose }: { shown: { email: string; creds: Credentials } | null; onClose: () => void }) {
  const { t } = useTranslation();
  const c = shown?.creds;
  return (
    <Dialog
      open={shown !== null}
      onOpenChange={(o) => !o && onClose()}
      title={t("admin.admins.credentialsTitle")}
      description={t("admin.admins.credentialsOnce")}
      persistent
      footer={
        <Button variant="primary" onClick={onClose}>
          {t("admin.admins.credentialsDone")}
        </Button>
      }
    >
      {c && (
        <div className="flex flex-col gap-4">
          <div className="text-sm text-fg-2">{shown.email}</div>
          {c.password && (
            <Secret label={t("admin.admins.password")} value={c.password} testId="admin-password" />
          )}
          {c.totp_secret && (
            <div className="flex flex-col gap-2">
              <Secret label={t("admin.admins.totpSecret")} value={c.totp_secret} testId="admin-totp-secret" />
              {c.totp_uri && (
                <div className="flex items-center gap-4">
                  <QrCode value={c.totp_uri} size={136} label={t("admin.admins.totpSecret")} />
                  <p className="text-sm text-fg-3">{t("admin.admins.totpScan")}</p>
                </div>
              )}
            </div>
          )}
        </div>
      )}
    </Dialog>
  );
}

function Secret({ label, value, testId }: { label: string; value: string; testId: string }) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-xs text-fg-3">{label}</span>
      <div className="flex items-center justify-between gap-3 rounded-2 border border-line-1 bg-bg-2 px-4 py-3">
        <span className="select-all break-all font-mono text-base tracking-wider" data-testid={testId}>
          {value}
        </span>
        <CopyButton value={value} size={16} />
      </div>
    </div>
  );
}

/** SessionsDrawer lists an administrator's live sessions and ends them all. */
function SessionsDrawer({ target, onClose }: { target: Managed; onClose: () => void }) {
  const { t } = useTranslation();
  const key = ["admin", "admins", target.id, "sessions"];
  const q = useQuery({
    queryKey: key,
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/admins/{id}/sessions", { params: { path: { id: target.id } } })).sessions,
  });
  const columns = useMemo<ColumnDef<Session, unknown>[]>(
    () => [
      { accessorKey: "ip", header: t("admin.admins.ip"), cell: ({ row }) => <span className="font-mono text-xs">{row.original.ip}</span> },
      {
        id: "device", header: t("admin.admins.device"),
        cell: ({ row }) => <span className="block max-w-[11rem] truncate text-xs text-fg-2" title={row.original.user_agent}>{row.original.user_agent}</span>,
      },
      { id: "started", header: t("admin.admins.started"), cell: ({ row }) => <TimeText value={row.original.created_at} style="datetime" /> },
      { id: "seen", header: t("admin.admins.lastSeen"), cell: ({ row }) => <TimeText value={row.original.last_seen_at} style="datetime" /> },
      { id: "expires", header: t("admin.admins.expires"), cell: ({ row }) => <TimeText value={row.original.expires_at} style="datetime" /> },
    ],
    [t],
  );
  const sessions = q.data ?? [];
  return (
    <Drawer
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("admin.admins.sessionsTitle", { email: target.email })}
      description={<span className="font-mono text-xs">{target.id}</span>}
      width={760}
      actions={
        <DangerAction
          trigger={(open) => (
            <Button size="sm" variant="danger" disabled={sessions.length === 0} onClick={open}>
              {t("admin.admins.revokeAll")}
            </Button>
          )}
          title={t("admin.admins.revokeTitle")}
          description={t("admin.admins.revokeDesc")}
          target={<span>{target.email}</span>}
          confirmWord={lastFour(target.id)}
          run={async (reason) => {
            const res = await adminApi.POST("/admin/v1/admins/{id}/sessions/revoke", { params: { path: { id: target.id } }, body: { reason } });
            if (!res.response.ok) adminData(res);
          }}
          success={t("admin.admins.sessionsRevoked")}
          invalidate={[key, adminsKey]}
        />
      }
    >
      {q.isError ? (
        <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />
      ) : (
        <DataTable
          columns={columns}
          data={sessions}
          getRowId={(s) => `${s.created_at}/${s.ip}`}
          loading={q.isPending}
          empty={<p className="py-6 text-center text-sm text-fg-3">{t("admin.admins.noSessions")}</p>}
          density="compact"
        />
      )}
    </Drawer>
  );
}

/** RoleMatrix shows which role holds which permission (read-only). */
function RoleMatrix() {
  const { t } = useTranslation();
  const label = useEnum();
  const q = useQuery({ queryKey: ["admin", "roles"], queryFn: async () => adminData(await adminApi.GET("/admin/v1/roles")).roles, staleTime: Infinity });
  const roles = q.data ?? [];
  const perms = [...new Set(roles.flatMap((r) => r.permissions))];
  return (
    <Card title={t("admin.admins.matrix")} className="stagger" style={stagger(1)}>
      <p className="mb-3 text-sm text-fg-3">{t("admin.admins.matrixHelp")}</p>
      {q.isError ? (
        <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />
      ) : q.isPending ? (
        <Skeleton className="h-64 w-full" />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm" data-testid="role-matrix">
            <thead>
              <tr className="border-b border-line-1 text-left text-xs text-fg-3">
                <th className="py-2 pr-4 font-normal">{t("admin.admins.permission")}</th>
                {roles.map((r) => (
                  <th key={r.role} className="px-3 py-2 text-center font-normal" title={r.role}>
                    {label("role", r.role)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {perms.map((p) => (
                <tr key={p} className="border-b border-line-1 last:border-0 hover:bg-bg-2">
                  <td className="py-1.5 pr-4">
                    <span className="text-fg-1">{t(`admin.perm.${p.replaceAll(".", "_")}`, { defaultValue: p })}</span>
                    <span className="ml-2 font-mono text-xs text-fg-3">{p}</span>
                  </td>
                  {roles.map((r) => (
                    <td key={r.role} className="px-3 py-1.5 text-center">
                      {r.permissions.includes(p) ? <Check size={14} className="inline text-success" aria-label="✓" /> : <span className="text-fg-3">·</span>}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}
