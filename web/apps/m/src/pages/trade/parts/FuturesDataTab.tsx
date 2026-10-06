import type { Contract } from "@exchange/core";
import { Skeleton } from "@exchange/ui";
import { Suspense, useEffect, useState } from "react";
import { FuturesDataBoard } from "../../../features/futures/lazyBoard";

/**
 * FuturesDataTab is the futures terminal's 数据 tab (design 2026-10-06
 * §3.3). The swipeable tabs keep every panel mounted, so the board (its
 * chunk and its requests) waits until the tab is first shown, and stops
 * reading again while another tab is.
 */
export function FuturesDataTab({ contract, active }: { contract: Contract; active: boolean }) {
  const [shown, setShown] = useState(active);
  useEffect(() => {
    if (active) setShown(true);
  }, [active]);
  if (!shown) return null;
  return (
    <div data-testid="futures-data" className="px-4 py-3">
      <Suspense
        fallback={
          <div className="flex flex-col gap-3">
            <Skeleton className="h-9 w-full rounded-full" />
            <Skeleton className="h-60 w-full rounded-2" />
            <Skeleton className="h-60 w-full rounded-2" />
          </div>
        }
      >
        <FuturesDataBoard contract={contract} active={active} />
      </Suspense>
    </div>
  );
}
