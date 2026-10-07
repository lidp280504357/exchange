import { selectSignedIn, useSession } from "@exchange/core";
import { entryOf, terminalLine, useProductsRead } from "@exchange/core/platform/products";
import type { ReactNode } from "react";
import { Navigate, useParams } from "react-router";
import { lazyPage } from "../../routing";

// The page of a closed product line, with its strings, in a chunk of its own.
const ProductClosed = lazyPage(() => import("./ProductClosed"), () => import("../../i18n/products"));

/**
 * ProductGate opens a trading page while its product line is open, and
 * says the product is not open while it is closed (design 2026-10-07,
 * product line switches §1 #2): not a 404, the terminal's chunk is not
 * even loaded. A futures address naming no contract is the terminal's to
 * answer. On a cold load it waits for the switches (the page's skeleton),
 * so a closed line's terminal never shows (review HK, F18 ②).
 */
export function ProductGate({ kind, children }: { kind: "trade" | "futures"; children: ReactNode }) {
  const { symbol = "" } = useParams();
  const open = useProductsRead();
  const signedIn = useSession(selectSignedIn);
  const line = terminalLine(kind, symbol);
  return line === null || open[line] ? children : <ProductClosed line={line} signedIn={signedIn} />;
}

/**
 * EntryRedirect answers the bare /trade and /futures: the line's default
 * market while it is open, else an open line's (review HK, F18 ④), once
 * the switches are read.
 */
export function EntryRedirect({ kind }: { kind: "trade" | "futures" }) {
  return <Navigate to={entryOf(kind, useProductsRead())} replace />;
}
