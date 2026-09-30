import { lazy, Suspense } from "react";
import { Skeleton } from "../components/Skeleton";
import { cn } from "../lib/cn";
import type { CandleChartProps } from "./CandleChart";

const Chart = lazy(() => import("./CandleChart").then((m) => ({ default: m.CandleChart })));

/**
 * CandleChart as the package index exports it: the chart and
 * lightweight-charts load in their own chunk on first render (design
 * §4.4), with a skeleton of the same size meanwhile. Import
 * "@exchange/ui/charts/CandleChart" for the eager component.
 */
export function CandleChart(props: CandleChartProps) {
  const height = props.height ?? 420;
  return (
    <Suspense
      fallback={
        <div className={cn("flex flex-col bg-bg-1", props.className)}>
          {props.toolbar !== false && <div className="h-9 border-b border-line-1" />}
          <div className={cn("p-3", height === "fill" && "min-h-0 flex-1")} style={height === "fill" ? undefined : { height }}>
            <Skeleton className="h-full w-full" />
          </div>
        </div>
      }
    >
      <Chart {...props} />
    </Suspense>
  );
}
