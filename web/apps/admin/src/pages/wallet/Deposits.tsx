import { Drawer, KeyValue } from "@exchange/ui";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { EnumBadge, useEnum } from "../../kit/enums";
import { FilterBar, options, useFilters } from "../../kit/filters";
import { Num, TimeText, UserCell } from "../../kit/format";
import { Page } from "../../kit/Page";
import { DepositsTable, useDeposits, type Deposit } from "../records/tables";

const KEYS = ["user_id", "asset", "network", "status", "tx_hash"] as const;

/** Deposits (design §10.3): by user, asset, network, status or transaction; a row shows its details. */
export default function Deposits() {
  const { t } = useTranslation();
  const label = useEnum();
  const filters = useFilters(KEYS);
  const f = filters.values;
  const list = useDeposits({ user_id: f.user_id, asset: f.asset?.toUpperCase(), network: f.network?.toUpperCase(), status: f.status, tx_hash: f.tx_hash });
  const [open, setOpen] = useState<Deposit | null>(null);
  return (
    <Page title={t("admin.nav.deposits")}>
      <FilterBar
        page="deposits"
        filters={filters}
        defs={[
          { key: "user_id", label: t("admin.orders.userFilter"), kind: "text" },
          { key: "asset", label: t("admin.common.asset"), kind: "text", placeholder: "ETH", width: 100 },
          { key: "network", label: t("admin.common.network"), kind: "text", placeholder: "ETH-SEPOLIA", width: 140 },
          {
            key: "status",
            label: t("admin.common.status"),
            kind: "select",
            options: options(t("admin.common.all"), ["DETECTED", "CONFIRMING", "CONFIRMED", "CREDITED", "ORPHANED", "REJECTED"], (c) => label("depositStatus", c)),
          },
          { key: "tx_hash", label: t("admin.deposits.txFilter"), kind: "text", width: 280 },
        ]}
      />
      <DepositsTable list={list} onRowClick={setOpen} />
      {open && (
        <Drawer
          open
          onOpenChange={(o) => !o && setOpen(null)}
          title={`${open.amount} ${open.asset}`}
          description={<span className="font-mono">{open.deposit_id}</span>}
          actions={<EnumBadge group="depositStatus" code={open.status} />}
        >
          <KeyValue
            items={[
              { label: t("admin.common.user"), value: <UserCell id={open.user_id} /> },
              { label: t("admin.common.amount"), value: <Num value={open.amount} unit={open.asset} /> },
              { label: t("admin.common.network"), value: open.network },
              { label: t("admin.deposits.kind"), value: open.kind },
              { label: t("admin.deposits.address"), value: <span className="font-mono text-xs">{open.address}</span>, copy: open.address },
              { label: t("admin.deposits.txHash"), value: <span className="font-mono text-xs">{open.tx_hash || "—"}</span>, copy: open.tx_hash || undefined },
              { label: t("admin.deposits.confirmations"), value: `${open.confirmations}/${open.required_confirmations}` },
              { label: t("admin.deposits.unclaimed"), value: open.unclaimed ? t("admin.common.yes") : t("admin.common.no") },
              ...(open.reason ? [{ label: t("admin.deposits.reason"), value: open.reason }] : []),
              { label: t("admin.common.updatedAt"), value: <TimeText value={open.updated_at} /> },
            ]}
          />
        </Drawer>
      )}
    </Page>
  );
}
