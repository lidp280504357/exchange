import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Button, EmptyState, Input, Segmented, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, lastFour } from "../../kit/actions";
import { EnumBadge } from "../../kit/enums";
import { IdText, Num, TimeText } from "../../kit/format";
import { ListTable, pageSize, RowActions, useCursorList } from "../../kit/lists";

type Fee = AdminSchemas["CustodyFee"];
type Status = Fee["status"];

const right: DataColumnMeta = { align: "right" };
const STATUSES: Status[] = ["HELD", "BOOKABLE", "WRITTEN_OFF"];
const decimalRE = /^\d+(\.\d+)?$/;

/** held reports whether a fee waits for a person to book it or write it off. */
const held = (f: Fee) => f.status === "HELD";
/** waitsForGas reports whether a fee booked as it came still waits for GAS_SUPPLY: it may be written off. */
const waitsForGas = (f: Fee) => f.status === "BOOKABLE" && !f.journal_id;

/**
 * CustodyFees lists what a custodian charges on withdrawals (C6): those
 * held for a person first. An administrator who approves adjustments books
 * one from GAS_SUPPLY (as reported, or as charged within 5 times that) or
 * writes it off, as exchangectl wallet custody-fee does; wallet-service
 * audits each.
 */
export function CustodyFees({ admin, provider }: { admin: Admin; provider: "UDUN" | "UDUNMOCK" }) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<Status | "ALL">("HELD");
  const list = useCursorList<Fee>(["admin", "custody", "fees", provider, status], async (cursor) =>
    adminData(
      await adminApi.GET("/admin/v1/custody/fees", {
        params: { query: { provider, status: status === "ALL" ? undefined : status, cursor, limit: pageSize() } },
      }),
    ),
  );
  const decide = can(admin, "ledger.adjust.approve");
  const columns = useMemo<ColumnDef<Fee, unknown>[]>(
    () => [
      { id: "time", header: t("admin.common.createdAt"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
      { id: "withdrawal", header: t("admin.custodyFees.withdrawal"), cell: ({ row }) => <IdText value={row.original.withdrawal_id} /> },
      {
        id: "where",
        header: t("admin.common.network"),
        cell: ({ row }) => (
          <span className="flex flex-col text-xs">
            <span>{row.original.network}</span>
            {row.original.provider && <span className="text-fg-3">{row.original.provider}</span>}
          </span>
        ),
      },
      { id: "amount", header: t("admin.common.amount"), meta: right, cell: ({ row }) => <Num value={row.original.amount} unit={row.original.asset} /> },
      {
        id: "unit",
        header: t("admin.custodyFees.unit"),
        cell: ({ row }) =>
          row.original.unit ? <EnumBadge group="feeUnit" code={row.original.unit} /> : <span className="text-xs text-fg-3">{t("admin.custodyFees.unitNone")}</span>,
      },
      {
        id: "status",
        header: t("admin.common.status"),
        cell: ({ row }) => (
          <span className="inline-flex flex-col items-start gap-0.5">
            <EnumBadge group="feeStatus" code={row.original.status} />
            {waitsForGas(row.original) && <span className="text-xs text-warn-strong">{t("admin.custodyFees.waitingGas")}</span>}
            {row.original.journal_id && (
              <span className="text-xs text-fg-3" title={t("admin.custodyFees.journal")}>
                <IdText value={row.original.journal_id} />
              </span>
            )}
          </span>
        ),
      },
      {
        id: "why",
        header: t("admin.custodyFees.holdReason"),
        cell: ({ row }) => (
          <span className="block max-w-56 truncate text-xs" title={row.original.hold_reason}>
            {row.original.hold_reason || "—"}
          </span>
        ),
      },
      {
        id: "resolved",
        header: t("admin.custodyFees.resolved"),
        cell: ({ row }) =>
          row.original.resolved_by ? (
            <span className="flex max-w-56 flex-col text-xs">
              <span>{row.original.resolved_by}</span>
              <span className="truncate text-fg-3" title={row.original.resolution}>
                {row.original.resolution}
              </span>
            </span>
          ) : (
            <span className="text-fg-3">—</span>
          ),
      },
      ...(decide
        ? [
            {
              id: "actions",
              header: "",
              cell: ({ row }) =>
                held(row.original) || waitsForGas(row.original) ? (
                  <RowActions className="flex justify-end gap-1">
                    {held(row.original) && <BookFee f={row.original} />}
                    <WriteOffFee f={row.original} />
                  </RowActions>
                ) : null,
            } as ColumnDef<Fee, unknown>,
          ]
        : []),
    ],
    [t, decide],
  );
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm text-fg-3">{t("admin.custodyFees.help")}</p>
      <Segmented
        aria-label={t("admin.common.status")}
        value={status}
        onValueChange={(v) => setStatus(v as Status | "ALL")}
        items={[
          ...STATUSES.map((s) => ({ value: s, label: t(`admin.enum.feeStatus.${s}`) })),
          { value: "ALL", label: t("admin.custodyFees.all") },
        ]}
      />
      <ListTable
        list={list}
        columns={columns}
        getRowId={(f) => f.tx_hash}
        empty={<EmptyState compact title={status === "HELD" ? t("admin.custodyFees.emptyHeld") : t("admin.custodyFees.empty")} />}
        aria-label="custody fees"
      />
    </div>
  );
}

const invalidate = [["admin", "custody"]];

/**
 * useDecision runs a decision on a fee and reloads the fees whatever came
 * of it: a 409 means another decision (or a lost answer's first try) took
 * it already, and the row should show so (review ㉖).
 */
function useDecision() {
  const qc = useQueryClient();
  return async <T,>(call: () => Promise<T>): Promise<T> => {
    try {
      return await call();
    } finally {
      void qc.invalidateQueries({ queryKey: ["admin", "custody"] });
    }
  };
}

/** FeeTarget names a fee in its dialogs. */
function FeeTarget({ f }: { f: Fee }) {
  return (
    <span className="inline-flex flex-wrap items-center gap-2 text-sm">
      <Num value={f.amount} unit={f.asset} />
      <span className="text-fg-3">{f.network}</span>
      <span className="font-mono text-xs">{f.withdrawal_id}</span>
    </span>
  );
}

/** BookFee books a held fee from GAS_SUPPLY, as reported or in the asset and amount found charged. */
function BookFee({ f }: { f: Fee }) {
  const { t } = useTranslation();
  const decision = useDecision();
  const [asset, setAsset] = useState("");
  const [amount, setAmount] = useState("");
  const a = asset.trim().toUpperCase();
  const n = amount.trim();
  const amountOk = n === "" || (decimalRE.test(n) && Number(n) > 0);
  return (
    <DangerAction
      trigger={(open) => (
        <Button size="sm" onClick={open} data-testid="fee-book">
          {t("admin.custodyFees.book")}
        </Button>
      )}
      danger={false}
      title={t("admin.custodyFees.bookTitle")}
      description={t("admin.custodyFees.bookHint")}
      target={<FeeTarget f={f} />}
      confirmWord={lastFour(f.withdrawal_id)}
      disabled={!amountOk}
      run={(reason) =>
        decision(async () =>
          adminData(
            await adminApi.POST("/admin/v1/custody/fees/{withdrawal_id}/book", {
              params: { path: { withdrawal_id: f.withdrawal_id } },
              body: { reason, ...(a ? { asset: a } : {}), ...(n ? { amount: n } : {}) },
            }),
          ),
        )
      }
      success={t("admin.custodyFees.booked")}
      invalidate={invalidate}
      onDone={() => {
        setAsset("");
        setAmount("");
      }}
    >
      <div className="grid grid-cols-2 gap-3 text-sm text-fg-2">
        <label className="flex flex-col gap-1.5">
          {t("admin.custodyFees.asset")}
          <Input value={asset} onValueChange={setAsset} placeholder={t("admin.custodyFees.assetHint", { asset: f.asset })} autoComplete="off" />
        </label>
        <label className="flex flex-col gap-1.5">
          {t("admin.custodyFees.amount")}
          <Input
            value={amount}
            onValueChange={setAmount}
            placeholder={t("admin.custodyFees.amountHint", { amount: f.amount })}
            inputMode="decimal"
            autoComplete="off"
            error={amountOk ? undefined : t("admin.custodyFees.badAmount")}
          />
        </label>
      </div>
    </DangerAction>
  );
}

/** WriteOffFee writes off a held fee, or one still waiting for GAS_SUPPLY. */
function WriteOffFee({ f }: { f: Fee }) {
  const { t } = useTranslation();
  const decision = useDecision();
  return (
    <DangerAction
      trigger={(open) => (
        <Button size="sm" variant="secondary" onClick={open} data-testid="fee-write-off">
          {t("admin.custodyFees.writeOff")}
        </Button>
      )}
      title={t("admin.custodyFees.writeOffTitle")}
      description={t("admin.custodyFees.writeOffHint")}
      target={<FeeTarget f={f} />}
      confirmWord={lastFour(f.withdrawal_id)}
      run={(reason) =>
        decision(async () =>
          adminData(
            await adminApi.POST("/admin/v1/custody/fees/{withdrawal_id}/write-off", {
              params: { path: { withdrawal_id: f.withdrawal_id } },
              body: { reason },
            }),
          ),
        )
      }
      success={t("admin.custodyFees.writtenOff")}
      invalidate={invalidate}
    />
  );
}
