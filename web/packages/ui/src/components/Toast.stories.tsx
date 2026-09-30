import type { Meta, StoryObj } from "@storybook/react-vite";
import { Button } from "./Button";
import { Toaster, toast } from "./Toast";

const meta = { title: "Feedback/Toast", component: Toaster, args: { position: "bottom-right" } } satisfies Meta<typeof Toaster>;
export default meta;

type Story = StoryObj<typeof meta>;

function Buttons() {
  return (
    <div className="flex flex-wrap gap-2">
      <Button
        size="sm"
        onClick={() => toast.success("下单成功", { description: "限价买入 0.0150 BTC", action: { label: "查看委托", onClick: () => {} } })}
      >
        成功
      </Button>
      <Button size="sm" variant="danger" onClick={() => toast.error("可用余额不足", { description: "trace 5d65903b0824" })}>
        错误
      </Button>
      <Button size="sm" variant="secondary" onClick={() => toast.info("新地址冷却中", { description: "24 小时后可提现到该地址" })}>
        提示
      </Button>
      <Button
        size="sm"
        variant="secondary"
        onClick={() => {
          const id = toast.info("提交中…", { duration: 0 });
          setTimeout(() => toast({ id, title: "已提交", tone: "success" }), 1200);
        }}
      >
        原地替换
      </Button>
      <Button size="sm" variant="ghost" onClick={() => toast.dismiss()}>
        全部关闭
      </Button>
    </div>
  );
}

/** PC site and console: bottom right; hover pauses the 4 s timer. */
export const Desktop: Story = {
  render: (args) => (
    <>
      <Buttons />
      <Toaster {...args} />
    </>
  ),
};

/** Mobile site: from the top, swipe up to dismiss. */
export const Mobile: Story = {
  args: { position: "top" },
  render: (args) => (
    <>
      <Buttons />
      <Toaster {...args} />
    </>
  ),
};
