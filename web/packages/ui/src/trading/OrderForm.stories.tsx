import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Toaster, toast } from "../components/Toast";
import { fakeMarket } from "../fixtures";
import { OrderBook } from "./OrderBook";
import { OrderForm, type OrderFormValues } from "./OrderForm";
import type { OrderSide, OrderType, PairRules } from "./orderMath";

const pair: PairRules = {
  base: "BTC",
  quote: "USDT",
  tickSize: "0.1",
  lotSize: "0.0001",
  minQuantity: "0.0001",
  maxQuantity: "100",
  minNotional: "5",
  makerFeeRate: "0.001",
  takerFeeRate: "0.001",
};

const meta = {
  title: "Trading/OrderForm",
  component: OrderForm,
  args: {
    side: "BUY",
    onSideChange: () => {},
    type: "limit",
    onTypeChange: () => {},
    pair,
    onSubmit: () => {},
    signedIn: true,
  },
} satisfies Meta<typeof OrderForm>;
export default meta;

type Story = StoryObj<typeof meta>;

function describe(o: OrderFormValues): string {
  return [o.side, o.type, o.price && `@ ${o.price}`, o.quantity && `× ${o.quantity}`, o.quoteAmount && `for ${o.quoteAmount} USDT`].filter(Boolean).join(" ");
}

/** Price × quantity ↔ total, lot snapping, the slider, fee and limits. */
export const SignedIn: Story = {
  render: () => {
    const [side, setSide] = useState<OrderSide>("BUY");
    const [type, setType] = useState<OrderType>("limit");
    const [busy, setBusy] = useState(false);
    const [done, setDone] = useState(0);
    return (
      <div className="w-80 rounded-2 border border-line-1 bg-bg-1 p-4">
        <OrderForm
          side={side}
          onSideChange={setSide}
          type={type}
          onTypeChange={setType}
          pair={pair}
          available={{ base: "0.5321", quote: "1000" }}
          lastPrice="63214.5"
          signedIn
          submitting={busy}
          resetKey={done}
          onDeposit={() => toast.info("去充值")}
          onSubmit={(o) => {
            setBusy(true);
            setTimeout(() => {
              setBusy(false);
              setDone((n) => n + 1);
              toast.success("下单成功", { description: describe(o), action: { label: "查看委托", onClick: () => {} } });
            }, 700);
          }}
        />
        <Toaster />
      </div>
    );
  },
};

export const SignedOut: Story = {
  render: () => {
    const [side, setSide] = useState<OrderSide>("SELL");
    const [type, setType] = useState<OrderType>("market");
    return (
      <div className="w-80 rounded-2 border border-line-1 bg-bg-1 p-4">
        <OrderForm side={side} onSideChange={setSide} type={type} onTypeChange={setType} pair={pair} lastPrice="63214.5" signedIn={false} onSubmit={() => {}} onSignIn={() => toast.info("去登录")} />
        <Toaster />
      </div>
    );
  },
};

/** Clicking the book fills the price; Shift+click also the cumulative amount. */
export const WithOrderBook: Story = {
  render: () => {
    const [m] = useState(() => fakeMarket());
    const [side, setSide] = useState<OrderSide>("BUY");
    const [type, setType] = useState<OrderType>("limit");
    const [fill, setFill] = useState<{ price?: string; quantity?: string } | null>(null);
    return (
      <div className="flex gap-4">
        <div className="w-80 rounded-2 border border-line-1">
          <OrderBook
            view={m.book.view(12)}
            priceDecimals={1}
            qtyDecimals={4}
            levels={12}
            lastPrice="63214.5"
            base="BTC"
            quote="USDT"
            onPriceClick={(p, q) => setFill({ price: p, quantity: q })}
          />
        </div>
        <div className="w-80 rounded-2 border border-line-1 bg-bg-1 p-4">
          <OrderForm
            side={side}
            onSideChange={setSide}
            type={type}
            onTypeChange={setType}
            pair={pair}
            available={{ base: "2", quote: "50000" }}
            lastPrice="63214.5"
            signedIn
            fill={fill}
            onSubmit={(o) => toast.success(describe(o))}
          />
        </div>
        <Toaster />
      </div>
    );
  },
};
