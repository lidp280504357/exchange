import { dec, errorText, formatDecimal } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, Drawer, KeyTag, Skeleton, Stepper, type DataColumnMeta, type ColumnDef, type RowSelectionState } from "@exchange/ui";
import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { CirclePause, CirclePlay } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useConsoleSettings } from "../../live";
import { DangerAction, lastFour } from "../../kit/actions";
import { SuspendedBadge, useSuspended } from "./suspensions";
import { EnumBadge, useEnum } from "../../kit/enums";
import { Fields } from "../../kit/fields";
import { IdText, Num, TimeText, useTimeText, UserCell } from "../../kit/format";
import { kindParam } from "../../kit/kinds";
import { ListTable, pageSize, useCursorList, type CursorList } from "../../kit/lists";
import { clean } from "../records/tables";

export type Withdrawal = AdminSchemas["Withdrawal"];
export type WithdrawalQuery = {
  user_id?: string;
  asset?: string;
  network?: string;
  status?: string;
  /** "true" for those on hold, "false" for the others. */
  held?: string;
  min_value_usdt?: string;
  max_value_usdt?: string;
  min_risk?: string;
  /** The accounts' kind (L1): none, the humans; BOT, TEST, SYSTEM or ALL. */
  kind?: string;
};

const right: DataColumnMeta = { align: "right" };

export function useWithdrawals(q: WithdrawalQuery) {
  return useCursorList<Withdrawal>(["admin", "withdrawals", q], async (cursor) => {
    const { held, min_risk, kind, ...rest } = clean(q);
    const risk = min_risk && /^\d+$/.test(min_risk) ? Number(min_risk) : undefined;
    return adminData(
      await adminApi.GET("/admin/v1/withdrawals", {
        params: {
          query: {
            ...rest, held: held === "true" || held === "false" ? held : undefined, min_risk: risk, kind: kindParam(kind) as never, cursor,
            limit: pageSize(),
          },
        },
      }),
    );
  });
}

/** HeldBadge marks a withdrawal on hold, with its note on hover. */
function HeldBadge({ w }: { w: Withdrawal }) {
  const { t } = useTranslation();
  if (!w.held_at) return null;
  return (
    <Badge tone="warn" title={w.hold_note || undefined} icon={<CirclePause size={12} />}>
      {t("admin.hold.held")}
    </Badge>
  );
}

export function WithdrawalsTable({
  list, withUser = true, onRowClick, selection, onSelectionChange,
}: {
  list: CursorList<Withdrawal>;
  withUser?: boolean;
  onRowClick?: (w: Withdrawal) => void;
  /** Rows checked for a batch review (by withdrawal ID); without it no checkboxes. */
  selection?: RowSelectionState;
  onSelectionChange?: (s: RowSelectionState) => void;
}) {
  const { t } = useTranslation();
  const label = useEnum();
  const suspended = useSuspended();
  const columns = useMemo<ColumnDef<Withdrawal, unknown>[]>(
    () => [
      { id: "time", header: t("admin.withdrawals.requested"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
      ...(withUser ? [{ id: "user", header: t("admin.common.user"), cell: ({ row }) => <UserCell id={row.original.user_id} /> } as ColumnDef<Withdrawal, unknown>] : []),
      { id: "amount", header: t("admin.common.amount"), meta: right, cell: ({ row }) => <Num value={row.original.amount} unit={row.original.asset} /> },
      { accessorKey: "network", header: t("admin.common.network") },
      { id: "address", header: t("admin.withdrawals.address"), cell: ({ row }) => <IdText value={row.original.address} chars={10} /> },
      { id: "value", header: t("admin.withdrawals.value"), meta: right, cell: ({ row }) => <Num value={row.original.value_usdt} decimals={2} /> },
      {
        id: "risk",
        header: t("admin.withdrawals.risk"),
        cell: ({ row }) => {
          const w = row.original;
          const score = w.risk_score ?? 0;
          return (
            <span className="inline-flex flex-wrap items-center gap-1" title={w.risk_reasons.map((r) => label("riskReason", r)).join("、")}>
              <Badge tone={score >= 50 ? "danger" : score > 0 ? "warn" : "neutral"}>{score}</Badge>
              {w.risk_reasons.length > 0 && <span className="text-xs text-fg-3">{w.risk_reasons.length}</span>}
            </span>
          );
        },
      },
      {
        id: "approvals",
        header: t("admin.withdrawals.approvals"),
        cell: ({ row }) => (
          <span className="tabular-nums">
            {row.original.approvals?.length ?? 0}/{row.original.approvals_required}
          </span>
        ),
      },
      {
        id: "status",
        header: t("admin.common.status"),
        cell: ({ row }) => (
          <span className="inline-flex flex-wrap items-center gap-1">
            <EnumBadge group="withdrawalStatus" code={row.original.status} />
            <HeldBadge w={row.original} />
            <SuspendedBadge status={row.original.status} asset={row.original.asset} suspended={suspended} />
          </span>
        ),
      },
    ],
    [t, withUser, label, suspended],
  );
  return (
    <ListTable
      list={list}
      columns={columns}
      getRowId={(w) => w.id}
      onRowClick={onRowClick}
      selectable={!!onSelectionChange}
      rowSelection={selection}
      onRowSelectionChange={onSelectionChange}
      aria-label="withdrawals"
    />
  );
}

// An address added to the address book this shortly before a withdrawal
// is new (the wallet's NEW_ADDRESS risk rule).
const NEW_ADDRESS_MS = 72 * 3600_000;

/**
 * WithdrawalDrawer shows a withdrawal's risk, its address in the user's
 * address book, the user's withdrawals so far, approvals and progress,
 * with the review actions (approve, reject, hold).
 */
export function WithdrawalDrawer({ admin, w: row, onClose }: { admin: Admin; w: Withdrawal; onClose: () => void }) {
  const { t } = useTranslation();
  const label = useEnum();
  const time = useTimeText();
  const detail = useQuery({
    queryKey: ["admin", "withdrawals", "detail", row.id],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/withdrawals/{id}", { params: { path: { id: row.id } } })),
  });
  // The detail is the latest word on it (say, a hold since the list loaded).
  const w = detail.data?.withdrawal ?? row;
  const steps = [
    { key: "requested", at: w.created_at },
    { key: "approvedAt", at: w.approved_at },
    w.custody ? { key: "submittedAt", at: w.submitted_at } : { key: "broadcastAt", at: w.broadcast_at },
    { key: "confirmedAt", at: w.confirmed_at },
  ];
  const current = steps.reduce((n, s, i) => (s.at ? i : n), 0);
  const reviewable = w.status === "PENDING_REVIEW" && can(admin, "withdrawals.review");
  const suspension = useSuspended().get(w.asset);
  // Single-person mode: one approval completes a withdrawal within the limit.
  const settings = useConsoleSettings().data;
  const alone =
    !!settings && !settings.two_person_approval && !!w.value_usdt && dec.isDecimal(w.value_usdt) && dec.lte(w.value_usdt, settings.withdrawal_max_usdt);
  const review = (approve: boolean) => async (reason: string, key: string) =>
    adminData(
      await adminApi.POST("/admin/v1/withdrawals/{id}/review", {
        params: { path: { id: w.id }, header: { "Idempotency-Key": key } },
        body: { approve, reason },
      }),
    );
  const hold = (on: boolean) => async (note: string) =>
    adminData(await adminApi.POST("/admin/v1/withdrawals/{id}/hold", { params: { path: { id: w.id } }, body: { hold: on, note } }));
  const held = !!w.held_at;
  const target = (
    <span className="text-sm">
      <Num value={w.amount} unit={w.asset} /> → <span className="font-mono text-xs">{w.address}</span>
    </span>
  );
  return (
    <Drawer
      open
      onOpenChange={(open) => !open && onClose()}
      title={`${w.amount} ${w.asset}`}
      description={<span className="font-mono">{w.id}</span>}
      actions={<EnumBadge group="withdrawalStatus" code={w.status} />}
    >
      <div className="flex flex-col gap-5">
        <Stepper
          steps={steps.map((s) => ({ key: s.key, title: t(`admin.withdrawals.${s.key}`), description: s.at ? time(s.at) : undefined }))}
          current={current}
        />
        <Fields
          label={t("admin.nav.withdrawals")}
          items={[
            { label: t("admin.common.user"), value: <UserCell id={w.user_id} /> },
            {
              label: t("admin.common.network"),
              value: (
                <span className="inline-flex flex-wrap items-center gap-1.5">
                  {w.network}
                  {w.internal ? <Badge tone="info">{t("admin.withdrawals.internal")}</Badge> : w.custody ? <Badge tone="neutral">{t("admin.withdrawals.custody")}</Badge> : null}
                </span>
              ),
            },
            ...(w.custody && w.provider_status
              ? [{ label: t("admin.withdrawals.providerStatus"), value: <EnumBadge group="providerStatus" code={w.provider_status} /> }]
              : []),
            { label: t("admin.withdrawals.address"), value: <KeyTag>{w.address}</KeyTag>, copy: w.address },
            { label: t("admin.withdrawalDetail.addressBook"), value: <AddressBook detail={detail} w={w} /> },
            { label: t("admin.withdrawals.fee"), value: <Num value={w.fee} unit={w.asset} /> },
            { label: t("admin.withdrawals.value"), value: <Num value={w.value_usdt} decimals={2} unit="USDT" /> },
            {
              label: t("admin.withdrawalDetail.used"),
              hint: t("admin.withdrawalDetail.usedHint"),
              value: detail.data ? (
                t("admin.withdrawalDetail.usedValue", {
                  today: formatDecimal(detail.data.used_today_usdt, { decimals: 2 }),
                  month: formatDecimal(detail.data.used_month_usdt, { decimals: 2 }),
                })
              ) : detail.isPending ? (
                <Skeleton className="h-4 w-40" />
              ) : (
                "—"
              ),
            },
            ...(held
              ? [
                  {
                    label: t("admin.hold.note"),
                    value: (
                      <span className="flex flex-col gap-0.5">
                        <span>{w.hold_note}</span>
                        <span className="text-xs text-fg-3">{t("admin.hold.by", { by: w.held_by, time: time(w.held_at!) })}</span>
                      </span>
                    ),
                  },
                ]
              : []),
            {
              label: t("admin.withdrawals.risk"),
              value: (
                <span className="inline-flex flex-wrap gap-1">
                  <Badge tone={(w.risk_score ?? 0) >= 50 ? "danger" : "neutral"}>{w.risk_score ?? 0}</Badge>
                  {w.risk_reasons.map((r) => (
                    <Badge key={r} tone="warn" title={r}>
                      {label("riskReason", r)}
                    </Badge>
                  ))}
                </span>
              ),
            },
            {
              label: t("admin.withdrawals.approvals"),
              value: (
                <span className="inline-flex flex-wrap items-baseline gap-x-2">
                  <span className="tabular-nums">{`${w.approvals?.length ?? 0}/${w.approvals_required}`}</span>
                  {w.approvals?.length ? <span className="text-fg-3">{w.approvals.join(t("admin.summary.sep"))}</span> : null}
                </span>
              ),
            },
            ...(w.tx_hash ? [{ label: t("admin.withdrawals.txHash"), value: <KeyTag>{w.tx_hash}</KeyTag>, copy: w.tx_hash }] : []),
            ...(w.reject_reason ? [{ label: t("admin.withdrawals.rejectReason"), value: w.reject_reason }] : []),
          ]}
        />
        {reviewable && (
          <div className="flex gap-2">
            <DangerAction
              trigger={(open) => <Button onClick={open}>{t("admin.withdrawals.approve")}</Button>}
              danger={false}
              title={t("admin.withdrawals.approveTitle")}
              target={target}
              confirmWord={lastFour(w.id)}
              run={review(true)}
              success={(r) =>
                // Approved all the same, a suspended asset's withdrawal waits for the lift (C5.5 ⑯).
                (r as { suspended_at?: string } | null)?.suspended_at
                  ? { info: t("admin.suspensions.approvedWaiting", { asset: w.asset }) }
                  : t("admin.withdrawals.approved")
              }
              invalidate={[["admin", "withdrawals"], ["admin", "todo"]]}
              onDone={onClose}
            >
              {suspension && <p className="text-sm text-danger-strong">{t("admin.suspensions.reviewNote", { asset: w.asset })}</p>}
              {alone ? (
                w.approvals_required > 1 && <p className="text-sm text-info-strong">{t("admin.withdrawals.alone", { max: settings?.withdrawal_max_usdt })}</p>
              ) : (
                (w.approvals?.length ?? 0) + 1 < w.approvals_required && (
                  <p className="text-sm text-fg-3">{t("admin.withdrawals.secondReviewer", { n: (w.approvals?.length ?? 0) + 1, required: w.approvals_required })}</p>
                )
              )}
            </DangerAction>
            <DangerAction
              trigger={(open) => (
                <Button variant="danger" onClick={open}>
                  {t("admin.withdrawals.reject")}
                </Button>
              )}
              title={t("admin.withdrawals.rejectTitle")}
              target={target}
              confirmWord={lastFour(w.id)}
              run={review(false)}
              success={t("admin.withdrawals.rejected")}
              invalidate={[["admin", "withdrawals"], ["admin", "todo"]]}
              onDone={onClose}
            />
            <DangerAction
              trigger={(open) => (
                <Button variant="secondary" icon={held ? <CirclePlay size={16} /> : <CirclePause size={16} />} onClick={open}>
                  {held ? t("admin.hold.unhold") : t("admin.hold.hold")}
                </Button>
              )}
              danger={false}
              title={held ? t("admin.hold.unholdTitle") : t("admin.hold.holdTitle")}
              description={held ? undefined : t("admin.hold.holdHint")}
              target={target}
              confirmWord={lastFour(w.id)}
              run={hold(!held)}
              success={held ? t("admin.hold.unholdDone") : t("admin.hold.holdDone")}
              invalidate={[["admin", "withdrawals"]]}
            />
          </div>
        )}
        {w.user_id && <RecentOfUser userId={w.user_id} exclude={w.id} />}
        {w.custody && <Callbacks withdrawalId={w.id} />}
      </div>
    </Drawer>
  );
}

type Detail = UseQueryResult<AdminSchemas["WithdrawalDetail"]>;

/** AddressBook is the withdrawal's address in the user's address book: when added (new or not), whether it still cools off. */
function AddressBook({ detail, w }: { detail: Detail; w: Withdrawal }) {
  const { t } = useTranslation();
  const time = useTimeText();
  if (detail.isPending) return <Skeleton className="h-4 w-40" />;
  if (detail.isError) return <span className="text-danger-strong">{errorText(detail.error)}</span>;
  const book = detail.data.address_book;
  if (!book) return <span className="text-fg-3">{t("admin.withdrawalDetail.addressGone")}</span>;
  const fresh = Date.parse(w.created_at) - Date.parse(book.created_at) < NEW_ADDRESS_MS;
  const cooling = Date.parse(book.usable_at) > Date.now();
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5">
      {book.label && <span>{book.label}</span>}
      <span className="text-xs text-fg-3">{t("admin.withdrawalDetail.addedAt", { time: time(book.created_at) })}</span>
      {fresh && (
        <Badge tone="warn" title={t("admin.withdrawalDetail.newAddressHint")}>
          {t("admin.withdrawalDetail.newAddress")}
        </Badge>
      )}
      {cooling && <Badge tone="warn">{t("admin.withdrawalDetail.cooling", { time: time(book.usable_at) })}</Badge>}
    </span>
  );
}

/** RecentOfUser lists the user's other recent withdrawals (a pattern is easier to see). */
function RecentOfUser({ userId, exclude }: { userId: string; exclude: string }) {
  const { t } = useTranslation();
  const time = useTimeText();
  const q = useQuery({
    queryKey: ["admin", "withdrawals", "recent", userId],
    queryFn: async () =>
      adminData(await adminApi.GET("/admin/v1/withdrawals", { params: { query: { user_id: userId, status: "ALL", order: "desc", limit: 6 } } })).items,
  });
  const rows = (q.data ?? []).filter((x) => x.id !== exclude).slice(0, 5);
  return (
    <section>
      <h3 className="mb-2 text-sm font-semibold">{t("admin.withdrawalDetail.recent")}</h3>
      {q.isPending ? (
        <Skeleton className="h-16 w-full" />
      ) : rows.length === 0 ? (
        <p className="text-sm text-fg-3">{t("admin.withdrawalDetail.noRecent")}</p>
      ) : (
        <ul className="flex flex-col divide-y divide-line-1 text-sm">
          {rows.map((x) => (
            <li key={x.id} className="flex items-center gap-3 py-1.5">
              <span className="w-36 shrink-0 text-xs tabular-nums text-fg-3">{time(x.created_at)}</span>
              <Num value={x.amount} unit={x.asset} className="flex-1" />
              <span className="text-xs text-fg-3">{x.network}</span>
              <EnumBadge group="withdrawalStatus" code={x.status} />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/** Callbacks is the custodian's word on a withdrawal, oldest first. */
function Callbacks({ withdrawalId }: { withdrawalId: string }) {
  const { t } = useTranslation();
  const time = useTimeText();
  const q = useQuery({
    queryKey: ["admin", "custody", "callbacks", withdrawalId],
    queryFn: async () =>
      adminData(await adminApi.GET("/admin/v1/custody/callbacks", { params: { query: { q: withdrawalId, kind: "WITHDRAWAL", limit: 20 } } })).items,
  });
  const rows = [...(q.data ?? [])].reverse();
  return (
    <section>
      <h3 className="mb-2 text-sm font-semibold">{t("admin.withdrawalDetail.callbacks")}</h3>
      {q.isPending ? (
        <Skeleton className="h-12 w-full" />
      ) : rows.length === 0 ? (
        <p className="text-sm text-fg-3">{t("admin.withdrawalDetail.noCallbacks")}</p>
      ) : (
        <ol className="flex flex-col gap-2 border-l border-line-1 pl-4 text-sm">
          {rows.map((c) => (
            <li key={c.id} className="flex flex-wrap items-center gap-2">
              <span className="text-xs tabular-nums text-fg-3">{time(c.received_at)}</span>
              {c.status !== null && <EnumBadge group="custodyStatus" code={String(c.status)} />}
              <EnumBadge group="callbackResult" code={c.result} />
              {c.detail && <span className="text-xs text-fg-3">{c.detail}</span>}
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
