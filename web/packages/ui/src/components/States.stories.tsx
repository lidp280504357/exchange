import type { Meta, StoryObj } from "@storybook/react-vite";
import { Button } from "./Button";
import { Skeleton, SkeletonLines } from "./Skeleton";
import { Spinner } from "./Spinner";
import { EmptyState, ErrorState } from "./States";

const meta = { title: "Base/States", component: EmptyState } satisfies Meta<typeof EmptyState>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Empty: Story = {
  args: { title: "暂无委托", description: "下单后你的委托会显示在这里", action: <Button size="sm">去交易</Button> },
};

export const EmptyCompact: Story = { args: { compact: true } };

export const Error: Story = {
  render: () => <ErrorState message="服务暂时不可用，请稍后重试" traceId="5d65903b0824a7e47546ab8dbc6cf761" onRetry={() => {}} />,
};

export const Loading: Story = {
  render: () => (
    <div className="flex w-80 flex-col gap-4">
      <div className="flex items-center gap-3">
        <Skeleton round className="size-10" />
        <SkeletonLines lines={2} className="flex-1" />
      </div>
      <SkeletonLines lines={4} />
      <Spinner size={20} className="text-brand" />
    </div>
  ),
};
