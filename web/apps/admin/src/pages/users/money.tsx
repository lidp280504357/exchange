import { dec, errorText, i18n } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, DataTable, ErrorState, Input, Select, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { Lock } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, FormError, lastFour, StillOpen, type Notice } from "../../kit/actions";
import { EnumBadge, EnumText } from "../../kit/enums";
import { IdText, Num, TimeText } from "../../kit/format";
import type { Approval } from "../../kit/funds";
import { Card } from "../../kit/Page";
import { AdjustForm, Outcome } from "../funds/Adjustments";
import { OrdersTable, useOrders, type Order } from "../records/tables";

type Balance = AdminSchemas["ValuedBalance"];
type Hold = AdminSchemas["Hold"];
type ContractOrder = AdminSchemas["ContractOrder"];
type Position = AdminSchemas["UserPosition"];

const right: DataColumnMeta = { align: "right" };

const userKey = (userId: string, part: string) => ["admin", "user", userId, part];

/**
 * BalancesTab is a user's money (design 2026-10-02 §4.1): every SPOT and
 * FUTURES balance valued in USDT, the holds on it with a hold placed or
 * released right there, and an adjustment of either account.
 */
export function BalancesTab({ admin, userId }: { admin: Admin; userId: string }) {
  const { t } = useTranslation();
  const [last, setLast] = useState<Approval | null>(null);
  const view = useQuery({
    queryKey: userKey(userId, "balances"),
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/users/{id}/balances", { params: { path: { id: userId } } })),
  });
  const columns = useMemo<ColumnDef<Balance, unknown>[]>(
    () => [
      { id: "account", header: t("admin.users.account"), cell: ({ row }) => <EnumText group="accountType" code={row.original.account_type} /> },
      { accessorKey: "asset", header: t("admin.common.asset") },
      { id: "available", header: t("admin.users.available"), meta: right, cell: ({ row }) => <Num value={row.original.available} /> },
      { id: "frozen", header: t("admin.users.frozen"), meta: right, cell: ({ row }) => <Num value={row.original.frozen} /> },
      { id: "total", header: t("admin.common.total"), meta: right, cell: ({ row }) => <Num value={row.original.total} /> },
      {
        id: "value", header: t("admin.money.valueUsdt"), meta: right,
        cell: ({ row }) => (row.original.value_usdt === null ? <span className="text-fg-3">—</span> : <Num value={row.original.value_usdt} decimals={2} />),
      },
    ],
    [t],
  );
  const v = view.data;
  return (
    <div className="flex flex-col gap-5">
      {view.isError ? (
        <ErrorState message={errorText(view.error)} onRetry={() => void view.refetch()} />
      ) : (
        <>
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <span className="text-sm text-fg-3">{t("admin.money.totalValue")}</span>
            <span className="text-xl font-semibold">{v ? <Num value={v.total_usdt} decimals={2} unit="USDT" /> : "…"}</span>
            {v && v.unpriced.length > 0 && <span className="text-xs text-fg-3">{t("admin.money.unpriced", { assets: v.unpriced.join(", ") })}</span>}
          </div>
          <DataTable
            columns={columns}
            data={v?.balances ?? []}
            getRowId={(b) => `${b.account_type}:${b.asset}`}
            loading={view.isPending}
            density="compact"
            empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.user.balancesEmpty")}</p>}
          />
        </>
      )}
      <Holds admin={admin} userId={userId} balances={v?.balances ?? []} />
      {can(admin, "ledger.adjust.request") && (
        <div className="grid gap-4 lg:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
          <Card title={t("admin.user.adjust")}>
            <AdjustForm
              userId={userId}
              onDone={(a) => {
                setLast(a);
                void view.refetch();
              }}
            />
          </Card>
          <Card title={t("admin.funds.outcome")}>
            {last ? <Outcome a={last} /> : <p className="py-6 text-center text-sm text-fg-3">{t("admin.funds.noOutcome")}</p>}
          </Card>
        </div>
      )}
    </div>
  );
}

const holdKeys = (userId: string) => [userKey(userId, "balances"), userKey(userId, "holds"), ["admin", "user", userId], ["admin", "audit"]];

/**
 * forceClose closes a position at the market. The console waits a few
 * seconds for the order to finish; one still filling keeps the dialog open
 * with its key, so confirming again looks its outcome up (C5.5 ⑯).
 */
export async function forceClose(userId: string, p: { symbol: string; position_side: "BOTH" | "LONG" | "SHORT" }, reason: string, key: string) {
  const o = adminData(
    await adminApi.POST("/admin/v1/users/{id}/positions/close", {
      params: { path: { id: userId }, header: { "Idempotency-Key": key } },
      body: { symbol: p.symbol, position_side: p.position_side, reason },
    }),
  );
  if (!["FILLED", "CANCELED", "EXPIRED", "REJECTED"].includes(o.status)) throw new StillOpen(i18n.t("admin.money.closeOpen"));
  return o;
}

/** closeOutcome says what came of a force close: filled in full, in part (the rest of the position stays, C5.5 ⑧), or placed. */
export function closeOutcome(result: unknown): string | Notice {
  const o = (result ?? {}) as { status?: string; quantity?: string; filled_quantity?: string };
  if (o.status === "FILLED") return i18n.t("admin.money.closedAll");
  if ((o.status === "CANCELED" || o.status === "EXPIRED" || o.status === "REJECTED") && o.quantity && o.filled_quantity !== undefined)
    return { info: i18n.t("admin.money.closedPart", { filled: o.filled_quantity, quantity: o.quantity }) };
  return i18n.t("admin.money.closed");
}

/** Holds lists the holds on the user's SPOT balance; ledger.hold places and releases them. */
function Holds({ admin, userId, balances }: { admin: Admin; userId: string; balances: Balance[] }) {
  const { t } = useTranslation();
  const act = can(admin, "ledger.hold");
  const [releasing, setReleasing] = useState<Hold | null>(null);
  const list = useQuery({
    queryKey: userKey(userId, "holds"),
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/users/{id}/holds", { params: { path: { id: userId } } })).holds,
  });
  const columns = useMemo<ColumnDef<Hold, unknown>[]>(
    () => [
      { id: "created", header: t("admin.common.createdAt"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
      { id: "amount", header: t("admin.common.amount"), meta: right, cell: ({ row }) => <Num value={row.original.amount} unit={row.original.asset} /> },
      { accessorKey: "reason", header: t("admin.common.reason"), cell: ({ row }) => <span className="line-clamp-2 max-w-64 text-xs">{row.original.reason}</span> },
      { accessorKey: "actor", header: t("admin.money.holdBy") },
      {
        id: "state", header: t("admin.common.status"),
        cell: ({ row }) =>
          row.original.active ? (
            <Badge tone="warn">{t("admin.money.holdActive")}</Badge>
          ) : (
            <span className="flex flex-col text-xs" title={row.original.release_reason}>
              <Badge tone="neutral">{t("admin.money.holdReleased")}</Badge>
              <span className="mt-0.5 text-fg-3">
                {row.original.released_by} · <TimeText value={row.original.released_at} style="datetime" />
              </span>
            </span>
          ),
      },
      ...(act
        ? [
            {
              id: "act", header: "", meta: right,
              cell: ({ row }: { row: { original: Hold } }) =>
                row.original.active ? (
                  <Button size="sm" variant="secondary" onClick={() => setReleasing(row.original)}>
                    {t("admin.money.release")}
                  </Button>
                ) : null,
            },
          ]
        : []),
    ],
    [t, act],
  );
  return (
    <Card title={t("admin.money.holds")} extra={act ? <PlaceHold userId={userId} balances={balances} /> : undefined}>
      {list.isError ? (
        <ErrorState message={errorText(list.error)} onRetry={() => void list.refetch()} />
      ) : (
        <DataTable
          columns={columns}
          data={list.data ?? []}
          getRowId={(h) => h.id}
          loading={list.isPending}
          density="compact"
          empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.money.noHolds")}</p>}
        />
      )}
      {releasing && (
        <DangerAction
          open
          onOpenChange={(o) => !o && setReleasing(null)}
          danger={false}
          title={t("admin.money.releaseTitle")}
          target={<Num value={releasing.amount} unit={releasing.asset} />}
          confirmWord={lastFour(releasing.id)}
          run={async (reason, key) =>
            adminData(
              await adminApi.DELETE("/admin/v1/users/{id}/holds/{hold}", {
                params: { path: { id: userId, hold: releasing.id }, header: { "Idempotency-Key": key } },
                body: { reason },
              }),
            )
          }
          success={t("admin.money.released")}
          invalidate={holdKeys(userId)}
        />
      )}
    </Card>
  );
}

/** PlaceHold freezes an amount of one of the user's SPOT assets. */
function PlaceHold({ userId, balances }: { userId: string; balances: Balance[] }) {
  const { t } = useTranslation();
  const spot = balances.filter((b) => b.account_type === "SPOT" && dec.gt(b.available, "0"));
  const [asset, setAsset] = useState("");
  const [amount, setAmount] = useState("");
  const chosen = spot.find((b) => b.asset === asset) ?? spot[0];
  const a = amount.trim();
  const ok = dec.isDecimal(a) && dec.gt(a, "0") && !!chosen && !dec.gt(a, chosen.available);
  return (
    <DangerAction
      trigger={(open) => (
        <Button size="sm" variant="secondary" icon={<Lock size={14} />} onClick={open} disabled={spot.length === 0}>
          {t("admin.money.placeHold")}
        </Button>
      )}
      title={t("admin.money.placeHoldTitle")}
      description={t("admin.money.placeHoldHelp")}
      target={<span className="font-mono text-xs">{userId}</span>}
      confirmWord={lastFour(userId)}
      run={async (reason, key) => {
        if (!ok || !chosen) throw new FormError(t("admin.money.badHold"));
        return adminData(
          await adminApi.POST("/admin/v1/users/{id}/holds", {
            params: { path: { id: userId }, header: { "Idempotency-Key": key } },
            body: { asset: chosen.asset, amount: a, reason },
          }),
        );
      }}
      success={t("admin.money.held")}
      invalidate={holdKeys(userId)}
      onDone={() => setAmount("")}
    >
      <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)] gap-3">
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.common.asset")}
          <Select size="sm" value={chosen?.asset ?? ""} onValueChange={setAsset} options={spot.map((b) => ({ value: b.asset, label: b.asset }))} />
        </label>
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.common.amount")}
          <Input
            value={amount}
            onValueChange={setAmount}
            inputMode="decimal"
            unit={chosen?.asset}
            error={amount !== "" && !ok ? t("admin.money.badHold") : undefined}
            suffix={
              chosen ? (
                <button type="button" className="mr-2 text-xs text-brand hover:underline" onClick={() => setAmount(chosen.available)}>
                  {t("admin.money.all")}
                </button>
              ) : undefined
            }
          />
        </label>
      </div>
      {chosen && <p className="text-xs text-fg-3">{t("admin.money.availableNow", { amount: chosen.available, asset: chosen.asset })}</p>}
    </DangerAction>
  );
}

const ACTIVE = ["NEW", "OPEN", "PARTIALLY_FILLED"];

/** OrdersTab is a user's spot orders and active contract orders; orders.cancel cancels one. */
export function OrdersTab({ admin, userId }: { admin: Admin; userId: string }) {
  const { t } = useTranslation();
  const act = can(admin, "orders.cancel");
  const [canceling, setCanceling] = useState<{ kind: "spot" | "contract"; id: string; label: string } | null>(null);
  const spot = useOrders({ user_id: userId });
  const contracts = useQuery({
    queryKey: userKey(userId, "contract-orders"),
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/users/{id}/contract-orders", { params: { path: { id: userId } } })).items,
    refetchInterval: 10_000,
  });
  const contractColumns = useMemo<ColumnDef<ContractOrder, unknown>[]>(
    () => [
      { id: "created", header: t("admin.common.createdAt"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
      { id: "id", header: t("admin.orders.orderId"), cell: ({ row }) => <IdText value={row.original.order_id} /> },
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      {
        id: "side", header: t("admin.common.side"),
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-1">
            <EnumBadge group="side" code={row.original.side} />
            {row.original.position_side !== "BOTH" && <span className="text-xs text-fg-3">{row.original.position_side}</span>}
            {row.original.reduce_only && <Badge tone="neutral">{t("admin.money.reduceOnly")}</Badge>}
          </span>
        ),
      },
      { id: "type", header: t("admin.orders.type"), cell: ({ row }) => <EnumText group="orderType" code={row.original.type} /> },
      { id: "price", header: t("admin.common.price"), meta: right, cell: ({ row }) => <Num value={row.original.price} /> },
      { id: "qty", header: t("admin.common.quantity"), meta: right, cell: ({ row }) => <Num value={row.original.quantity} /> },
      { id: "filled", header: t("admin.orders.filled"), meta: right, cell: ({ row }) => <Num value={row.original.filled_quantity} /> },
      {
        id: "status", header: t("admin.common.status"),
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-1">
            <EnumBadge group="orderStatus" code={row.original.status} />
            {row.original.cancel_requested && <span className="text-xs text-fg-3">{t("admin.money.cancelPending")}</span>}
          </span>
        ),
      },
      ...(act
        ? [
            {
              id: "act", header: "", meta: right,
              cell: ({ row }: { row: { original: ContractOrder } }) =>
                row.original.cancel_requested ? null : (
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => setCanceling({ kind: "contract", id: row.original.order_id, label: `${row.original.symbol} ${row.original.side}` })}
                  >
                    {t("admin.money.cancel")}
                  </Button>
                ),
            },
          ]
        : []),
    ],
    [t, act],
  );
  const spotAction = act
    ? (o: Order) =>
        ACTIVE.includes(o.status) ? (
          <Button
            size="sm"
            variant="secondary"
            onClick={(e) => {
              e.stopPropagation();
              setCanceling({ kind: "spot", id: o.order_id, label: `${o.symbol} ${o.side}` });
            }}
          >
            {t("admin.money.cancel")}
          </Button>
        ) : null
    : undefined;
  return (
    <div className="flex flex-col gap-5">
      <div>
        <h3 className="mb-2 text-sm font-semibold">{t("admin.money.spotOrders")}</h3>
        <OrdersTable list={spot} withUser={false} action={spotAction} />
      </div>
      <div>
        <h3 className="mb-2 text-sm font-semibold">{t("admin.money.contractOrders")}</h3>
        {contracts.isError ? (
          <ErrorState message={errorText(contracts.error)} onRetry={() => void contracts.refetch()} />
        ) : (
          <DataTable
            columns={contractColumns}
            data={contracts.data ?? []}
            getRowId={(o) => o.order_id}
            loading={contracts.isPending}
            density="compact"
            empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.money.noContractOrders")}</p>}
          />
        )}
      </div>
      {canceling && (
        <DangerAction
          open
          onOpenChange={(o) => !o && setCanceling(null)}
          danger={false}
          title={t("admin.money.cancelTitle")}
          description={t("admin.money.cancelHelp")}
          target={
            <span className="inline-flex flex-wrap items-center gap-2">
              <span>{canceling.label}</span>
              <span className="font-mono text-xs">{canceling.id}</span>
            </span>
          }
          confirmWord={lastFour(canceling.id)}
          run={async (reason) => {
            const path = { params: { path: { id: userId, order: canceling.id } }, body: { reason } };
            return canceling.kind === "spot"
              ? adminData(await adminApi.POST("/admin/v1/users/{id}/orders/{order}/cancel", path))
              : adminData(await adminApi.POST("/admin/v1/users/{id}/contract-orders/{order}/cancel", path));
          }}
          success={t("admin.money.canceled")}
          invalidate={[["admin", "orders"], userKey(userId, "contract-orders"), ["admin", "audit"]]}
        />
      )}
    </div>
  );
}

/** PositionsTab is a user's open contract positions; derivatives.write closes one at the market. */
export function PositionsTab({ admin, userId }: { admin: Admin; userId: string }) {
  const { t } = useTranslation();
  const act = can(admin, "derivatives.write");
  const [closing, setClosing] = useState<Position | null>(null);
  const positions = useQuery({
    queryKey: userKey(userId, "positions"),
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/users/{id}/positions", { params: { path: { id: userId } } })).positions,
    refetchInterval: 5_000,
  });
  const columns = useMemo<ColumnDef<Position, unknown>[]>(
    () => [
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      {
        id: "side", header: t("admin.money.direction"),
        cell: ({ row }) => {
          const long = dec.gt(row.original.quantity, "0");
          return (
            <span className="inline-flex items-center gap-1">
              <Badge tone={long ? "up" : "down"}>{long ? t("admin.money.long") : t("admin.money.short")}</Badge>
              {row.original.position_side !== "BOTH" && <span className="text-xs text-fg-3">{row.original.position_side}</span>}
            </span>
          );
        },
      },
      { id: "qty", header: t("admin.common.quantity"), meta: right, cell: ({ row }) => <Num value={dec.abs(row.original.quantity)} /> },
      { id: "entry", header: t("admin.money.entry"), meta: right, cell: ({ row }) => <Num value={row.original.entry_price} /> },
      { id: "mark", header: t("admin.money.mark"), meta: right, cell: ({ row }) => <Num value={row.original.mark_price} /> },
      {
        id: "liq", header: t("admin.money.liquidation"), meta: right,
        cell: ({ row }) => (row.original.liquidation_price ? <Num value={row.original.liquidation_price} className="text-warn" /> : <span className="text-fg-3">—</span>),
      },
      { id: "upnl", header: t("admin.money.upnl"), meta: right, cell: ({ row }) => <Num value={row.original.unrealized_pnl} signed /> },
      { id: "margin", header: t("admin.money.margin"), meta: right, cell: ({ row }) => <Num value={row.original.margin} /> },
      {
        id: "mode", header: t("admin.money.mode"),
        cell: ({ row }) => (
          <span className="whitespace-nowrap text-xs">
            {row.original.margin_mode === "CROSS" ? t("admin.money.cross") : t("admin.money.isolated")} · {row.original.leverage}x
          </span>
        ),
      },
      ...(act
        ? [
            {
              id: "act", header: "", meta: right,
              cell: ({ row }: { row: { original: Position } }) => (
                <Button size="sm" variant="danger" onClick={() => setClosing(row.original)}>
                  {t("admin.money.forceClose")}
                </Button>
              ),
            },
          ]
        : []),
    ],
    [t, act],
  );
  return (
    <div className="flex flex-col gap-4">
      {positions.isError ? (
        <ErrorState message={errorText(positions.error)} onRetry={() => void positions.refetch()} />
      ) : (
        <DataTable
          columns={columns}
          data={positions.data ?? []}
          getRowId={(p) => p.position_id}
          loading={positions.isPending}
          density="compact"
          empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.money.noPositions")}</p>}
        />
      )}
      {closing && (
        <DangerAction
          open
          onOpenChange={(o) => !o && setClosing(null)}
          title={t("admin.money.closeTitle")}
          description={t("admin.money.closeHelp")}
          target={
            <span className="inline-flex flex-wrap items-center gap-2">
              <span className="font-medium">{closing.symbol}</span>
              <Num value={closing.quantity} signed />
              {closing.position_side !== "BOTH" && <span className="text-xs text-fg-3">{closing.position_side}</span>}
            </span>
          }
          confirmWord={closing.symbol.split("-")[0] ?? closing.symbol}
          run={(reason, key) => forceClose(userId, closing, reason, key)}
          success={closeOutcome}
          invalidate={[userKey(userId, "positions"), userKey(userId, "contract-orders"), userKey(userId, "balances"), ["admin", "audit"]]}
        />
      )}
    </div>
  );
}
