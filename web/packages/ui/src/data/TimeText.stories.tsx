import { timeZones, useSettings } from "@exchange/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Select } from "../components/Select";
import { TimeText } from "./TimeText";

const meta = { title: "Data/TimeText", component: TimeText, args: { value: "2026-09-30T10:13:12.016Z" } } satisfies Meta<typeof TimeText>;
export default meta;

type Story = StoryObj<typeof meta>;

/** Every format, in the zone chosen in the settings. */
export const Formats: Story = {
  render: (args) => {
    const zone = useSettings((s) => s.timeZone);
    const set = useSettings((s) => s.set);
    const zones = ["", "UTC", "Asia/Shanghai", "America/New_York"].filter((z) => z === "" || timeZones().includes(z));
    return (
      <div className="flex flex-col gap-3 text-sm">
        <Select
          size="sm"
          className="w-56"
          value={zone || "browser"}
          onValueChange={(v) => set({ timeZone: v === "browser" ? "" : v })}
          options={zones.map((z) => ({ value: z || "browser", label: z || "Browser" }))}
          aria-label="Time zone"
        />
        {(["datetime", "datetimeSeconds", "date", "time", "timeSeconds", "monthDay"] as const).map((f) => (
          <div key={f} className="flex gap-4">
            <span className="w-36 text-fg-3">{f}</span>
            <TimeText value={args.value} format={f} />
          </div>
        ))}
      </div>
    );
  },
};

/** Relative times share one 30-second clock. */
export const Relative: Story = {
  render: () => {
    const [base] = useState(() => Date.now());
    return (
      <div className="flex flex-col gap-2 text-sm">
        {[5, 90, 60 * 25, 3600 * 5, 86400 * 3].map((s) => (
          <TimeText key={s} value={base - s * 1000} relative />
        ))}
      </div>
    );
  },
};
