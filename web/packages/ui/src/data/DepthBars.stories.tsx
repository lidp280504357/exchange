import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useState } from "react";
import { DepthBars } from "./DepthBars";

const meta = { title: "Data/DepthBars", component: DepthBars, args: { value: "3", max: "10", side: "buy" } } satisfies Meta<typeof DepthBars>;
export default meta;

type Story = StoryObj<typeof meta>;

/** Bars move with a 200 ms transform transition as amounts change. */
export const Live: Story = {
  render: () => {
    const [vals, setVals] = useState([2, 5, 8, 3, 9, 6]);
    useEffect(() => {
      const id = setInterval(() => setVals((v) => v.map(() => Math.round(Math.random() * 10))), 800);
      return () => clearInterval(id);
    }, []);
    return (
      <div className="flex w-72 flex-col gap-1">
        {vals.map((v, i) => (
          <div key={`row-${i}`} className="relative h-5 text-xs leading-5">
            <DepthBars value={v} max={10} side={i < 3 ? "sell" : "buy"} />
            <span className="relative px-2 tabular-nums">{v}</span>
          </div>
        ))}
      </div>
    );
  },
};

export const FromLeft: Story = {
  render: () => (
    <div className="relative h-6 w-72 text-xs leading-6">
      <DepthBars ratio={0.6} side="buy" align="left" />
      <span className="relative px-2">60%</span>
    </div>
  ),
};
