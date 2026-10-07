import { errorText } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, ErrorState, Skeleton } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { DangerAction, type Notice } from "../../kit/actions";
import { TimeText } from "../../kit/format";
import { Card } from "../../kit/Page";

// The product lines (design 2026-10-07, product switches §1 #5, K3): spot
// trading and the USDT- and coin-margined contracts, each a switch on its
// own - never deleted - with when and by whom it was last switched and
// what closing it touches; closing one is confirmed with what it does
// (orders canceled, positions kept, the sites hiding it), and a closed
// line's orders still open can be canceled again.

type ProductState = AdminSchemas["ProductState"];
type ProductCancel = AdminSchemas["ProductCancel"];

export const productsKey = ["admin", "products"];

/** ProductsCard is the three product lines with their switches. */
export function ProductsCard({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: productsKey,
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/products")),
    refetchInterval: 15_000,
  });
  const control = can(admin, "instruments.trading");
  return (
    <Card title={t("admin.products.title")}>
      <p className="mb-3 text-xs text-fg-3">{t("admin.products.hint")}</p>
      {q.isError ? (
        <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} />
      ) : !q.data ? (
        <Skeleton className="h-24 w-full" />
      ) : (
        <div className="grid gap-3 md:grid-cols-3" data-testid="products">
          {q.data.products.map((p) => (
            <ProductLine key={p.product} p={p} control={control} />
          ))}
        </div>
      )}
      {q.data && q.data.partial.length > 0 && <p className="mt-2 text-xs text-warn-strong">{t("admin.products.partial")}</p>}
    </Card>
  );
}

/** ProductLine is one line: its switch, its last switch and what closing it touches. */
function ProductLine({ p, control }: { p: ProductState; control: boolean }) {
  const { t } = useTranslation();
  const name = t(`admin.products.lines.${p.product}`);
  const count = (n: number | null | undefined, key: string) => (n === null || n === undefined ? t("admin.products.unknown") : t(key, { n }));
  return (
    <div
      className="flex flex-col gap-1.5 rounded-2 border border-line-1 px-3 py-2.5"
      data-testid={`product-${p.product}`}
      data-state={p.enabled ? "open" : "closed"}
    >
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium text-fg-1">{name}</span>
        <span className="font-mono text-xs text-fg-3">{p.flag}</span>
        <Badge tone={p.enabled ? "success" : "warn"} className="ml-auto">
          {t(p.enabled ? "admin.products.open" : "admin.products.closed")}
        </Badge>
      </div>
      <span className="text-xs text-fg-3">
        {p.switched_at ? (
          <>
            {t("admin.products.since", { who: p.switched_by ?? "—" })}
            <TimeText value={p.switched_at} />
          </>
        ) : (
          t("admin.products.never")
        )}
      </span>
      <span className="text-xs text-fg-2" title={t("admin.products.ordersHint")}>
        {count(p.open_orders, "admin.products.orders")} · {count(p.open_positions, p.product === "spot" ? "admin.products.debts" : "admin.products.positions")}
      </span>
      {control && <ProductSwitch p={p} name={name} />}
    </div>
  );
}

/** ProductSwitch closes or opens a line, or cancels a closed line's orders still open. */
function ProductSwitch({ p, name }: { p: ProductState; name: string }) {
  const { t } = useTranslation();
  const closing = p.enabled;
  const left = !p.enabled && (p.open_orders ?? 0) > 0;
  const put = async (enabled: boolean, reason: string) =>
    adminData(await adminApi.PUT("/admin/v1/products", { body: { product: p.product, enabled, reason } }));
  // What a close (or a cancel of what is left) did: a cancel that did not
  // finish is told without the success tone, with how to retry (A85).
  const said = (res: unknown, done: string): string | Notice => {
    const c = (res as { cancel?: ProductCancel | null }).cancel;
    if (!c || c.status === "DONE") return t(done, { n: c?.canceled ?? 0 });
    if (c.status === "UNAVAILABLE") return { info: t("admin.products.cancelUnavailable") };
    return { info: t("admin.products.cancelFailed", { n: c.canceled, why: c.error ?? "—" }) };
  };
  return (
    <span className="flex flex-wrap gap-2">
      <DangerAction
        trigger={(open) => (
          <Button size="sm" variant={closing ? "danger" : "secondary"} onClick={open} data-testid={`product-switch-${p.product}`}>
            {t(closing ? "admin.products.close" : "admin.products.reopen")}
          </Button>
        )}
        danger={closing}
        title={t(closing ? "admin.products.closeTitle" : "admin.products.openTitle", { line: name })}
        description={
          closing ? (
            <span className="flex flex-col gap-1">
              {/* Spot's positions are the margin accounts owing (K1a). */}
              <span>
                {t(p.product === "spot" ? "admin.products.closeSpotHint" : "admin.products.closeHint", {
                  orders: p.open_orders ?? "?",
                  positions: p.open_positions ?? "?",
                })}
              </span>
              {p.product === "spot" && <span className="text-warn-strong">{t("admin.products.closeSpotAstra")}</span>}
            </span>
          ) : (
            t("admin.products.openHint")
          )
        }
        target={<span className="font-medium">{name}</span>}
        confirmWord={p.product}
        run={(reason) => put(!p.enabled, reason)}
        success={(res) => (closing ? said(res, "admin.products.closedDone") : t("admin.products.openedDone"))}
        invalidate={[productsKey]}
      />
      {left && (
        <DangerAction
          trigger={(open) => (
            <Button size="sm" variant="secondary" onClick={open} data-testid={`product-cancel-left-${p.product}`}>
              {t("admin.products.cancelLeft")}
            </Button>
          )}
          title={t("admin.products.cancelLeftTitle", { line: name })}
          description={t("admin.products.cancelLeftHint", { orders: p.open_orders ?? 0 })}
          target={<span className="font-medium">{name}</span>}
          confirmWord={p.product}
          run={(reason) => put(false, reason)}
          success={(res) => said(res, "admin.products.leftDone")}
          invalidate={[productsKey]}
        />
      )}
    </span>
  );
}
