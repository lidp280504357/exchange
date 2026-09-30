import type { Meta, StoryObj } from "@storybook/react-vite";
import { AmountText } from "./AmountText";
import { CopyButton } from "./CopyButton";
import { KeyValue } from "./KeyValue";
import { TimeText } from "./TimeText";

const meta = {
  title: "Data/KeyValue",
  component: KeyValue,
  args: { items: [] },
} satisfies Meta<typeof KeyValue>;
export default meta;

type Story = StoryObj<typeof meta>;

/** A withdrawal summary: rows with copy buttons. */
export const Rows: Story = {
  render: () => (
    <div className="w-96">
      <KeyValue
        items={[
          { label: "币种", value: "USDT" },
          { label: "网络", value: "TRON (TRC20)" },
          { label: "地址", value: "TQn9Y2khEsLJW1ChVWFMSMeRDow5KcbLSE", copy: true },
          { label: "数量", value: <AmountText value="1250.5" decimals={2} asset="USDT" /> },
          { label: "手续费", value: <AmountText value="1" decimals={2} asset="USDT" />, hint: "按网络固定收取" },
          { label: "实际到账", value: <AmountText value="1249.5" decimals={2} asset="USDT" />, valueClassName: "font-semibold" },
          { label: "时间", value: <TimeText value="2026-09-30T10:13:12Z" format="datetimeSeconds" /> },
          { label: "追踪 ID", value: "5d65903b0824a7e4", copy: "5d65903b0824a7e47546ab8dbc6cf761" },
        ]}
      />
    </div>
  ),
};

/** Position fields in a grid. */
export const Grid: Story = {
  render: () => (
    <div className="w-[480px]">
      <KeyValue
        layout="grid"
        columns={3}
        items={[
          { label: "持仓数量", value: "0.250 BTC" },
          { label: "开仓均价", value: "62,810.5" },
          { label: "标记价格", value: "63,214.5" },
          { label: "强平价格", value: "51,020.0", valueClassName: "text-warn" },
          { label: "保证金", value: "785.13 USDT" },
          { label: "未实现盈亏", value: "+101.00 USDT", valueClassName: "text-up" },
        ]}
      />
    </div>
  ),
};

export const Copy: Story = {
  render: () => (
    <div className="flex items-center gap-4 text-sm">
      <CopyButton value="0192e4c7-5b1a-7c3e-9f00-6d2b8e11a3f4" />
      <CopyButton value="0192e4c7-5b1a-7c3e-9f00-6d2b8e11a3f4">复制订单号</CopyButton>
    </div>
  ),
};
