import { METRIC_VALUES, type ContractSpec, type FuturesMetric } from "@exchange/core/futures/index";
import type { FuturesMetricCommon, FuturesMetricLabels, LiquidationTapeLabels } from "@exchange/ui/futures/index";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

// The words of the futures data cards (namespace mFutures), in the
// shapes @exchange/ui's FuturesMetric and LiquidationTape take.

export function useFuturesLabels() {
  const { t } = useTranslation();
  return useMemo(() => {
    const metric = (m: FuturesMetric): FuturesMetricLabels => ({
      title: t(`mFutures.metrics.${m}.title`),
      hint: t(`mFutures.metrics.${m}.hint`),
      values: Object.fromEntries(METRIC_VALUES[m].map((v) => [v.key, t(`mFutures.values.${v.key}`)])),
    });
    const common: FuturesMetricCommon = {
      chart: t("mFutures.common.chart"),
      table: t("mFutures.common.table"),
      time: t("mFutures.common.time"),
      empty: t("mFutures.none"),
      windowChange: (value) => t("mFutures.common.windowChange", { value }),
    };
    const liquidations = (unit: string): LiquidationTapeLabels => ({
      time: t("mFutures.liquidations.time"),
      side: t("mFutures.liquidations.side"),
      price: t("mFutures.liquidations.price"),
      quantity: t("mFutures.liquidations.quantity", { unit }),
      value: t("mFutures.liquidations.value"),
      long: t("mFutures.liquidations.long"),
      short: t("mFutures.liquidations.short"),
    });
    return { metric, common, liquidations, contracts: t("mFutures.contracts") };
  }, [t]);
}

/** qtyUnit is what a contract's quantities count: its base asset, or contracts of a coin-margined one. */
export function qtyUnit(c: Pick<ContractSpec, "margin_type" | "base_asset">, contracts: string): string {
  return c.margin_type === "COIN" ? contracts : c.base_asset;
}
