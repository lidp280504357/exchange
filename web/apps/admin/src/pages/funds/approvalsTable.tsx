import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { Badge, Button, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { lastFour } from "../../kit/actions";
import { EnumBadge, EnumText } from "../../kit/enums";
import { Num, TimeText, UserCell } from "../../kit/format";
import { FundAction, type Approval } from "../../kit/funds";
import { ListTable, pageSize, RowActions, useCursorList, type CursorList } from "../../kit/lists";
import { MintShares, pct, SimRequestNow, useEventText, type SimEvent } from "../sim/common";

const right: DataColumnMeta = { align: "right" };

/** useApprovals pages through the fund operations in a status ("" for all). */
export function useApprovals(status: string) {
  return useCursorList<Approval>(["admin", "approvals", status], async (cursor) =>
    adminData(
      await adminApi.GET("/admin/v1/approvals", {
        params: { query: { status: (status || undefined) as Approval["status"] | undefined, cursor, limit: pageSize() } },
      }),
    ),
  );
}

/** ApprovalsTable lists fund operations with what the administrator may do with each. */
export function ApprovalsTable({ admin, list }: { admin: Admin; list: CursorList<Approval> }) {
  const { t } = useTranslation();
  const columns = useMemo<ColumnDef<Approval, unknown>[]>(
    () => [
      { id: "time", header: t("admin.common.createdAt"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
      { id: "kind", header: t("admin.ledger.kind"), cell: ({ row }) => <EnumText group="approvalKind" code={row.original.kind} /> },
      { id: "payload", header: t("admin.ledger.payload"), cell: ({ row }) => <Payload a={row.original} /> },
      {
        id: "value",
        header: t("admin.funds.value"),
        meta: right,
        cell: ({ row }) => (row.original.value_usdt ? <Num value={row.original.value_usdt} decimals={2} /> : <span className="text-fg-3">—</span>),
      },
      { id: "mode", header: t("admin.funds.mode"), cell: ({ row }) => <Mode a={row.original} /> },
      {
        accessorKey: "reason",
        header: t("admin.common.reason"),
        cell: ({ row }) => (
          <span className="block max-w-56 truncate" title={row.original.reason}>
            {row.original.reason}
          </span>
        ),
      },
      {
        id: "status",
        header: t("admin.common.status"),
        cell: ({ row }) => (
          <span className="inline-flex flex-col items-start gap-0.5">
            <EnumBadge group="approvalStatus" code={row.original.status} />
            {attempted(row.original) && (
              <Badge tone="warn" title={t("admin.attempts.hint")}>
                {t("admin.attempts.badge")}
              </Badge>
            )}
          </span>
        ),
      },
      {
        id: "people",
        header: t("admin.funds.people"),
        cell: ({ row }) => (
          <span className="flex flex-col text-xs">
            <span title={t("admin.ledger.requestedBy")}>{row.original.requested_by_email || row.original.requested_by.slice(0, 8)}</span>
            {row.original.decided_by_email && row.original.decided_by !== row.original.requested_by && (
              <span className="text-fg-3" title={t("admin.ledger.decidedBy")}>
                → {row.original.decided_by_email}
              </span>
            )}
          </span>
        ),
      },
      {
        id: "actions",
        header: "",
        cell: ({ row }) => {
          const a = row.original;
          const note = a.result ? (
            <span className="block max-w-48 truncate text-xs text-fg-3" title={a.result}>
              {a.result}
            </span>
          ) : null;
          if (a.status !== "PENDING") return note;
          // A pending operation's result is how its unfinished attempt ended (C5.5 ⑥).
          return (
            <span className="flex flex-col items-end gap-1">
              <Decide admin={admin} a={a} />
              {note}
            </span>
          );
        },
      },
    ],
    [t, admin],
  );
  return <ListTable list={list} columns={columns} getRowId={(a) => a.id} aria-label="fund operations" />;
}

/** simKind reports whether a request is a simulated market's change (C5). */
const simKind = (kind: string) => kind === "SIM_EVENT" || kind === "SIM_PARAMS";

/** attempted reports whether a pending operation's attempt did not finish: it may have booked, so it is finished, never rejected (C5.5 ⑥). */
const attempted = (a: Approval) => a.status === "PENDING" && !!a.attempted_at;

/** A requested spike of a threshold target (A6), as the request keeps it. */
type RequestedSpike = { at: string; size: number; width_seconds?: number };

/**
 * SimChange says what a simulated market's request changes and how far
 * market-sim measured it moving the price; full lists a target's spikes
 * for its decider and says where a spike's liquidations are measured.
 */
function SimChange({ a, full }: { a: Approval; full?: boolean }) {
  const { t } = useTranslation();
  const eventText = useEventText();
  const p = a.payload as Record<string, string>;
  let what = t("admin.sim.settingsChange");
  let spikes: RequestedSpike[] = [];
  let spike = false;
  if (a.kind === "SIM_EVENT") {
    try {
      const e = JSON.parse(p.change ?? "{}") as Partial<Omit<SimEvent, "spikes">> & { spikes?: RequestedSpike[] };
      what = eventText({ type: e.type ?? "JUMP", size: e.size ?? 0, price: e.price ?? null, mu: e.mu ?? 0, factor: e.factor ?? 0,
        duration_seconds: e.duration_seconds ?? 0, hold_seconds: e.hold_seconds ?? 0, direction: e.direction, then: e.then,
        width_seconds: e.width_seconds, spikes: e.spikes });
      spikes = e.spikes ?? [];
      spike = e.type === "SPIKE";
    } catch {
      what = p.change ?? "";
    }
  }
  return (
    <span className="flex flex-col">
      <span>{what}</span>
      {p.move && <span className="text-xs text-fg-3">{t("admin.sim.measuredMove", { move: pct(Number(p.move), 1) })}</span>}
      {full &&
        spikes.map((s, i) => (
          <span key={i} className="text-xs text-fg-2">
            #{i + 1} <TimeText value={s.at} style="datetimeSeconds" /> · {pct(s.size, 1)} · {t("admin.simTarget.wide", { s: s.width_seconds || 20 })}
          </span>
        ))}
      {full && spike && <span className="text-xs text-fg-3">{t("admin.simTarget.markNoteRequest")}</span>}
    </span>
  );
}

/**
 * WelcomeChange says what a WELCOME_CREDIT request sets the welcome
 * credits to and from, and what its raise is worth (design 2026-10-04
 * §4.2).
 */
function WelcomeChange({ a }: { a: Approval }) {
  const { t } = useTranslation();
  const p = a.payload as Record<string, string>;
  const list = (raw?: string) => {
    try {
      const credits = JSON.parse(raw ?? "[]") as { asset: string; amount: string }[];
      return credits.length ? credits.map((c) => `${c.amount} ${c.asset}`).join(" · ") : t("admin.launch.nothing");
    } catch {
      return raw ?? "";
    }
  };
  return (
    <span className="flex flex-col" data-testid="welcome-change">
      <span className="font-mono text-xs">
        {list(p.previous)} → {list(p.credits)}
      </span>
      <span className="text-xs text-fg-3">{t("admin.platform.raiseWorth", { usdt: p.raise_usdt })}</span>
    </span>
  );
}

function Payload({ a }: { a: Approval }) {
  const { t } = useTranslation();
  const p = a.payload as Record<string, string>;
  if (simKind(a.kind)) return <SimChange a={a} />;
  if (a.kind === "SIM_MINT") return <MintShares payload={p} />;
  if (a.kind === "WELCOME_CREDIT") return <WelcomeChange a={a} />;
  return (
    <span className="inline-flex items-center gap-2">
      {p.user_id && <UserCell id={p.user_id} />}
      <Num value={p.amount} unit={p.asset} signed />
      {p.reference && <span className="text-xs text-fg-3">#{p.reference}</span>}
      {p.trade_id && (
        <span className="font-mono text-xs text-fg-3" title={p.tx_hash}>
          {p.network} · {p.trade_id}
        </span>
      )}
      {a.kind === "DEPOSIT_ASSIGN" && p.deposit_id && (
        <span className="font-mono text-xs text-fg-3" title={p.tx_hash}>
          {p.network} · {t("admin.unowned.deposit", { id: p.deposit_id.slice(-8) })}
        </span>
      )}
      {a.kind === "DEPOSIT_ASSIGN" && <Holder p={p} />}
    </span>
  );
}

/** Holder names a deposit of nobody's address holder when it is credited to someone else (C5.5 ㉑). */
function Holder({ p }: { p: Record<string, string> }) {
  const { t } = useTranslation();
  const holder = p.former_holder || p.address_owner;
  if (!holder || holder === p.user_id) return null;
  return (
    <span className="inline-flex items-center gap-1 text-xs text-warn-strong" data-testid="approval-holder">
      {t(p.former_holder ? "admin.unowned.ownerRetired" : "admin.unowned.owner")}
      <UserCell id={holder} />
    </span>
  );
}

/** Mode says who carries it out: its requester alone, or a second administrator (and why); a lapsed request says so. */
export function Mode({ a }: { a: Approval }) {
  const { t } = useTranslation();
  if (a.mode === "SINGLE") return <Badge tone="info">{t("admin.funds.single")}</Badge>;
  return (
    <span className="inline-flex flex-col gap-0.5">
      <Badge tone="neutral">{t("admin.funds.twoPerson")}</Badge>
      {a.escalation && <span className="text-xs text-fg-3">{t(`admin.funds.escalationShort.${a.escalation}`)}</span>}
      {a.status === "PENDING" && lapsed(a) && <Badge tone="warn">{t("admin.sim.lapsedShort")}</Badge>}
    </span>
  );
}

/**
 * lapsed reports whether a request that lapses did: a simulated market's
 * (a day after it was asked for, or when its event was to start, C5.5 ④)
 * or a welcome credits raise (a day after, review ㉚), as the server judged
 * it by its clock when it listed it (review ⑭).
 */
function lapsed(a: Approval): boolean {
  return (simKind(a.kind) || a.kind === "WELCOME_CREDIT") && a.expired === true;
}

/**
 * Decide offers what the administrator may do with a pending operation:
 * approve or reject another's request; finish their own single-person
 * operation whose outcome was unknown; withdraw their own request. One
 * whose attempt did not finish is only finished (C5.5 ⑥).
 */
function Decide({ admin, a }: { admin: Admin; a: Approval }) {
  const { t } = useTranslation();
  if (!can(admin, simKind(a.kind) ? "sim.control" : a.kind === "WELCOME_CREDIT" ? "settings.write" : "ledger.adjust.approve")) return null;
  const p = a.payload as Record<string, string>;
  const mine = a.requested_by === admin.id;
  const finish = mine || attempted(a);
  const target = (
    <span className="inline-flex items-center gap-2">
      <EnumText group="approvalKind" code={a.kind} />{" "}
      {simKind(a.kind) ? (
        <SimChange a={a} full />
      ) : a.kind === "SIM_MINT" ? (
        <MintShares payload={p} full />
      ) : a.kind === "WELCOME_CREDIT" ? (
        <WelcomeChange a={a} />
      ) : (
        <Num value={p.amount} unit={p.asset} signed />
      )}
    </span>
  );
  const run = (approve: boolean) => async (reason: string) =>
    adminData(await adminApi.POST("/admin/v1/approvals/{id}/decide", { params: { path: { id: a.id } }, body: { approve, reason } }));
  const approve = !mine || a.mode === "SINGLE";
  return (
    <RowActions className="flex justify-end gap-1">
      {approve && (
        <FundAction
          trigger={(open) => (
            <Button size="sm" onClick={open}>
              {finish ? t("admin.funds.finish") : t("admin.ledger.approve")}
            </Button>
          )}
          danger={false}
          title={finish ? t("admin.funds.finishTitle") : t("admin.ledger.approveTitle")}
          description={attempted(a) ? t("admin.attempts.finishHelp") : undefined}
          target={target}
          confirmWord={lastFour(a.id)}
          run={run(true)}
        >
          {simKind(a.kind) && <SimRequestNow id={a.id} />}
        </FundAction>
      )}
      {!attempted(a) && (
        <FundAction
          trigger={(open) => (
            <Button size="sm" variant="ghost" onClick={open}>
              {mine ? t("admin.funds.withdraw") : t("admin.ledger.reject")}
            </Button>
          )}
          title={mine ? t("admin.funds.withdrawTitle") : t("admin.ledger.rejectTitle")}
          target={target}
          confirmWord={lastFour(a.id)}
          run={run(false)}
        />
      )}
    </RowActions>
  );
}
