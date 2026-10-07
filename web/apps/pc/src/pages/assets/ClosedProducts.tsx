import {
  cancelContractOrder, cancelOrder, closeAtMarket, dec, enumLabel, errorText, formatDecimal, routes, type ContractOrder, type ContractPosition, type Order,
} from "@exchange/core";
import { productOf, useOpenProducts } from "@exchange/core/platform/products";
import { useWindDown } from "@exchange/core/platform/windDown";
import { Button, EmptyState, ErrorState, Skeleton, cn, toast } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { AssetsLayout } from "./parts/AssetsLayout";

/**
 * ClosedProducts (design 2026-10-07, product line switches §1 #2): what the
 * paused product lines still hold of the user's — positions to close at
 * market (reduce-only), orders to cancel, futures balances to move out, and
 * with spot paused the margin accounts to repay and empty on the margin
 * page (§1 #7). Nothing new opens here.
 */
export default function ClosedProducts() {
  const { t } = useTranslation();
  const open = useOpenProducts();
  const held = useWindDown(open);
  const orders: { order: ContractOrder | Order; contract: boolean }[] = [
    ...held.orders.map((order) => ({ order, contract: true })),
    ...held.spotOrders.map((order) => ({ order, contract: false })),
  ];
  const empty = !held.loading && held.positions.length + orders.length + held.balances.length + held.margin.length === 0;

  return (
    <AssetsLayout title={t("pcProducts.page.title")} subtitle={t("pcProducts.page.subtitle")}>
      <div className="flex flex-col gap-5" data-testid="closed-products">
        {held.error && !held.loading ? (
          <ErrorState message={errorText(held.error)} onRetry={held.refetch} />
        ) : held.loading ? (
          <Skeleton className="h-48 rounded-3" />
        ) : empty ? (
          <EmptyState title={t("pcProducts.page.done")} description={t("pcProducts.page.doneHint")} />
        ) : (
          <>
            {held.positions.length > 0 && (
              <Block title={t("pcProducts.page.positions")}>
                <Table head={["symbol", "side", "quantity", "entry", "mark", "pnl", "action"]}>
                  {held.positions.map((p) => (
                    <PositionRow key={`${p.symbol}-${p.position_side}`} p={p} onDone={held.refetch} />
                  ))}
                </Table>
              </Block>
            )}
            {orders.length > 0 && (
              <Block title={t("pcProducts.page.orders")}>
                <Table head={["market", "side", "price", "quantity", "filled", "action"]}>
                  {orders.map(({ order, contract }) => (
                    <OrderRow key={order.order_id} order={order} contract={contract} onDone={held.refetch} />
                  ))}
                </Table>
              </Block>
            )}
            {held.balances.length > 0 && (
              <Block title={t("pcProducts.page.balances")}>
                <Table head={["asset", "total", "available", "action"]}>
                  {held.balances.map((b) => (
                    <tr key={b.asset} className="border-t border-line-1">
                      <Td className="font-medium">{b.asset}</Td>
                      <Td>{formatDecimal(b.total)}</Td>
                      <Td>{formatDecimal(b.available)}</Td>
                      <Td className="text-right">
                        <Button asChild size="sm" variant="secondary">
                          <Link to={`${routes.transfer}?from=FUTURES&asset=${encodeURIComponent(b.asset)}`}>{t("pcProducts.page.transfer")}</Link>
                        </Button>
                      </Td>
                    </tr>
                  ))}
                </Table>
              </Block>
            )}
            {held.margin.length > 0 && (
              <Block title={t("pcProducts.page.margin")} hint={t("pcProducts.page.marginHint")}>
                <Table head={["account", "totalAsset", "totalLiability", "action"]}>
                  {held.margin.map((a) => (
                    <tr key={a.account === "MARGIN_ISOLATED" ? `${a.account}:${a.symbol ?? ""}` : a.account} className="border-t border-line-1">
                      <Td className="font-medium">
                        {a.account === "MARGIN_ISOLATED" ? t("pcProducts.page.isolated", { symbol: a.symbol ?? "" }) : t("pcProducts.page.cross")}
                      </Td>
                      <Td>{formatDecimal(a.total_asset)}</Td>
                      <Td className={dec.sign(a.total_liability) > 0 ? "text-down" : undefined}>{formatDecimal(a.total_liability)}</Td>
                      <Td className="text-right">
                        <Button asChild size="sm" variant="secondary">
                          <Link to={routes.margin}>{t("pcProducts.page.toMargin")}</Link>
                        </Button>
                      </Td>
                    </tr>
                  ))}
                </Table>
              </Block>
            )}
          </>
        )}
      </div>
    </AssetsLayout>
  );
}

function Block({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section className="overflow-hidden rounded-3 border border-line-1 bg-bg-1">
      <h2 className="px-5 pb-2 pt-4 text-md font-semibold text-fg-1">{title}</h2>
      {hint && <p className="-mt-1 px-5 pb-2 text-sm text-fg-3">{hint}</p>}
      {children}
    </section>
  );
}

function Table({ head, children }: { head: string[]; children: ReactNode }) {
  const { t } = useTranslation();
  return (
    <table className="w-full text-sm">
      <thead>
        <tr className="text-left text-xs text-fg-3">
          {head.map((h) => (
            <th key={h} className={cn("px-5 py-2 font-normal", h === "action" && "text-right")}>
              {t(`pcProducts.page.${h}`)}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>{children}</tbody>
    </table>
  );
}

function Td({ children, className }: { children: ReactNode; className?: string }) {
  return <td className={cn("px-5 py-3 tabular-nums text-fg-1", className)}>{children}</td>;
}

// A position closes at market with reduce-only orders: what a thin book
// leaves open is said, and may be closed again.
function PositionRow({ p, onDone }: { p: ContractPosition; onDone: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const long = p.position_side === "LONG" || (p.position_side === "BOTH" && dec.sign(p.quantity) > 0);
  const coin = productOf(p.symbol) === "coin_m";
  const size = formatDecimal(dec.abs(p.quantity));
  const close = async () => {
    setBusy(true);
    try {
      const r = await closeAtMarket({ symbol: p.symbol, quantity: p.quantity, position_side: p.position_side });
      if (dec.sign(r.left) <= 0) toast.success(t("pcProducts.page.closed"));
      else if (dec.sign(r.closed) > 0) toast.info(t("pcProducts.page.closedPart", { done: formatDecimal(r.closed), left: formatDecimal(r.left) }));
      else toast.error(r.reason ? `${t("pcProducts.page.notClosed")} · ${r.reason}` : t("pcProducts.page.notClosed"));
      void qc.invalidateQueries({ queryKey: ["derivatives"] });
      onDone();
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <tr className="border-t border-line-1">
      <Td className="font-medium">{p.symbol}</Td>
      <Td className={long ? "text-up" : "text-down"}>{long ? t("pcProducts.page.long") : t("pcProducts.page.short")}</Td>
      <Td>
        {size}
        {coin ? ` ${t("pcProducts.page.contracts")}` : ""}
      </Td>
      <Td>{formatDecimal(p.entry_price)}</Td>
      <Td>{formatDecimal(p.mark_price)}</Td>
      <Td className={dec.sign(p.unrealized_pnl ?? "0") > 0 ? "text-up" : dec.sign(p.unrealized_pnl ?? "0") < 0 ? "text-down" : undefined}>
        {formatDecimal(p.unrealized_pnl)} {p.settle_asset}
      </Td>
      <Td className="text-right">
        <Button size="sm" variant="danger" loading={busy} onClick={() => void close()}>
          {t("pcProducts.page.close")}
        </Button>
      </Td>
    </tr>
  );
}

function OrderRow({ order, contract, onDone }: { order: ContractOrder | Order; contract: boolean; onDone: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const cancel = async () => {
    setBusy(true);
    try {
      await (contract ? cancelContractOrder(order.order_id) : cancelOrder(order.order_id));
      toast.success(t("pcProducts.page.canceled"));
      void qc.invalidateQueries({ queryKey: [contract ? "derivatives" : "orders"] });
      onDone();
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <tr className="border-t border-line-1">
      <Td className="font-medium">{order.symbol}</Td>
      <Td className={order.side === "BUY" ? "text-up" : "text-down"}>
        {enumLabel(order.side)} · {enumLabel(order.type)}
      </Td>
      <Td>{formatDecimal(order.price)}</Td>
      <Td>{formatDecimal(order.quantity)}</Td>
      <Td>{formatDecimal("filled_quantity" in order ? order.filled_quantity : undefined)}</Td>
      <Td className="text-right">
        <Button size="sm" variant="secondary" loading={busy} onClick={() => void cancel()}>
          {t("pcProducts.page.cancel")}
        </Button>
      </Td>
    </tr>
  );
}
