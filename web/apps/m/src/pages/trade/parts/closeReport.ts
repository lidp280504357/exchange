import { codeText, dec, isActive, type MarketClose } from "@exchange/core";
import { toast, type ToastAction } from "@exchange/ui";

type T = (key: string, options?: Record<string, unknown>) => string;

/**
 * reportClose says how a market close went (review FE, B129; FN, B136):
 * all of it closed; part of it, with what is left and a button that
 * closes the rest; or nothing, because the order was refused (its
 * reason), is still being processed, or met no depth (with the button).
 */
export function reportClose(t: T, r: Pick<MarketClose, "closed" | "left" | "status" | "reason">, amount: (v: string) => string, again: ToastAction): void {
  if (dec.sign(r.left) === 0) toast.success(t("mTrade.closeDone"));
  else if (dec.sign(r.closed) > 0) {
    toast.info(t("mTrade.closePartly", { closed: amount(r.closed), left: amount(r.left) }), { description: t("mTrade.closePartlyHint"), action: again });
  } else if (r.status === "REJECTED") toast.error(t("mTrade.closeRejected", { reason: codeText(r.reason) }));
  else if (isActive(r.status)) toast.info(t("mTrade.closePending"));
  else toast.error(t("mTrade.closeNone"), { action: again });
}
