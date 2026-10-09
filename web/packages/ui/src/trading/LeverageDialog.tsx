import { TriangleAlert } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Dialog } from "../components/Dialog";
import { NumberInput } from "../components/NumberInput";
import { Slider } from "../components/Slider";
import { cn } from "../lib/cn";

export type LeverageDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The current leverage. */
  value: number;
  /** The contract's maximum (its first risk tier). */
  max: number;
  min?: number;
  onConfirm: (leverage: number) => void;
  submitting?: boolean;
  /** Marks on the slider; defaults to a spread up to max. */
  marks?: number[];
  /** Above this a risk warning shows (default 20, design §6.2). */
  riskAbove?: number;
  /** A line under the slider for the chosen leverage (the tier's max position). */
  info?: (leverage: number) => ReactNode;
  /** The contract, shown in the title ("BTC-USDT-PERP"). */
  symbol?: string;
};

/** leverageMarks spreads at most six marks from min to max (1, 25, 50 … 125 for 125×). */
export function leverageMarks(min: number, max: number): number[] {
  if (max - min <= 5) return Array.from({ length: max - min + 1 }, (_, i) => min + i);
  const step = [2, 5, 10, 20, 25, 50].find((n) => (max - min) / n <= 5) ?? Math.ceil((max - min) / 5);
  const out = [min];
  for (let v = step; v < max; v += step) if (v > min) out.push(v);
  out.push(max);
  return out;
}

/**
 * LeverageDialog adjusts a contract's leverage: a slider from 1× to the
 * maximum with marks, an exact input, and a warning above 20×. The value
 * applies only on confirm.
 */
export function LeverageDialog({
  open, onOpenChange, value, max, min = 1, onConfirm, submitting, marks, riskAbove = 20, info, symbol,
}: LeverageDialogProps) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(String(value));

  // Each opening starts from the current leverage.
  const valueRef = useRef(value);
  valueRef.current = value;
  useEffect(() => {
    if (open) setDraft(String(valueRef.current));
  }, [open]);

  const n = Math.round(Number(draft));
  const valid = draft !== "" && Number.isFinite(n) && n >= min && n <= max;
  const lev = valid ? n : Math.min(max, Math.max(min, Number.isFinite(n) ? n : min));
  const risky = lev > riskAbove;

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={symbol ? `${t("ui.leverage.title")} · ${symbol}` : t("ui.leverage.title")}
      description={t("ui.leverage.current", { n: value })}
      size="sm"
      onConfirm={() => valid && onConfirm(n)}
      confirmDisabled={!valid || n === value}
      confirmLoading={submitting}
    >
      <div className="flex flex-col gap-5">
        <NumberInput
          aria-label={t("ui.leverage.label")}
          value={draft}
          onValueChange={setDraft}
          decimals={0}
          step="1"
          min={String(min)}
          max={String(max)}
          maxButton={false}
          align="right"
          size="lg"
          unit="x"
          error={draft !== "" && !valid ? `${min}x – ${max}x` : undefined}
        />
        <Slider
          value={lev}
          onValueChange={(v) => setDraft(String(v))}
          min={min}
          max={max}
          step={1}
          marks={marks ?? leverageMarks(min, max)}
          markLabels
          formatMark={(m) => `${m}x`}
          formatValue={(v) => `${v}x`}
          tone={risky ? "down" : "brand"}
          pulseAtMax={false}
          aria-label={t("ui.leverage.label")}
        />
        {info && <div className="text-xs text-fg-3">{info(lev)}</div>}
        <div
          className={cn(
            "flex gap-2 rounded-2 border p-3 text-xs transition-opacity duration-[var(--t-base)]",
            risky ? "border-warn/40 bg-warn/10 text-warn opacity-100" : "pointer-events-none border-transparent opacity-0",
          )}
          aria-hidden={!risky}
          role={risky ? "alert" : undefined}
        >
          <TriangleAlert size={14} className="mt-0.5 shrink-0" />
          <span>{t("ui.leverage.risk")}</span>
        </div>
      </div>
    </Dialog>
  );
}
