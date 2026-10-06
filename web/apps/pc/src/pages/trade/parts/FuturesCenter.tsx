import type { Contract } from "@exchange/core";
import { Skeleton, Tabs, cn } from "@exchange/ui";
import { Suspense, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { FuturesDataBoard } from "../../../features/futures/lazyBoard";

type CenterTab = "chart" | "data";

/**
 * FuturesCenter is the futures terminal's centre column with two tabs
 * (design 2026-10-06 §3.3, where the reference market's terminal keeps its
 * trading data): the chart (its children) and the contract's futures data.
 * The chart stays laid out under the data (hidden, not unmounted), so it
 * keeps its candles, scroll and size; the data board loads on first use.
 */
export function FuturesCenter({ contract, children, className }: { contract: Contract; children: ReactNode; className?: string }) {
  const { t } = useTranslation();
  const [tab, setTab] = useState<CenterTab>("chart");
  const data = tab === "data";
  const preload = () => void FuturesDataBoard.preload().catch(() => {});
  return (
    <div className={cn("flex min-h-0 min-w-0 flex-col bg-bg-1", className)}>
      <div onPointerEnter={preload} onFocus={preload}>
        <Tabs
          value={tab}
          onValueChange={(v) => setTab(v as CenterTab)}
          size="sm"
          listClassName="px-3"
          aria-label={t("pcFutures.tabs.label")}
          items={[
            { value: "chart", label: t("pcFutures.tabs.chart") },
            { value: "data", label: t("pcFutures.tabs.data") },
          ]}
        />
      </div>
      <div className="relative flex min-h-0 flex-1 flex-col">
        <div aria-hidden={data || undefined} className={cn("flex min-h-0 flex-1 flex-col", data && "invisible")}>
          {children}
        </div>
        {data && (
          <div data-testid="futures-data" className="absolute inset-0 overflow-y-auto overscroll-contain bg-bg-0 p-2">
            <Suspense fallback={<BoardSkeleton />}>
              <FuturesDataBoard contract={contract} layout="terminal" />
            </Suspense>
          </div>
        )}
      </div>
    </div>
  );
}

function BoardSkeleton() {
  return (
    <div className="flex flex-col gap-2">
      <Skeleton className="h-6 w-64" />
      <div className="grid grid-cols-[repeat(auto-fill,minmax(300px,1fr))] gap-2">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-56 w-full rounded-2" />
        ))}
      </div>
    </div>
  );
}
