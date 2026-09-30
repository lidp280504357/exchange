import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Badge, ChangeBadge, Tag, type BadgeTone } from "./Badge";

const tones: BadgeTone[] = ["neutral", "brand", "up", "down", "info", "warn", "danger", "success"];

const meta = { title: "Base/Badge", component: Badge, args: { children: "交易中" } } satisfies Meta<typeof Badge>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Tones: Story = {
  render: () => (
    <div className="flex flex-col gap-3">
      {(["soft", "solid", "outline"] as const).map((variant) => (
        <div key={variant} className="flex flex-wrap items-center gap-2">
          {tones.map((tone) => (
            <Badge key={tone} tone={tone} variant={variant}>
              {tone}
            </Badge>
          ))}
        </div>
      ))}
    </div>
  ),
};

export const Status: Story = {
  render: () => (
    <div className="flex flex-wrap items-center gap-2">
      <Badge tone="success" dot>已完成</Badge>
      <Badge tone="warn" dot>审核中</Badge>
      <Badge tone="info" dot size="md">确认中 3/19</Badge>
      <Badge tone="danger" dot>已拒绝</Badge>
      <Badge tone="neutral" dot>已撤销</Badge>
    </div>
  ),
};

/** 24h change in market lists. */
export const Change: Story = {
  render: () => (
    <div className="flex items-center gap-2">
      <ChangeBadge value="0.0231" />
      <ChangeBadge value="-0.0112" />
      <ChangeBadge value="0" />
      <ChangeBadge value={null} />
      <ChangeBadge value="0.1532" variant="soft" />
    </div>
  ),
};

export const Tags: Story = {
  render: () => {
    const [tags, setTags] = useState(["Layer 1", "DeFi", "Meme"]);
    return (
      <div className="flex items-center gap-2">
        {tags.map((t) => (
          <Tag key={t} onRemove={() => setTags((all) => all.filter((x) => x !== t))}>
            {t}
          </Tag>
        ))}
        <Tag tone="brand" variant="soft">热门</Tag>
      </div>
    );
  },
};
