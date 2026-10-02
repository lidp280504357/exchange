import { can, type Admin } from "@exchange/core/api/admin";
import { Button, Drawer, KeyValue, Tabs } from "@exchange/ui";
import { FilePlus2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { EnumBadge, useEnum } from "../../kit/enums";
import { FilterBar, options, useFilters } from "../../kit/filters";
import { Num, TimeText, UserCell } from "../../kit/format";
import { Page } from "../../kit/Page";
import { useTodo } from "../../live";
import { DepositsTable, useDeposits, type Deposit } from "../records/tables";
import { BackfillDrawer } from "./Backfill";
import { ReviewDepositDrawer, ReviewDepositsTable, useReviewDeposits, type ReviewDeposit, type ReviewView } from "./depositReview";

const KEYS = ["view", "user_id", "asset", "network", "status", "tx_hash"] as const;
const VIEWS = ["all", "attention", "manual"] as const;
type View = (typeof VIEWS)[number];

/**
 * Deposits (design §10.3, 2026-10-02 §4.3): every deposit by user, asset,
 * network, status or transaction (the read model); the ones waiting for a
 * decision, credited to their user or rejected (?view=attention); the
 * backfills still waiting for the custodian's callback (?view=manual);
 * and the backfill of a deposit whose callback was lost.
 */
export default function Deposits({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const filters = useFilters(KEYS);
  const f = filters.values;
  const view: View = (VIEWS as readonly string[]).includes(f.view ?? "") ? (f.view as View) : "all";
  const todo = useTodo();
  const [backfill, setBackfill] = useState(false);
  return (
    <Page
      title={t("admin.nav.deposits")}
      help={view === "attention" ? t("admin.depositReview.attentionHelp") : view === "manual" ? t("admin.depositReview.manualHelp") : undefined}
      actions={
        can(admin, "deposits.review") && (
          <Button icon={<FilePlus2 size={16} />} onClick={() => setBackfill(true)}>
            {t("admin.backfill.open")}
          </Button>
        )
      }
    >
      <Tabs
        items={VIEWS.map((v) => ({
          value: v,
          label: t(`admin.depositReview.views.${v}`),
          count: v === "attention" && todo?.deposits ? todo.deposits : undefined,
        }))}
        value={view}
        onValueChange={(v) => filters.set({ ...Object.fromEntries(KEYS.map((k) => [k, ""])), view: v === "all" ? "" : v })}
        aria-label={t("admin.nav.deposits")}
      />
      <div key={view} className="flex flex-col gap-4 animate-rise">
        {view === "all" ? <AllDeposits filters={filters} /> : <ToHandle admin={admin} view={view} filters={filters} />}
      </div>
      {backfill && <BackfillDrawer onClose={() => setBackfill(false)} />}
    </Page>
  );
}

type Filters = ReturnType<typeof useFilters>;

/** AllDeposits is every deposit in its latest state, from the read model; a row shows its details. */
function AllDeposits({ filters }: { filters: Filters }) {
  const { t } = useTranslation();
  const label = useEnum();
  const f = filters.values;
  const list = useDeposits({ user_id: f.user_id, asset: f.asset?.toUpperCase(), network: f.network?.toUpperCase(), status: f.status, tx_hash: f.tx_hash });
  const [open, setOpen] = useState<Deposit | null>(null);
  return (
    <>
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
              ...(open.reason ? [{ label: t("admin.deposits.reason"), value: <EnumBadge group="depositReason" code={open.reason} /> }] : []),
              { label: t("admin.common.updatedAt"), value: <TimeText value={open.updated_at} /> },
            ]}
          />
        </Drawer>
      )}
    </>
  );
}

/** ToHandle lists wallet-service's deposits of a review view; a row opens its details and decisions. */
function ToHandle({ admin, view, filters }: { admin: Admin; view: ReviewView; filters: Filters }) {
  const { t } = useTranslation();
  const f = filters.values;
  const list = useReviewDeposits(view, { user_id: f.user_id, network: f.network?.toUpperCase() });
  const [open, setOpen] = useState<ReviewDeposit | null>(null);
  return (
    <>
      <FilterBar
        page={`deposits-${view}`}
        filters={filters}
        defs={[
          { key: "user_id", label: t("admin.orders.userFilter"), kind: "text" },
          { key: "network", label: t("admin.common.network"), kind: "text", placeholder: "TRON", width: 140 },
        ]}
      />
      <ReviewDepositsTable admin={admin} view={view} list={list} onRowClick={setOpen} />
      {open && <ReviewDepositDrawer admin={admin} d={open} onClose={() => setOpen(null)} />}
    </>
  );
}
