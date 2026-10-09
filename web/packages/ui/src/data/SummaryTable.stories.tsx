import type { Meta, StoryObj } from "@storybook/react-vite";
import { Badge } from "../components/Badge";
import { AmountText } from "./AmountText";
import { KeyValue } from "./KeyValue";
import { KeyTag, ShortList, SummaryRow, SummaryTable, withKeys } from "./SummaryTable";

const meta = {
  title: "Data/SummaryTable",
  component: SummaryTable,
  args: { label: "上线检查清单", children: null },
} satisfies Meta<typeof SummaryTable>;
export default meta;

type Story = StoryObj<typeof meta>;

const funds: [string, string][] = [
  ["USDT", "1250000"], ["BTC", "12.5"], ["ETH", "180"], ["ASTRA", "99999.5"], ["SOL", "5000"], ["BNB", "800"], ["XRP", "200000"],
];

/** The launch checklist's items: a sentence each, the raw values once a row is opened (the insurance fund's open). */
export const Checklist: Story = {
  render: () => (
    <div className="w-[1100px]">
      <SummaryTable label="上线检查清单" headings={{ item: "项目", status: "状态", summary: "当前" }}>
        <SummaryRow
          title="测试模式"
          source="平台资料"
          status={{ tone: "danger", label: "阻塞" }}
          summary="开着：两站显示「测试模式」徽标与横幅"
          action={<span className="text-info-strong">去修改</span>}
          details={<span>上线应为：关（正式模式）</span>}
        />
        <SummaryRow
          title="提现总闸"
          source={withKeys("开关 wallet.withdraw")}
          status={{ tone: "success", label: "已就绪" }}
          summary={
            <span className="flex items-center gap-2">
              <Badge tone="success">开</Badge>
              <span className="text-fg-3">对所有人</span>
            </span>
          }
          details={<KeyTag>wallet.withdraw</KeyTag>}
        />
        <SummaryRow
          title="合约保险基金"
          source="derivatives-service 与账本"
          status={{ tone: "success", label: "已就绪" }}
          summary="开放中 U 本位 88、币本位 21；基金 23 种结算币，全部 > 0（最低 ASTRA 99,999.5）"
          defaultOpen
          details={
            <KeyValue
              layout="grid"
              columns={4}
              density="compact"
              items={funds.map(([asset, balance]) => ({ key: asset, label: asset, value: <AmountText value={balance} decimals={2} asset={asset} /> }))}
            />
          }
        />
        <SummaryRow
          title="HOUSE 报价与资金"
          source={withKeys("开关 market.house_liquidity 与 HOUSE 库存")}
          status={{ tone: "warn", label: "待处理" }}
          summary={
            <span>
              背书资产 <ShortList items={["USDT", "BTC", "ETH", "SOL", "BNB"]} />
            </span>
          }
        />
      </SummaryTable>
    </div>
  ),
};

/** Values without a check: no status column. */
export const Values: Story = {
  render: () => (
    <div className="w-[900px]">
      <SummaryTable label="行情源" noStatus headings={{ item: "项目", summary: "当前" }}>
        <SummaryRow title="最近收到" summary="3 秒前" />
        <SummaryRow
          title="跟随的交易对"
          summary={
            <span>
              50 个：
              <ShortList items={["BTC-USDT", "ETH-USDT", "SOL-USDT", ...Array.from({ length: 47 }, (_, i) => `C${i}-USDT`)]} />
            </span>
          }
          details={<span>BTC-USDT、ETH-USDT、SOL-USDT …</span>}
        />
      </SummaryTable>
    </div>
  ),
};

/** A record's labelled values (a deposit's, in a drawer): grey labels, lower rows, a copy button, a long value folded. */
export const Fields: Story = {
  render: () => (
    <div className="w-[520px]">
      <SummaryTable label="充值" variant="fields">
        <SummaryRow title="用户" summary={<KeyTag>0191f0c2-7e2a-7b6e-9c1d-5a4f3e2d1c0b</KeyTag>} />
        <SummaryRow title="金额" summary={<AmountText value="1250.5" asset="USDT" />} />
        <SummaryRow title="网络" summary="TRON" />
        <SummaryRow
          title="交易哈希"
          summary={<KeyTag>0x8f3c…a91e</KeyTag>}
          action={<span className="text-xs text-info-strong">复制</span>}
          details={<KeyTag>0x8f3c2b7d4e1a9c6f0b5d8e2a7c4f1b9e6d3a0c5f8b2e7d4a1c9f6b3e0d5a91e</KeyTag>}
        />
        <SummaryRow title="确认数" summary="19/19" />
      </SummaryTable>
    </div>
  ),
};

/** Compact, for half a page: narrower item and status columns. */
export const Compact: Story = {
  render: () => (
    <div className="w-[560px]">
      <SummaryTable label="对账" compact>
        <SummaryRow title="分录借贷平衡" source={<KeyTag>JOURNAL_BALANCED</KeyTag>} status={{ tone: "success", label: "一致" }} summary="一致" action={<span className="text-xs text-fg-3">01:20</span>} />
        <SummaryRow
          title="余额等于分录累计"
          source={<KeyTag>ACCOUNT_MATCHES_LINES</KeyTag>}
          status={{ tone: "danger", label: "不一致" }}
          summary="2 处不一致"
          action={<span className="text-xs text-fg-3">01:20</span>}
          details={
            <span className="flex flex-col gap-1">
              <span>
                <KeyTag>0191f0c2-…/USDT</KeyTag> 余额 10 ≠ 分录 9
              </span>
            </span>
          }
        />
      </SummaryTable>
    </div>
  ),
};
