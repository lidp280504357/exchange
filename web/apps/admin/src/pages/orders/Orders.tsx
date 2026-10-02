import { Button, Tabs } from "@exchange/ui";
import { Download } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";
import { useEnum } from "../../kit/enums";
import { dayEnd, dayStart, FilterBar, options, useFilters } from "../../kit/filters";
import { downloadCsv } from "../../kit/lists";
import { Page } from "../../kit/Page";
import { OrdersTable, TradesTable, useOrders, useTrades } from "../records/tables";

const KEYS = ["user_id", "order_id", "symbol", "status", "side", "accounts", "from", "to"] as const;

/** The accounts filter: the simulated market's bots or everyone else (ASTRA design §8 item 6). */
function useAccountsFilter() {
  const { t } = useTranslation();
  return {
    key: "accounts",
    label: t("admin.sim.accounts"),
    kind: "select" as const,
    options: options(t("admin.common.all"), ["users", "bots"], (c) => t(`admin.sim.accountsOf.${c}`)),
    width: 110,
  };
}

/** Orders and trades (design §10.3): filters by user, pair, status, the bots and time; the loaded rows export as CSV. */
export default function Orders() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const tab = params.get("tab") === "trades" ? "trades" : "orders";
  const setTab = (v: string) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set("tab", v);
        return next;
      },
      { replace: true },
    );
  return (
    <Page title={t("admin.nav.orders")}>
      <Tabs
        items={[
          { value: "orders", label: t("admin.orders.tabs.orders") },
          { value: "trades", label: t("admin.orders.tabs.trades") },
        ]}
        value={tab}
        onValueChange={setTab}
      />
      {tab === "orders" ? <OrderList /> : <TradeList />}
    </Page>
  );
}

function OrderList() {
  const { t } = useTranslation();
  const label = useEnum();
  const filters = useFilters(KEYS);
  const accounts = useAccountsFilter();
  const f = filters.values;
  const q = {
    user_id: f.user_id, order_id: f.order_id, symbol: f.symbol?.toUpperCase(), status: f.status, side: f.side, accounts: f.accounts,
    from: dayStart(f.from ?? ""), to: dayEnd(f.to ?? ""),
  };
  const list = useOrders(q);
  return (
    <>
      <FilterBar
        page="orders"
        filters={filters}
        defs={[
          { key: "user_id", label: t("admin.orders.userFilter"), kind: "text" },
          { key: "order_id", label: t("admin.orders.orderFilter"), kind: "text" },
          { key: "symbol", label: t("admin.common.symbol"), kind: "text", placeholder: "BTC-USDT", width: 130 },
          {
            key: "status",
            label: t("admin.common.status"),
            kind: "select",
            options: options(t("admin.common.all"), ["NEW", "OPEN", "PARTIALLY_FILLED", "FILLED", "CANCELED", "REJECTED"], (c) => label("orderStatus", c)),
          },
          { key: "side", label: t("admin.common.side"), kind: "select", options: options(t("admin.common.all"), ["BUY", "SELL"], (c) => label("side", c)), width: 100 },
          accounts,
          { key: "from", label: t("admin.common.from"), kind: "date" },
          { key: "to", label: t("admin.common.to"), kind: "date" },
        ]}
        extra={
          <Button
            size="sm"
            variant="secondary"
            icon={<Download size={14} />}
            disabled={list.rows.length === 0}
            title={t("admin.common.exportHint", { n: list.rows.length })}
            onClick={() =>
              downloadCsv(
                "orders.csv",
                [
                  { header: "created_at", value: (o) => o.created_at },
                  { header: "order_id", value: (o) => o.order_id },
                  { header: "user_id", value: (o) => o.user_id },
                  { header: "symbol", value: (o) => o.symbol },
                  { header: "side", value: (o) => o.side },
                  { header: "type", value: (o) => o.type },
                  { header: "price", value: (o) => o.price },
                  { header: "quantity", value: (o) => o.quantity },
                  { header: "quote_amount", value: (o) => o.quote_amount },
                  { header: "filled_quantity", value: (o) => o.filled_quantity },
                  { header: "filled_quote", value: (o) => o.filled_quote },
                  { header: "status", value: (o) => o.status },
                  { header: "reason", value: (o) => o.reason },
                  { header: "bot", value: (o) => String(o.bot) },
                ],
                list.rows,
              )
            }
          >
            {t("admin.common.export")}
          </Button>
        }
      />
      <OrdersTable list={list} />
    </>
  );
}

function TradeList() {
  const { t } = useTranslation();
  const filters = useFilters(["user_id", "symbol", "accounts", "from", "to"]);
  const accounts = useAccountsFilter();
  const f = filters.values;
  const q = { user_id: f.user_id, symbol: f.symbol?.toUpperCase(), accounts: f.accounts, from: dayStart(f.from ?? ""), to: dayEnd(f.to ?? "") };
  const list = useTrades(q);
  return (
    <>
      <FilterBar
        page="trades"
        filters={filters}
        defs={[
          { key: "user_id", label: t("admin.orders.userFilter"), kind: "text" },
          { key: "symbol", label: t("admin.common.symbol"), kind: "text", placeholder: "BTC-USDT", width: 130 },
          accounts,
          { key: "from", label: t("admin.common.from"), kind: "date" },
          { key: "to", label: t("admin.common.to"), kind: "date" },
        ]}
        extra={
          <Button
            size="sm"
            variant="secondary"
            icon={<Download size={14} />}
            disabled={list.rows.length === 0}
            title={t("admin.common.exportHint", { n: list.rows.length })}
            onClick={() =>
              downloadCsv(
                "trades.csv",
                [
                  { header: "executed_at", value: (x) => x.executed_at },
                  { header: "trade_id", value: (x) => x.trade_id },
                  { header: "symbol", value: (x) => x.symbol },
                  { header: "price", value: (x) => x.price },
                  { header: "quantity", value: (x) => x.quantity },
                  { header: "quote_quantity", value: (x) => x.quote_quantity },
                  { header: "taker_side", value: (x) => x.taker_side },
                  { header: "buyer_user_id", value: (x) => x.buyer_user_id },
                  { header: "seller_user_id", value: (x) => x.seller_user_id },
                  { header: "buyer_fee", value: (x) => x.buyer_fee },
                  { header: "seller_fee", value: (x) => x.seller_fee },
                  { header: "house_side", value: (x) => x.house_side ?? "" },
                  { header: "buyer_bot", value: (x) => String(x.buyer_bot) },
                  { header: "seller_bot", value: (x) => String(x.seller_bot) },
                ],
                list.rows,
              )
            }
          >
            {t("admin.common.export")}
          </Button>
        }
      />
      <TradesTable list={list} />
    </>
  );
}
