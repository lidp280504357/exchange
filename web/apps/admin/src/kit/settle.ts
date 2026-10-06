import { useTranslation } from "react-i18next";

// The asset of a contract row's amounts (design 2026-10-06 §2.5, G5;
// review ER ①): USDT for a linear contract, the coin for a coin-margined
// one, whose quantities are whole contracts. Rows from before the
// coin-margined contracts carry none: USDT.

type Settled = { settle_asset?: string | null };

/** settleOf is the asset a contract row's margin, results, fees and funding are in. */
export const settleOf = (row: Settled) => row.settle_asset || "USDT";

/** coinMargined tells a coin-margined contract's row (only its settlement asset is not USDT). */
export const coinMargined = (row: Settled) => settleOf(row) !== "USDT";

/** cents shows USDT amounts to the cent and a coin's in full. */
export const cents = (row: Settled) => (coinMargined(row) ? undefined : 2);

/** useQuantityUnit is the unit of a row's quantities: whole contracts on a coin-margined contract, none otherwise (the base asset). */
export function useQuantityUnit() {
  const { t } = useTranslation();
  return (row: Settled) => (coinMargined(row) ? t("admin.coinm.contracts") : undefined);
}
