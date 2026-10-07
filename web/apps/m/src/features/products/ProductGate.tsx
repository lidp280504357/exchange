import { selectSignedIn, useSession } from "@exchange/core";
import { terminalLine, useOpenProducts } from "@exchange/core/platform/products";
import type { ReactNode } from "react";
import { useParams } from "react-router";
import { lazyPage } from "../../routing";

// The page of a closed product line, with its strings, in a chunk of its own.
const ProductClosed = lazyPage(() => import("./ProductClosed"), () => import("../../i18n/products"));

/**
 * ProductGate opens a trading page while its product line is open, and
 * says the product is not open while it is closed (design 2026-10-07,
 * product line switches §1 #2): not a 404, the terminal's chunk is not
 * even loaded. A futures address naming no contract is the terminal's to
 * answer.
 */
export function ProductGate({ kind, children }: { kind: "trade" | "futures"; children: ReactNode }) {
  const { symbol = "" } = useParams();
  const open = useOpenProducts();
  const signedIn = useSession(selectSignedIn);
  const line = terminalLine(kind, symbol);
  return line === null || open[line] ? children : <ProductClosed line={line} signedIn={signedIn} />;
}
