import { dec, type CandleData } from "@exchange/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useRef, useState } from "react";
import { fakeCandles, lots } from "../fixtures";
import { CandleChart } from "./CandleChart";

const STEP: Record<string, number> = { "1m": 60_000, "15m": 900_000, "1h": 3_600_000, "4h": 14_400_000, "1d": 86_400_000, "1w": 604_800_000 };
const SEED: Record<string, number> = { "1m": 11, "15m": 12, "1h": 13, "4h": 14, "1d": 15, "1w": 16 };

const meta = {
  title: "Charts/CandleChart",
  component: CandleChart,
  args: { candles: [], interval: "1h", priceDecimals: 1 },
  parameters: { layout: "fullscreen" },
} satisfies Meta<typeof CandleChart>;
export default meta;

type Story = StoryObj<typeof meta>;

/**
 * Live: the last candle updates every second (series.update), switching the
 * interval swaps the data without rebuilding the chart, and scrolling to
 * the left edge loads 200 older candles (three pages).
 */
export const Live: Story = {
  render: () => {
    const [interval, setInterval_] = useState("1h");
    const [candles, setCandles] = useState<CandleData[]>([]);
    const [loading, setLoading] = useState(true);
    const pages = useRef(0);

    useEffect(() => {
      setLoading(true);
      pages.current = 0;
      const id = setTimeout(() => {
        const step = STEP[interval] ?? 3_600_000;
        const end = Math.floor(Date.now() / step) * step;
        setCandles(fakeCandles(300, end, SEED[interval] ?? 1, step));
        setLoading(false);
      }, 300);
      return () => clearTimeout(id);
    }, [interval]);

    // The open candle moves every second.
    useEffect(() => {
      const id = window.setInterval(() => {
        setCandles((list) => {
          const last = list[list.length - 1];
          if (!last) return list;
          const move = String(Math.round((Math.random() - 0.5) * 300));
          const close = dec.add(last.close, dec.div(move, "10", 1));
          const next: CandleData = {
            ...last,
            close,
            high: dec.max(last.high, close),
            low: dec.min(last.low, close),
            volume: dec.add(last.volume, lots(Math.random() * 20_000)),
          };
          return [...list.slice(0, -1), next];
        });
      }, 1000);
      return () => window.clearInterval(id);
    }, []);

    const loadMore = (oldest: string) => {
      if (pages.current >= 3) return;
      pages.current += 1;
      const step = STEP[interval] ?? 3_600_000;
      setTimeout(() => {
        setCandles((list) => [...fakeCandles(200, Date.parse(oldest) - step, 100 + pages.current, step), ...list]);
      }, 500);
    };

    return (
      <div className="h-[560px] p-4">
        <div className="h-full rounded-2 border border-line-1">
          <CandleChart
            candles={candles}
            interval={interval}
            symbol="BTC-USDT"
            priceDecimals={1}
            intervals={["1m", "15m", "1h", "4h", "1d", "1w"]}
            onIntervalChange={setInterval_}
            onLoadMore={loadMore}
            hasMore={pages.current < 3}
            loading={loading}
            defaultIndicators={["MA", "VOL"]}
            height={480}
          />
        </div>
      </div>
    );
  },
};

export const Empty: Story = {
  render: () => (
    <div className="p-4">
      <div className="rounded-2 border border-line-1">
        <CandleChart candles={[]} interval="1h" priceDecimals={1} height={300} />
      </div>
    </div>
  ),
};
