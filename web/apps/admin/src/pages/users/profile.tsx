import { errorText } from "@exchange/core";
import { can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, cn, DataTable, ErrorState, IconButton, KeyValue, Skeleton, type ColumnDef } from "@exchange/ui";
import { Eye, EyeOff, Mail, Phone } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { errorToast } from "../../kit/actions";
import { EnumBadge, EnumText } from "../../kit/enums";
import { TimeText } from "../../kit/format";
import { Card } from "../../kit/Page";
import { useContacts, useHistory, useSecurity } from "./data";

type UserSummary = AdminSchemas["UserSummary"];
type Identity = AdminSchemas["Identity"];
type Consent = AdminSchemas["Consent"];

/**
 * useIdentities is an account's identities as the page shows them: masked,
 * or in full once an administrator with users.contacts revealed them
 * (audited) until the page closes.
 */
export function useIdentities(admin: Admin, userId: string) {
  const sec = useSecurity(userId);
  const contacts = useContacts(userId);
  const revealed = contacts.data !== undefined;
  const reveal = async () => {
    const r = await contacts.refetch();
    if (r.error) errorToast(r.error);
  };
  return {
    identities: contacts.data ?? sec.data?.identities,
    loading: sec.isPending,
    error: sec.error,
    revealed,
    canReveal: can(admin, "users.contacts"),
    revealing: contacts.isFetching,
    reveal,
  };
}

/** Contacts is the email and phone in the page's header, masked until revealed. */
export function Contacts({ admin, userId }: { admin: Admin; userId: string }) {
  const { t } = useTranslation();
  const ids = useIdentities(admin, userId);
  if (ids.loading) return <Skeleton className="h-10 w-full" />;
  if (ids.error) return <p className="text-xs text-fg-3">{t("admin.common.unavailable")}</p>;
  return (
    <div className="flex items-start gap-2">
      <ul className="min-w-0 flex-1 space-y-1 text-sm">
        {(ids.identities ?? []).map((id) => (
          <li key={id.kind} className="flex items-center gap-2" data-testid={`contact-${id.kind}`}>
            {id.kind === "EMAIL" ? <Mail size={14} className="shrink-0 text-fg-3" /> : <Phone size={14} className="shrink-0 text-fg-3" />}
            <span className={cn("truncate", ids.revealed ? "font-medium" : "font-mono text-xs")} title={id.value}>
              {id.value}
            </span>
          </li>
        ))}
        {(ids.identities ?? []).length === 0 && <li className="text-fg-3">{t("admin.user.noIdentities")}</li>}
      </ul>
      {ids.canReveal && !ids.revealed && (ids.identities ?? []).length > 0 && (
        <IconButton
          size="sm"
          icon={<Eye />}
          label={t("admin.user.reveal")}
          title={t("admin.user.revealHint")}
          onClick={() => void ids.reveal()}
          disabled={ids.revealing}
        />
      )}
      {ids.revealed && (
        <span className="grid size-8 place-items-center text-fg-3" title={t("admin.user.revealed")}>
          <EyeOff size={15} />
        </span>
      )}
    </div>
  );
}

/** ProfileTab is an account's profile: settings, identities, accepted documents and status changes. */
export function ProfileTab({ admin, user }: { admin: Admin; user: UserSummary }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-5">
      <KeyValue
        layout="grid"
        columns={3}
        items={[
          { label: t("admin.users.id"), value: <span className="font-mono text-xs">{user.id}</span>, copy: user.id },
          { label: t("admin.user.profileRows.status"), value: <EnumBadge group="userStatus" code={user.status} /> },
          { label: t("admin.user.registered"), value: <TimeText value={user.created_at} /> },
          { label: t("admin.user.profileRows.region"), value: user.region || "—" },
          { label: t("admin.user.profileRows.language"), value: user.language || "—" },
          { label: t("admin.user.timezone"), value: user.timezone || t("admin.user.browserZone") },
          { label: t("admin.user.profileRows.kyc"), value: user.kyc_level },
        ]}
      />
      <Identities admin={admin} userId={user.id} />
      <div className="grid gap-4 lg:grid-cols-2">
        <Consents userId={user.id} />
        <StatusHistory userId={user.id} />
      </div>
    </div>
  );
}

function Identities({ admin, userId }: { admin: Admin; userId: string }) {
  const { t } = useTranslation();
  const ids = useIdentities(admin, userId);
  const columns = useMemo<ColumnDef<Identity, unknown>[]>(
    () => [
      { id: "kind", header: t("admin.user.identityKind"), cell: ({ row }) => <EnumText group="identityKind" code={row.original.kind} /> },
      { accessorKey: "value", header: t("admin.user.identityValue"), cell: ({ row }) => <span className="font-mono text-xs">{row.original.value}</span> },
      {
        id: "verified", header: t("admin.user.verifiedAt"),
        cell: ({ row }) => (row.original.verified_at ? <TimeText value={row.original.verified_at} /> : <Badge tone="warn">{t("admin.user.notVerified")}</Badge>),
      },
      { id: "bound", header: t("admin.user.boundAt"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
    ],
    [t],
  );
  return (
    <Card
      title={t("admin.user.identities")}
      extra={
        ids.canReveal && !ids.revealed && (ids.identities ?? []).length > 0 ? (
          <button type="button" className="inline-flex items-center gap-1 text-xs text-brand-strong hover:underline" onClick={() => void ids.reveal()} disabled={ids.revealing}>
            <Eye size={13} />
            {t("admin.user.reveal")}
          </button>
        ) : undefined
      }
    >
      {ids.error ? (
        <ErrorState message={errorText(ids.error)} />
      ) : (
        <DataTable
          columns={columns}
          data={ids.identities ?? []}
          getRowId={(i) => i.kind}
          loading={ids.loading}
          density="compact"
          empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.user.noIdentities")}</p>}
        />
      )}
    </Card>
  );
}

function Consents({ userId }: { userId: string }) {
  const { t } = useTranslation();
  const h = useHistory(userId);
  const columns = useMemo<ColumnDef<Consent, unknown>[]>(
    () => [
      { id: "doc", header: t("admin.user.document"), cell: ({ row }) => <EnumText group="consentDocument" code={row.original.document} /> },
      { accessorKey: "version", header: t("admin.user.version"), cell: ({ row }) => <span className="font-mono text-xs">{row.original.version}</span> },
      { id: "at", header: t("admin.user.acceptedAt"), cell: ({ row }) => <TimeText value={row.original.accepted_at} /> },
    ],
    [t],
  );
  return (
    <Card title={t("admin.user.consents")}>
      {h.isError ? (
        <ErrorState message={errorText(h.error)} onRetry={() => void h.refetch()} />
      ) : (
        <DataTable
          columns={columns}
          data={h.data?.consents ?? []}
          getRowId={(c) => `${c.document}:${c.version}`}
          loading={h.isPending}
          density="compact"
          empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.user.noConsents")}</p>}
        />
      )}
    </Card>
  );
}

/** StatusHistory is the account's status changes as a timeline, newest first. */
function StatusHistory({ userId }: { userId: string }) {
  const { t } = useTranslation();
  const h = useHistory(userId);
  const changes = h.data?.status_changes ?? [];
  return (
    <Card title={t("admin.user.statusHistory")}>
      {h.isError ? (
        <ErrorState message={errorText(h.error)} onRetry={() => void h.refetch()} />
      ) : h.isPending ? (
        <Skeleton className="h-24 w-full" />
      ) : changes.length === 0 ? (
        <p className="py-4 text-center text-sm text-fg-3">{t("admin.user.noStatusChanges")}</p>
      ) : (
        <ol className="relative ml-1.5 border-l border-line-1">
          {changes.map((c, i) => (
            <li key={`${c.at}:${i}`} className="relative pb-4 pl-4 last:pb-0">
              <span className="absolute -left-[5px] top-1.5 size-2.5 rounded-full border-2 border-bg-1 bg-brand" />
              <div className="flex flex-wrap items-center gap-1.5 text-sm">
                <EnumBadge group="userStatus" code={c.from_status} />
                <span className="text-fg-3">→</span>
                <EnumBadge group="userStatus" code={c.to_status} />
                <span className="text-fg-2">
                  <EnumText group="reasonCode" code={c.reason_code} />
                </span>
              </div>
              <div className="mt-0.5 text-xs text-fg-3">
                {c.actor} · <TimeText value={c.at} />
              </div>
            </li>
          ))}
        </ol>
      )}
    </Card>
  );
}
