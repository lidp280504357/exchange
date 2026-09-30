import { dec, formatPercent } from "@exchange/core";
import { AmountText, CoinIcon, cn, ease } from "@exchange/ui";
import { Layers } from "lucide-react";
import { motion } from "motion/react";
import { useState } from "react";

export type AllocationSegment = {
  key: string;
  label: string;
  /** The coin, or null for the "others" slice. */
  asset: string | null;
  /** The value in USDT. */
  value: string;
  /** The share of the total, a fraction ("0.6230"). */
  share: string;
  /** The palette slot (0-4), or -1 for "others". */
  tone: number;
};

// The categorical order, checked with the dataviz palette validator on the
// dark card surface (bg-1): every ring of 2 to 5 slices, and the grey
// "others" slice next to them, clears the lightness band, the chroma floor,
// CVD separation (ΔE ≥ 9.2) and the normal-vision floor (ΔE ≥ 16.3). The
// chart-1..5 tokens fail as a categorical set there (brand and orange are
// too light; sky and fuchsia merge under deuteranopia), and the green and
// red of rises and falls stay out of the ring.
const STROKES = ["stroke-id-lime", "stroke-id-sky", "stroke-id-violet", "stroke-id-cyan", "stroke-id-blue"];
const SWATCHES = ["bg-id-lime", "bg-id-sky", "bg-id-violet", "bg-id-cyan", "bg-id-blue"];

const strokeOf = (tone: number) => (tone < 0 ? "stroke-fg-3" : (STROKES[tone % STROKES.length] ?? "stroke-fg-3"));
const swatchOf = (tone: number) => (tone < 0 ? "bg-fg-3" : (SWATCHES[tone % SWATCHES.length] ?? "bg-fg-3"));

const pct = (share: string) => formatPercent(share, 2, false);

/**
 * AllocationRing draws the asset mix as a ring (segments in the given
 * order from 12 o'clock, 2 px of card between them) beside its legend,
 * which is also the table view: every slice with its share and value.
 * Hovering or focusing a slice or its legend row shows it in the middle;
 * a click pins it.
 */
export function AllocationRing({ segments, title, size = 152, thickness = 14 }: { segments: AllocationSegment[]; title: string; size?: number; thickness?: number }) {
  const [hover, setHover] = useState<string | null>(null);
  const [pinned, setPinned] = useState<string | null>(null);
  // A slice that left (the balances changed) no longer dims the others.
  const active = [hover, pinned].find((k) => k !== null && segments.some((s) => s.key === k)) ?? null;
  const legend = [...segments].sort((a, b) => dec.cmp(b.value, a.value));
  const focus = segments.find((s) => s.key === active) ?? legend[0];

  const r = (size - thickness) / 2;
  const c = 2 * Math.PI * r;
  const gap = segments.length > 1 ? 2 : 0;
  let offset = 0;
  const arcs = segments.map((s) => {
    const len = dec.toNumber(s.share) * c;
    const arc = { ...s, start: offset, len: Math.max(len - gap, 0.75) };
    offset += len;
    return arc;
  });
  const summary = `${title}: ${legend.map((s) => `${s.label} ${pct(s.share)}`).join(", ")}`;
  const toggle = (key: string) => setPinned((p) => (p === key ? null : key));

  return (
    <div className="flex items-center gap-6">
      <div className="relative shrink-0" style={{ width: size, height: size }}>
        <motion.svg
          width={size}
          height={size}
          viewBox={`0 0 ${size} ${size}`}
          role="img"
          aria-label={summary}
          initial={{ opacity: 0, rotate: -45 }}
          animate={{ opacity: 1, rotate: 0 }}
          transition={{ duration: 0.45, ease }}
        >
          <g transform={`rotate(-90 ${size / 2} ${size / 2})`}>
            {arcs.map((a) => (
              <circle
                key={a.key}
                cx={size / 2}
                cy={size / 2}
                r={r}
                fill="none"
                strokeWidth={thickness}
                strokeDasharray={`${a.len} ${c}`}
                strokeDashoffset={-a.start}
                className={cn(strokeOf(a.tone), "cursor-pointer transition-opacity duration-[var(--t-base)]")}
                style={{ opacity: active && active !== a.key ? 0.28 : 1 }}
                onPointerEnter={() => setHover(a.key)}
                onPointerLeave={() => setHover(null)}
                onClick={() => toggle(a.key)}
              />
            ))}
          </g>
        </motion.svg>
        {focus && (
          <div aria-hidden className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center text-center">
            <span className="max-w-[96px] truncate text-xs text-fg-3">{focus.label}</span>
            <span className="text-lg font-semibold tabular-nums text-fg-1">{pct(focus.share)}</span>
            <AmountText value={focus.value} decimals={2} asset="USDT" className="text-xs text-fg-2" />
          </div>
        )}
      </div>
      <ul className="flex min-w-0 flex-1 flex-col gap-0.5">
        {legend.map((s) => (
          <li key={s.key}>
            <button
              type="button"
              aria-pressed={pinned === s.key}
              onPointerEnter={() => setHover(s.key)}
              onPointerLeave={() => setHover(null)}
              onFocus={() => setHover(s.key)}
              onBlur={() => setHover(null)}
              onClick={() => toggle(s.key)}
              className={cn(
                "flex w-full items-center gap-2.5 rounded-2 px-2 py-1.5 text-left transition-colors duration-[var(--t-fast)] hover:bg-bg-2",
                active === s.key && "bg-bg-2",
              )}
            >
              <span aria-hidden className={cn("size-2.5 shrink-0 rounded-full", swatchOf(s.tone))} />
              {s.asset ? (
                <CoinIcon symbol={s.asset} size={20} />
              ) : (
                <span aria-hidden className="grid size-5 shrink-0 place-items-center rounded-full bg-bg-3 text-fg-3">
                  <Layers size={11} />
                </span>
              )}
              <span className="flex min-w-0 flex-1 flex-col leading-tight">
                <span className="truncate text-sm text-fg-1">{s.label}</span>
                <AmountText value={s.value} decimals={2} className="truncate text-xs text-fg-3" />
              </span>
              <span className="shrink-0 text-sm tabular-nums text-fg-2">{pct(s.share)}</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
