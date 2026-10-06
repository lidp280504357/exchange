import { METRIC_VALUES, type ContractSpec, type FuturesMetric } from "@exchange/core/futures/index";
import type { FuturesMetricCommon, FuturesMetricLabels, LiquidationTapeLabels } from "@exchange/ui/futures/index";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

// The words of the futures data cards (namespace pcFutures), in the
// shapes @exchange/ui's FuturesMetric and LiquidationTape take.

export function useFuturesLabels() {
  const { t } = useTranslation();
  return useMemo(() => {
    const metric = (m: FuturesMetric): FuturesMetricLabels => ({
      title: t(`pcFutures.metrics.${m}.title`),
      hint: t(`pcFutures.metrics.${m}.hint`),
      values: Object.fromEntries(METRIC_VALUES[m].map((v) => [v.key, t(`pcFutures.values.${v.key}`)])),
    });
    const common: FuturesMetricCommon = {
      chart: t("pcFutures.common.chart"),
      table: t("pcFutures.common.table"),
      time: t("pcFutures.common.time"),
      empty: t("pcFutures.none"),
      windowChange: (value) => t("pcFutures.common.windowChange", { value }),
    };
    const liquidations = (unit: string): LiquidationTapeLabels => ({
      time: t("pcFutures.liquidations.time"),
      side: t("pcFutures.liquidations.side"),
      price: t("pcFutures.liquidations.price"),
      quantity: t("pcFutures.liquidations.quantity", { unit }),
      value: t("pcFutures.liquidations.value"),
      long: t("pcFutures.liquidations.long"),
      short: t("pcFutures.liquidations.short"),
    });
    return { metric, common, liquidations, contracts: t("pcFutures.contracts") };
  }, [t]);
}

/** qtyUnit is what a contract's quantities count: its base asset, or contracts of a coin-margined one. */
export function qtyUnit(c: Pick<ContractSpec, "margin_type" | "base_asset">, contracts: string): string {
  return c.margin_type === "COIN" ? contracts : c.base_asset;
}
