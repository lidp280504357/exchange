import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useState, type CSSProperties, type ReactNode } from "react";
import { Button } from "../components/Button";
import { Skeleton, SkeletonLines } from "../components/Skeleton";
import { cn } from "../lib/cn";

// The design system at a glance (design §5.1, §5.3): every colour token
// with the value it resolves to in the current theme, the text-only shades
// on each background of both themes with their contrast, radii, the type
// scale, the touch target, shadows and the motion presets. Switch theme and
// rise colour in the toolbar to review both.

type Swatch = { name: string; cls?: string; style?: CSSProperties; v: string; border?: boolean };

const groups: { title: string; items: Swatch[] }[] = [
  {
    title: "Background",
    items: [
      { name: "bg-0", cls: "bg-bg-0", v: "--bg-0", border: true },
      { name: "bg-1", cls: "bg-bg-1", v: "--bg-1", border: true },
      { name: "bg-2", cls: "bg-bg-2", v: "--bg-2" },
      { name: "bg-3", cls: "bg-bg-3", v: "--bg-3" },
    ],
  },
  {
    title: "Text",
    items: [
      { name: "fg-1", cls: "bg-fg-1", v: "--fg-1" },
      { name: "fg-2", cls: "bg-fg-2", v: "--fg-2" },
      { name: "fg-3", cls: "bg-fg-3", v: "--fg-3" },
    ],
  },
  {
    title: "Lines",
    items: [
      { name: "line-1", cls: "bg-line-1", v: "--line-1" },
      { name: "line-2", cls: "bg-line-2", v: "--line-2" },
    ],
  },
  {
    title: "Brand",
    items: [
      { name: "brand", cls: "bg-brand", v: "--brand" },
      { name: "brand-fg", cls: "bg-brand-fg", v: "--brand-fg", border: true },
      { name: "brand-soft", cls: "bg-brand-soft", v: "--brand-soft" },
      { name: "glow", cls: "bg-glow", v: "--glow" },
    ],
  },
  {
    title: "Rise and fall (data-updown swaps them)",
    items: [
      { name: "up", cls: "bg-up", v: "--up" },
      { name: "down", cls: "bg-down", v: "--down" },
      { name: "up-soft", style: { backgroundColor: "var(--up-soft)" }, v: "--up-soft" },
      { name: "down-soft", style: { backgroundColor: "var(--down-soft)" }, v: "--down-soft" },
    ],
  },
  {
    title: "Semantic",
    items: [
      { name: "info", cls: "bg-info", v: "--info" },
      { name: "warn", cls: "bg-warn", v: "--warn" },
      { name: "danger", cls: "bg-danger", v: "--danger" },
      { name: "success", cls: "bg-success", v: "--success" },
      { name: "overlay", cls: "bg-overlay", v: "--overlay", border: true },
    ],
  },
  {
    title: "Identity (letter icons)",
    items: [
      { name: "id-orange", cls: "bg-id-orange", v: "--id-orange" },
      { name: "id-amber", cls: "bg-id-amber", v: "--id-amber" },
      { name: "id-lime", cls: "bg-id-lime", v: "--id-lime" },
      { name: "id-emerald", cls: "bg-id-emerald", v: "--id-emerald" },
      { name: "id-teal", cls: "bg-id-teal", v: "--id-teal" },
      { name: "id-cyan", cls: "bg-id-cyan", v: "--id-cyan" },
      { name: "id-sky", cls: "bg-id-sky", v: "--id-sky" },
      { name: "id-blue", cls: "bg-id-blue", v: "--id-blue" },
      { name: "id-indigo", cls: "bg-id-indigo", v: "--id-indigo" },
      { name: "id-violet", cls: "bg-id-violet", v: "--id-violet" },
      { name: "id-fuchsia", cls: "bg-id-fuchsia", v: "--id-fuchsia" },
      { name: "id-rose", cls: "bg-id-rose", v: "--id-rose" },
    ],
  },
  {
    title: "Chart lines (MA 7/25/99, EMA 12/26)",
    items: [
      { name: "chart-1", cls: "bg-chart-1", v: "--chart-1" },
      { name: "chart-2", cls: "bg-chart-2", v: "--chart-2" },
      { name: "chart-3", cls: "bg-chart-3", v: "--chart-3" },
      { name: "chart-4", cls: "bg-chart-4", v: "--chart-4" },
      { name: "chart-5", cls: "bg-chart-5", v: "--chart-5" },
    ],
  },
];

const radii = [
  { name: "rounded-1 (4 px)", cls: "rounded-1" },
  { name: "rounded-2 (8 px)", cls: "rounded-2" },
  { name: "rounded-3 (12 px)", cls: "rounded-3" },
  { name: "rounded-full", cls: "rounded-full" },
];

const sizes = [
  { name: "text-xs · 12", cls: "text-xs" },
  { name: "text-sm · 13", cls: "text-sm" },
  { name: "text-base · 14", cls: "text-base" },
  { name: "text-md · 16", cls: "text-md" },
  { name: "text-lg · 20", cls: "text-lg" },
  { name: "text-xl · 24", cls: "text-xl" },
  { name: "text-2xl · 32", cls: "text-2xl" },
];

// useTokenValues reads what each token resolves to, again when the toolbar
// switches theme or rise colour.
function useTokenValues(): Record<string, string> {
  const read = () => {
    const css = getComputedStyle(document.documentElement);
    const out: Record<string, string> = {};
    for (const g of groups) for (const s of g.items) out[s.v] = css.getPropertyValue(s.v).trim();
    return out;
  };
  const [values, setValues] = useState(read);
  useEffect(() => {
    const mo = new MutationObserver(() => setValues(read()));
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme", "data-updown"] });
    return () => mo.disconnect();
  }, []);
  return values;
}

/** The text-only shades (text-*-strong), each a text colour that reads on every background. */
const strongs = ["up-strong", "down-strong", "brand-strong", "success-strong", "danger-strong", "info-strong", "warn-strong"] as const;
const strongText: Record<(typeof strongs)[number], string> = {
  "up-strong": "text-up-strong",
  "down-strong": "text-down-strong",
  "brand-strong": "text-brand-strong",
  "success-strong": "text-success-strong",
  "danger-strong": "text-danger-strong",
  "info-strong": "text-info-strong",
  "warn-strong": "text-warn-strong",
};
const backgrounds = ["bg-bg-0", "bg-bg-1", "bg-bg-2", "bg-bg-3"] as const;

/** contrastOf is the WCAG 2.x contrast of an element's text colour on its cell's background, as computed. */
function contrastOf(el: HTMLElement | null): string {
  if (!el) return "";
  const channels = (c: string) => (c.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number);
  const lum = (c: number[]) => {
    const f = (v: number) => ((v /= 255) <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
    return 0.2126 * f(c[0] ?? 0) + 0.7152 * f(c[1] ?? 0) + 0.0722 * f(c[2] ?? 0);
  };
  const [a, b] = [lum(channels(getComputedStyle(el).color)), lum(channels(getComputedStyle(el.parentElement ?? el).backgroundColor))].sort((x, y) => y - x);
  return `${((a! + 0.05) / (b! + 0.05)).toFixed(2)} : 1`;
}

/** StrongTable shows each text-only shade on bg-0..bg-3 of the theme given, with its contrast. */
function StrongTable({ theme }: { theme: "dark" | "light" }) {
  const [ratios, setRatios] = useState<Record<string, string>>({});
  const [root, setRoot] = useState<HTMLElement | null>(null);
  useEffect(() => {
    if (!root) return;
    const out: Record<string, string> = {};
    for (const el of root.querySelectorAll<HTMLElement>("[data-sample]")) out[el.dataset.sample!] = contrastOf(el);
    setRatios(out);
  }, [root]);
  return (
    <div ref={setRoot} data-theme={theme} className="overflow-hidden rounded-3 border border-line-1 bg-bg-0 p-3">
      <div className="mb-2 text-sm font-semibold text-fg-1">{theme}</div>
      <table className="w-full border-separate border-spacing-1 text-sm">
        <thead>
          <tr>
            <th className="text-left text-xs font-normal text-fg-3">token</th>
            {backgrounds.map((b) => (
              <th key={b} className="text-left text-xs font-normal text-fg-3">
                {b.replace("bg-bg-", "bg-")}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {strongs.map((name) => (
            <tr key={name}>
              <td className="font-mono text-xs text-fg-2">--{name}</td>
              {backgrounds.map((b) => (
                <td key={b} className={cn("rounded-1 px-2 py-1.5", b)}>
                  <span data-sample={`${name}/${b}`} className={cn("tabular-nums", strongText[name])}>
                    +2.31% 文字
                  </span>
                  <span className="ml-2 font-mono text-xs text-fg-3">{ratios[`${name}/${b}`]}</span>
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-md font-semibold text-fg-1">{title}</h2>
      {children}
    </section>
  );
}

const meta = { title: "Overview/Tokens", parameters: { layout: "fullscreen" } } satisfies Meta;
export default meta;

type Story = StoryObj;

export const Colours: Story = {
  render: () => {
    const values = useTokenValues();
    return (
      <div className="flex flex-col gap-8 p-6">
        {groups.map((g) => (
          <Section key={g.title} title={g.title}>
            <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3">
              {g.items.map((s) => (
                <div key={s.name} className="flex flex-col gap-1.5">
                  <div className={cn("h-14 rounded-2", s.cls, s.border && "border border-line-2")} style={s.style} />
                  <div className="text-sm text-fg-1">{s.name}</div>
                  <code className="font-mono text-xs text-fg-3">
                    {s.v} {values[s.v]}
                  </code>
                </div>
              ))}
            </div>
          </Section>
        ))}
      </div>
    );
  },
};

/** The text-only shades on every background of both themes, with their contrast (at least 4.5 : 1 each). */
export const TextShades: Story = {
  render: () => (
    <div className="flex flex-col gap-6 p-6">
      <Section title="Text-only shades (text-*-strong) on bg-0..bg-3">
        <div className="grid gap-4 xl:grid-cols-2">
          <StrongTable theme="dark" />
          <StrongTable theme="light" />
        </div>
      </Section>
    </div>
  ),
};

export const RadiiTypeShadow: Story = {
  render: () => (
    <div className="flex flex-col gap-8 p-6">
      <Section title="Radii">
        <div className="flex flex-wrap gap-4">
          {radii.map((r) => (
            <div key={r.cls} className="flex flex-col items-center gap-2">
              <div className={cn("size-20 border border-line-2 bg-bg-2", r.cls)} />
              <span className="text-xs text-fg-3">{r.name}</span>
            </div>
          ))}
        </div>
      </Section>
      <Section title="Type scale (Inter + system, tabular figures)">
        <div className="flex flex-col gap-2">
          {sizes.map((s) => (
            <div key={s.cls} className="flex items-baseline gap-6">
              <span className="w-32 shrink-0 text-xs text-fg-3">{s.name}</span>
              <span className={cn("text-fg-1", s.cls)}>行情 Markets 63,214.50 +2.31%</span>
            </div>
          ))}
          <div className="flex items-baseline gap-6">
            <span className="w-32 shrink-0 text-xs text-fg-3">font-mono</span>
            <span className="font-mono text-sm text-fg-1">0x3f5ce5fbfe3e9af3971dd833d26ba9b5c936f0be</span>
          </div>
        </div>
      </Section>
      <Section title="Touch target (--tap)">
        <div className="flex items-center gap-4">
          <div className="grid size-tap place-items-center rounded-2 border border-line-2 bg-bg-2 text-xs text-fg-2">44</div>
          <span className="text-xs text-fg-3">--tap 44 px: the smallest touch target (ui-checklist M1); the root font is 14 px, so size-11 is only 38.5 px</span>
        </div>
      </Section>
      <Section title="Shadow and layers">
        <div className="flex gap-6">
          <div className="grid h-24 w-48 place-items-center rounded-3 border border-line-1 bg-bg-2 text-sm text-fg-2 shadow-pop">shadow-pop</div>
          <ul className="text-xs text-fg-3">
            <li>--z-sticky 30</li>
            <li>--z-sheet 40</li>
            <li>--z-dialog 50</li>
            <li>--z-dropdown 60 (menus, lists, popovers)</li>
            <li>--z-toast 70 (toasts, tooltips)</li>
          </ul>
        </div>
      </Section>
    </div>
  ),
};

/** The motion presets (all off under prefers-reduced-motion). */
export const Motion: Story = {
  render: () => {
    const [flash, setFlash] = useState<{ n: number; dir: "up" | "down" }>({ n: 0, dir: "up" });
    const [replay, setReplay] = useState(0);
    return (
      <div className="flex flex-col gap-8 p-6">
        <Section title="Price flash (400 ms, restarted by remounting)">
          <div className="flex items-center gap-4">
            <span
              key={flash.n}
              className={cn(
                "rounded-1 px-2 py-1 text-xl tabular-nums",
                flash.n > 0 && (flash.dir === "up" ? "animate-flash-up text-up" : "animate-flash-down text-down"),
              )}
            >
              63,214.50
            </span>
            <Button size="sm" variant="buy" onClick={() => setFlash((f) => ({ n: f.n + 1, dir: "up" }))}>
              Flash up
            </Button>
            <Button size="sm" variant="sell" onClick={() => setFlash((f) => ({ n: f.n + 1, dir: "down" }))}>
              Flash down
            </Button>
          </div>
        </Section>
        <Section title="Skeleton shimmer (1.2 s loop)">
          <div className="flex w-96 items-center gap-3">
            <Skeleton round className="size-10" />
            <SkeletonLines lines={2} className="flex-1" />
          </div>
        </Section>
        <Section title="Entrances (replay)">
          <div className="flex flex-wrap items-center gap-6">
            <div key={`a${replay}`} className="animate-fade-up rounded-2 bg-bg-2 px-4 py-3 text-sm">fade-up 200 ms</div>
            <div key={`b${replay}`} className="animate-fade-in rounded-2 bg-bg-2 px-4 py-3 text-sm">fade-in 200 ms</div>
            <div className="overflow-hidden rounded-2">
              <div key={`c${replay}`} className="animate-slide-down bg-bg-2 px-4 py-3 text-sm">slide-down 150 ms</div>
            </div>
            <div key={`d${replay}`} className="animate-pop-in rounded-2 bg-bg-2 px-4 py-3 text-sm">pop-in 120 ms</div>
            <Button size="sm" variant="secondary" onClick={() => setReplay((n) => n + 1)}>
              Replay
            </Button>
          </div>
        </Section>
        <Section title="Loops">
          <div className="flex items-center gap-10">
            <span className="inline-block size-6 animate-spin rounded-full border-2 border-brand border-t-transparent" />
            <div className="relative h-24 w-48 overflow-hidden rounded-3 border border-line-1 bg-bg-1">
              <div className="absolute -left-6 -top-6 size-28 animate-float rounded-full bg-glow blur-2xl" />
              <span className="absolute bottom-2 left-3 text-xs text-fg-3">float 14 s</span>
            </div>
            <div className="w-72 overflow-hidden rounded-2 border border-line-1 py-2 mask-x-from-90%">
              <div className="flex w-max animate-marquee gap-8 text-sm">
                {["BTC 63,214.5", "ETH 2,614.32", "SOL 154.21", "BNB 581.4", "BTC 63,214.5", "ETH 2,614.32", "SOL 154.21", "BNB 581.4"].map((s, i) => (
                  <span key={`${s}-${i}`}>{s}</span>
                ))}
              </div>
            </div>
          </div>
        </Section>
      </div>
    );
  },
};
