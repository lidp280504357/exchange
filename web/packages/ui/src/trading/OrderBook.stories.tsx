import { dec, type BookView } from "@exchange/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useState } from "react";
import { fakeMarket, price, tick } from "../fixtures";
import { OrderBook, type BookMode } from "./OrderBook";

/**
 * useFakeBook runs a fake market: `perSecond` depth messages, the view cut
 * at most once per animation frame (like core's MarketStore).
 */
function useFakeBook(depth: number, step: string, perSecond = 10) {
  const [m] = useState(() => fakeMarket());
  const [view, setView] = useState<BookView>(() => m.book.view(depth, step));
  const [last, setLast] = useState<{ price: string; dir: "up" | "down" | null }>({ price: price(m.mid), dir: null });
  useEffect(() => {
    let raf = 0;
    let scheduled = false;
    setView(m.book.view(depth, step));
    const id = setInterval(() => {
      tick(m);
      if (scheduled) return;
      scheduled = true;
      raf = requestAnimationFrame(() => {
        scheduled = false;
        setView(m.book.view(depth, step));
        setLast((prev) => {
          const p = price(m.mid);
          const c = dec.cmp(p, prev.price);
          return c === 0 ? prev : { price: p, dir: c > 0 ? "up" : "down" };
        });
      });
    }, 1000 / perSecond);
    return () => {
      clearInterval(id);
      cancelAnimationFrame(raf);
    };
  }, [m, depth, step, perSecond]);
  return { view, last };
}

const empty: BookView = { bids: [], asks: [], maxTotal: "0", spread: null, seq: 0 };

const meta = {
  title: "Trading/OrderBook",
  component: OrderBook,
  args: { view: empty, priceDecimals: 1, qtyDecimals: 4 },
} satisfies Meta<typeof OrderBook>;
export default meta;

type Story = StoryObj<typeof meta>;

/** 20 levels a side, 10 depth messages a second, one render per frame. */
export const Live: Story = {
  render: () => {
    const [mode, setMode] = useState<BookMode>("both");
    const [step, setStep] = useState("0.1");
    const [picked, setPicked] = useState("");
    const { view, last } = useFakeBook(mode === "both" ? 20 : 40, step === "0.1" ? "" : step);
    return (
      <div className="flex gap-6">
        <div className="w-80 rounded-2 border border-line-1">
          <OrderBook
            view={view}
            priceDecimals={dec.decimalsOf(step)}
            qtyDecimals={4}
            mode={mode}
            onModeChange={setMode}
            steps={["0.1", "1", "10", "100"]}
            step={step}
            onStepChange={setStep}
            lastPrice={last.price}
            lastDirection={last.dir}
            base="BTC"
            quote="USDT"
            onPriceClick={(p, q) => setPicked(q ? `${p} × ${q}` : p)}
          />
        </div>
        <div className="text-xs text-fg-3">
          <p>Click a row to fill the price; Shift+click adds the cumulative amount.</p>
          <p className="mt-2">picked: {picked || "—"}</p>
        </div>
      </div>
    );
  },
};

/** 50 messages a second: still one render per frame. */
export const Busy: Story = {
  render: () => {
    const { view, last } = useFakeBook(20, "", 50);
    return (
      <div className="w-80 rounded-2 border border-line-1">
        <OrderBook view={view} priceDecimals={1} qtyDecimals={4} lastPrice={last.price} lastDirection={last.dir} base="BTC" quote="USDT" />
      </div>
    );
  },
};

/** The mobile site: 12 levels, no toolbar, syncing after a sequence gap. */
export const MobileSyncing: Story = {
  render: () => {
    const { view, last } = useFakeBook(12, "");
    return (
      <div className="w-[360px] rounded-2 border border-line-1">
        <OrderBook view={view} priceDecimals={1} qtyDecimals={4} levels={12} toolbar={false} lastPrice={last.price} lastDirection={last.dir} syncing />
      </div>
    );
  },
};

export const Loading: Story = {
  render: () => (
    <div className="w-80 rounded-2 border border-line-1">
      <OrderBook view={empty} priceDecimals={1} qtyDecimals={4} levels={10} loading markPrice="63210.2" />
    </div>
  ),
};
