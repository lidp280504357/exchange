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
import { usePageHeader } from "../../layout/header";

/**
 * ClosedProducts on the phone (design 2026-10-07, product line switches
 * §1 #2): what the paused product lines still hold of the user's — a card
 * per position to close at market (reduce-only), per order to cancel, per
 * futures balance to move out, and with spot paused per margin account to
 * repay and empty on the margin page (§1 #7). Nothing new opens here.
 */
export default function ClosedProducts() {
  const { t } = useTranslation();
  usePageHeader({ title: t("mProducts.page.title"), back: routes.assets }, [t]);
  const open = useOpenProducts();
  const held = useWindDown(open);
  const orders: { order: ContractOrder | Order; contract: boolean }[] = [
    ...held.orders.map((order) => ({ order, contract: true })),
    ...held.spotOrders.map((order) => ({ order, contract: false })),
  ];
  const empty = !held.loading && held.positions.length + orders.length + held.balances.length + held.margin.length === 0;

  return (
    <div className="flex flex-col gap-4 px-4 py-3" data-testid="closed-products">
      <p className="px-1 text-sm leading-relaxed text-fg-3">{t("mProducts.page.subtitle")}</p>
      {held.error && !held.loading ? (
        <ErrorState message={errorText(held.error)} onRetry={held.refetch} />
      ) : held.loading ? (
        <Skeleton className="h-40 rounded-3" />
      ) : empty ? (
        <EmptyState title={t("mProducts.page.done")} description={t("mProducts.page.doneHint")} />
      ) : (
        <>
          {held.positions.length > 0 && (
            <Group title={t("mProducts.page.positions")}>
              {held.positions.map((p) => (
                <PositionCard key={`${p.symbol}-${p.position_side}`} p={p} onDone={held.refetch} />
              ))}
            </Group>
          )}
          {orders.length > 0 && (
            <Group title={t("mProducts.page.orders")}>
              {orders.map(({ order, contract }) => (
                <OrderCard key={order.order_id} order={order} contract={contract} onDone={held.refetch} />
              ))}
            </Group>
          )}
          {held.balances.length > 0 && (
            <Group title={t("mProducts.page.balances")}>
              {held.balances.map((b) => (
                <Card key={b.asset} title={b.asset}>
                  <Fact label={t("mProducts.page.total")}>{formatDecimal(b.total)}</Fact>
                  <Fact label={t("mProducts.page.available")}>{formatDecimal(b.available)}</Fact>
                  <Button asChild size="lg" block variant="secondary" className="mt-2">
                    <Link to={`${routes.transfer}?from=FUTURES&asset=${encodeURIComponent(b.asset)}`}>{t("mProducts.page.transfer")}</Link>
                  </Button>
                </Card>
              ))}
            </Group>
          )}
          {held.margin.length > 0 && (
            <Group title={t("mProducts.page.margin")} hint={t("mProducts.page.marginHint")}>
              {held.margin.map((a) => (
                <Card
                  key={a.account === "MARGIN_ISOLATED" ? `${a.account}:${a.symbol ?? ""}` : a.account}
                  title={a.account === "MARGIN_ISOLATED" ? t("mProducts.page.isolated", { symbol: a.symbol ?? "" }) : t("mProducts.page.cross")}
                >
                  <Fact label={t("mProducts.page.totalAsset")}>{formatDecimal(a.total_asset)}</Fact>
                  <Fact label={t("mProducts.page.totalLiability")} className={dec.sign(a.total_liability) > 0 ? "text-down" : undefined}>
                    {formatDecimal(a.total_liability)}
                  </Fact>
                  <Button asChild size="lg" block variant="secondary" className="mt-2">
                    <Link to={routes.margin}>{t("mProducts.page.toMargin")}</Link>
                  </Button>
                </Card>
              ))}
            </Group>
          )}
        </>
      )}
    </div>
  );
}

function Group({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="px-1 text-sm font-medium text-fg-2">{title}</h2>
      {hint && <p className="-mt-1 px-1 text-xs leading-relaxed text-fg-3">{hint}</p>}
      {children}
    </section>
  );
}

function Card({ title, extra, children }: { title: ReactNode; extra?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5 rounded-3 bg-bg-1 p-4">
      <div className="mb-1 flex items-center justify-between gap-2">
        <span className="font-medium text-fg-1">{title}</span>
        {extra}
      </div>
      {children}
    </div>
  );
}

function Fact({ label, children, className }: { label: ReactNode; children: ReactNode; className?: string }) {
  return (
    <div className="flex items-center justify-between gap-3 text-sm">
      <span className="text-fg-3">{label}</span>
      <span className={cn("tabular-nums text-fg-1", className)}>{children}</span>
    </div>
  );
}

// A position closes at market with reduce-only orders: what a thin book
// leaves open is said, and may be closed again.
function PositionCard({ p, onDone }: { p: ContractPosition; onDone: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const long = p.position_side === "LONG" || (p.position_side === "BOTH" && dec.sign(p.quantity) > 0);
  const coin = productOf(p.symbol) === "coin_m";
  const pnl = dec.sign(p.unrealized_pnl ?? "0");
  const close = async () => {
    setBusy(true);
    try {
      const r = await closeAtMarket({ symbol: p.symbol, quantity: p.quantity, position_side: p.position_side });
      if (dec.sign(r.left) <= 0) toast.success(t("mProducts.page.closed"));
      else if (dec.sign(r.closed) > 0) toast.info(t("mProducts.page.closedPart", { done: formatDecimal(r.closed), left: formatDecimal(r.left) }));
      else toast.error(r.reason ? `${t("mProducts.page.notClosed")} · ${r.reason}` : t("mProducts.page.notClosed"));
      void qc.invalidateQueries({ queryKey: ["derivatives"] });
      onDone();
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Card
      title={p.symbol}
      extra={<span className={cn("text-sm font-medium", long ? "text-up" : "text-down")}>{long ? t("mProducts.page.long") : t("mProducts.page.short")}</span>}
    >
      <Fact label={t("mProducts.page.quantity")}>
        {formatDecimal(dec.abs(p.quantity))}
        {coin ? ` ${t("mProducts.page.contracts")}` : ""}
      </Fact>
      <Fact label={t("mProducts.page.entry")}>{formatDecimal(p.entry_price)}</Fact>
      <Fact label={t("mProducts.page.mark")}>{formatDecimal(p.mark_price)}</Fact>
      <Fact label={t("mProducts.page.pnl")} className={pnl > 0 ? "text-up" : pnl < 0 ? "text-down" : undefined}>
        {formatDecimal(p.unrealized_pnl)} {p.settle_asset}
      </Fact>
      <Button size="lg" block variant="danger" loading={busy} onClick={() => void close()} className="mt-2">
        {t("mProducts.page.close")}
      </Button>
    </Card>
  );
}

function OrderCard({ order, contract, onDone }: { order: ContractOrder | Order; contract: boolean; onDone: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const cancel = async () => {
    setBusy(true);
    try {
      await (contract ? cancelContractOrder(order.order_id) : cancelOrder(order.order_id));
      toast.success(t("mProducts.page.canceled"));
      void qc.invalidateQueries({ queryKey: [contract ? "derivatives" : "orders"] });
      onDone();
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Card
      title={order.symbol}
      extra={
        <span className={cn("text-sm font-medium", order.side === "BUY" ? "text-up" : "text-down")}>
          {enumLabel(order.side)} · {enumLabel(order.type)}
        </span>
      }
    >
      <Fact label={t("mProducts.page.price")}>{formatDecimal(order.price)}</Fact>
      <Fact label={t("mProducts.page.quantity")}>{formatDecimal(order.quantity)}</Fact>
      <Fact label={t("mProducts.page.filled")}>{formatDecimal("filled_quantity" in order ? order.filled_quantity : undefined)}</Fact>
      <Button size="lg" block variant="secondary" loading={busy} onClick={() => void cancel()} className="mt-2">
        {t("mProducts.page.cancel")}
      </Button>
    </Card>
  );
}
