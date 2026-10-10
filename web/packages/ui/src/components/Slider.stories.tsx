import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useRef, useState } from "react";
import { Slider } from "./Slider";

const meta = {
  title: "Base/Slider",
  component: Slider,
  args: { value: 25, onValueChange: () => {} },
  decorators: [(Story) => <div className="w-80 py-4"><Story /></div>],
} satisfies Meta<typeof Slider>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Percent: Story = {
  render: () => {
    const [v, setV] = useState(25);
    return (
      <Slider
        value={v}
        onValueChange={setV}
        marks={[0, 25, 50, 75, 100]}
        markLabels
        formatMark={(m) => `${m}%`}
        formatValue={(x) => `${x}%`}
        aria-label="Percent of balance"
      />
    );
  },
};

export const Tones: Story = {
  render: () => {
    const [a, setA] = useState(40);
    const [b, setB] = useState(70);
    return (
      <div className="flex flex-col gap-6">
        <Slider value={a} onValueChange={setA} tone="up" marks={[0, 25, 50, 75, 100]} aria-label="Buy" />
        <Slider value={b} onValueChange={setB} tone="down" marks={[0, 25, 50, 75, 100]} aria-label="Sell" />
      </div>
    );
  },
};

/**
 * The thumb's centre is the value (B173): at 0% and 100% the dot's centre
 * sits on the track's ends, as the fill's end and the marks do. The last
 * slider plays its arrival effect each time it reaches 100% (the buttons
 * replay it; B176): the glow charges along the fill and flares at the end,
 * two soft bands of light run back across each other with their
 * afterglow, the marks glow as they pass, sparks fly, the glow breathes
 * once and settles; one soft glow under reduced motion.
 */
export const EndsAndPeak: Story = {
  render: () => {
    const ref = useRef<HTMLDivElement>(null);
    const [v, setV] = useState(75);
    return (
      <div className="flex flex-col gap-6">
        {[0, 50, 100].map((at) => (
          <Slider key={at} value={at} onValueChange={() => {}} pulseAtMax={false} marks={[0, 25, 50, 75, 100]} markLabels formatMark={(m) => `${m}%`} aria-label={`${at}%`} />
        ))}
        <div ref={ref}>
          <Slider value={v} onValueChange={setV} tone="up" marks={[0, 25, 50, 75, 100]} markLabels formatMark={(m) => `${m}%`} aria-label="Pulse at 100%" />
        </div>
        <div className="flex gap-2 text-xs">
          <button type="button" className="rounded border border-line-2 px-2 py-1" onClick={() => pressEnd(ref.current)}>100%</button>
          <button type="button" className="rounded border border-line-2 px-2 py-1" onClick={() => setV(75)}>75%</button>
        </div>
      </div>
    );
  },
};

const marks = [0, 25, 50, 75, 100];

// Only an arrival the user made plays (B183): the stories press End on the
// sliders' thumbs, as a keyboard would, instead of setting the value.
function pressEnd(root: HTMLElement | null) {
  for (const thumb of root?.querySelectorAll<HTMLElement>("[role=slider]") ?? []) {
    thumb.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true }));
  }
}

/**
 * The arrival in slow motion (B176): every animation under the story at
 * the chosen rate, the brand, rise and fall tones at once.
 */
export const ArrivalSlowMotion: Story = {
  render: () => {
    const ref = useRef<HTMLDivElement>(null);
    const [v, setV] = useState(75);
    const [rate, setRate] = useState(0.2);
    useEffect(() => {
      let raf = 0;
      const slow = () => {
        for (const a of ref.current?.getAnimations({ subtree: true }) ?? []) if (a.playbackRate !== rate) a.playbackRate = rate;
        raf = requestAnimationFrame(slow);
      };
      raf = requestAnimationFrame(slow);
      return () => cancelAnimationFrame(raf);
    }, [rate]);
    const replay = () => {
      setV(75);
      requestAnimationFrame(() => pressEnd(ref.current));
    };
    return (
      <div ref={ref} className="flex flex-col gap-6">
        {(["brand", "up", "down"] as const).map((tone) => (
          <Slider key={tone} value={v} onValueChange={setV} tone={tone} marks={marks} markLabels formatMark={(m) => `${m}%`} aria-label={tone} />
        ))}
        <div className="flex gap-2 text-xs">
          <button type="button" className="rounded border border-line-2 px-2 py-1" onClick={replay}>
            Replay
          </button>
          {[0.1, 0.2, 0.5, 1].map((r) => (
            <button
              key={r}
              type="button"
              className={`rounded border px-2 py-1 ${r === rate ? "border-brand text-brand" : "border-line-2"}`}
              onClick={() => setRate(r)}
            >
              {r}x
            </button>
          ))}
        </div>
      </div>
    );
  },
};

/** One frame of the arrival: a slider that reached 100%, paused at `at` ms. */
function ArrivalFrame({ at }: { at: number }) {
  const ref = useRef<HTMLDivElement>(null);
  const [v, setV] = useState(75);
  useEffect(() => {
    const root = ref.current;
    if (!root) return;
    // Paused at `at` as soon as the layers are in the page: an observer,
    // not animation frames, which a hidden tab does not run (the effect
    // would play through and be gone).
    const seek = () => {
      const all = root.getAnimations({ subtree: true });
      for (const a of all) {
        a.pause();
        a.currentTime = at;
      }
      return all.length > 0;
    };
    const watch = new MutationObserver(() => seek() && watch.disconnect());
    watch.observe(root, { childList: true, subtree: true });
    pressEnd(root);
    return () => watch.disconnect();
  }, [at]);
  return (
    <div className="flex items-center gap-4">
      <span className="w-16 shrink-0 text-right font-mono text-xs text-fg-3">{at} ms</span>
      <div ref={ref} className="w-72">
        <Slider value={v} onValueChange={setV} marks={marks} aria-label={`${at} ms`} />
      </div>
    </div>
  );
}

/** The arrival frame by frame, every 50 ms from 0 to 1,400 (B176): the review's filmstrip. */
export const ArrivalFrames: Story = {
  render: () => (
    <div className="flex flex-col gap-2">
      {Array.from({ length: 29 }, (_, i) => i * 50).map((at) => (
        <ArrivalFrame key={at} at={at} />
      ))}
    </div>
  ),
};

/**
 * In the boxes the order forms sit in (B194): a PC terminal's order panel
 * (290 px, p-3) and a phone's order sheet (390 px, px-4), both scrolling.
 * The arrival's light stays inside them: neither scrolls sideways while it
 * plays (the browser smokes measure the real ones).
 */
export const InPanels: Story = {
  render: () => {
    const [a, setA] = useState(75);
    const [b, setB] = useState(75);
    return (
      <div className="flex flex-col gap-6">
        <div data-testid="pc-panel" className="w-[290px] overflow-y-auto bg-bg-1 p-3">
          <Slider className="px-1" value={a} onValueChange={setA} marks={marks} markLabels formatMark={(m) => `${m}%`} aria-label="PC order panel" />
        </div>
        <div data-testid="m-sheet" className="w-[390px] overflow-y-auto bg-bg-1 px-4 pb-4">
          <Slider className="px-1" value={b} onValueChange={setB} tone="up" marks={marks} markLabels formatMark={(m) => `${m}%`} aria-label="Phone order sheet" />
        </div>
      </div>
    );
  },
};

export const Leverage: Story = {
  render: () => {
    const [v, setV] = useState(10);
    return (
      <Slider
        value={v}
        onValueChange={setV}
        min={1}
        max={125}
        marks={[1, 25, 50, 75, 100, 125]}
        markLabels
        formatMark={(m) => `${m}x`}
        aria-label="Leverage"
      />
    );
  },
};
