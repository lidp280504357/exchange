import { dec } from "@exchange/core";
import { Minus, Plus } from "lucide-react";
import { useState, type ChangeEvent, type FocusEvent, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";
import { Input, type InputProps } from "./Input";
import { Slider, type SliderTone } from "./Slider";

// Amounts typed by people (design §5.2): the value is a decimal string end
// to end; digits beyond the precision are trimmed as they are typed, the
// +/- buttons move along the step's grid with exact decimal arithmetic,
// and "Max" or the percent slider never round up past the maximum.

/**
 * sanitizeDecimal cleans typed or pasted text into a decimal string with at
 * most `decimals` places: full-width digits and Chinese points are
 * accepted, thousands separators dropped, leading zeros removed. It returns
 * null for text that is not a number, which the field then ignores.
 */
export function sanitizeDecimal(raw: string, decimals: number): string | null {
  let s = raw
    .replace(/[０-９]/g, (c) => String.fromCharCode(c.charCodeAt(0) - 0xfee0))
    .replace(/[。．]/g, ".")
    .replace(/\s/g, "");
  if (s.includes(".")) s = s.replaceAll(",", "");
  else if ((s.match(/,/g) ?? []).length === 1 && !/,\d{3}$/.test(s)) s = s.replace(",", ".");
  else s = s.replaceAll(",", "");
  if (s === "") return "";
  if (!/^\d*\.?\d*$/.test(s)) return null;
  if (s.startsWith(".")) s = `0${s}`;
  s = s.replace(/^0+(?=\d)/, "");
  const [int = "0", frac] = s.split(".");
  if (frac === undefined) return int;
  if (decimals <= 0) return int;
  return `${int}.${frac.slice(0, decimals)}`;
}

/**
 * stepValue moves a value one step along the step's grid: up to the next
 * multiple above, down to the next multiple below, within [min, max].
 */
export function stepValue(value: string, step: string, dir: 1 | -1, min = "0", max?: string | null): string {
  const base = value && dec.isDecimal(value) ? value : min;
  let next = dir > 0 ? dec.add(dec.quantize(base, step, "down"), step) : dec.sub(dec.quantize(base, step, "up"), step);
  if (dec.lt(next, min)) next = min;
  if (max && dec.isDecimal(max) && dec.gt(next, max)) next = dec.quantize(max, step, "down");
  return dec.normalize(next);
}

/**
 * percentOf returns pct % of max, rounded down to decimals and then to a
 * multiple of step: the slider and "Max" never ask for more than there is.
 */
export function percentOf(max: string, pct: number, decimals: number, step?: string): string {
  if (!dec.isDecimal(max) || dec.sign(max) <= 0 || pct <= 0) return "0";
  const whole = Math.min(100, Math.round(pct));
  let v = dec.div(dec.mul(max, String(whole)), "100", decimals, "down");
  if (step && dec.isDecimal(step) && dec.sign(step) > 0) v = dec.quantize(v, step, "down");
  return dec.normalize(v);
}

/** ratioPercent is value / max in percent, for drawing the slider (0-100). */
export function ratioPercent(value: string, max: string | null | undefined): number {
  if (!max || !dec.isDecimal(max) || dec.sign(max) <= 0 || !value || !dec.isDecimal(value)) return 0;
  return Math.min(100, Math.max(0, dec.toNumber(dec.div(value, max, 6)) * 100));
}

export type NumberInputProps = Omit<
  InputProps,
  "value" | "defaultValue" | "onChange" | "onValueChange" | "type" | "inputMode" | "min" | "max" | "step"
> & {
  /** The value: a decimal string, "" when empty. */
  value: string;
  onValueChange: (value: string) => void;
  /** Places kept while typing (default: the step's, else 8). */
  decimals?: number;
  /** The +/- buttons and arrow keys move by this step. */
  step?: string;
  /** The floor of the step buttons (default "0"). */
  min?: string;
  /** The ceiling: shows a "Max" button (and bounds the slider). */
  max?: string | null;
  /** Hide the Max button while keeping max for the slider. */
  maxButton?: boolean;
  /** A 0/25/50/75/100 % slider under the field (needs max). */
  slider?: boolean;
  sliderTone?: SliderTone;
  /** Snap to a multiple of step when the field loses focus. */
  snap?: boolean;
  align?: "left" | "right";
};

const MARKS = [0, 25, 50, 75, 100];

/**
 * NumberInput is the amount field: a decimal string with a fixed
 * precision, optional step buttons, a Max button and a percent slider.
 * It opens the decimal keypad on phones (inputMode="decimal").
 */
export function NumberInput({
  value, onValueChange, decimals, step, min = "0", max, maxButton = true, slider, sliderTone = "brand", snap, align = "left",
  suffix, unit, disabled, readOnly, onBlur, onKeyDown, className, containerClassName, ...rest
}: NumberInputProps) {
  const { t } = useTranslation();
  const places = decimals ?? (step ? dec.decimalsOf(step) : 8);
  const [dragPct, setDragPct] = useState<number | null>(null);
  const hasMax = Boolean(max && dec.isDecimal(max));
  const editable = !disabled && !readOnly;

  const change = (e: ChangeEvent<HTMLInputElement>) => {
    const next = sanitizeDecimal(e.target.value, places);
    if (next !== null && next !== value) onValueChange(next);
  };

  const move = (dir: 1 | -1) => {
    if (!step || !editable) return;
    onValueChange(stepValue(value, step, dir, min, max));
  };

  const blur = (e: FocusEvent<HTMLInputElement>) => {
    let v = value;
    if (v.endsWith(".")) v = v.slice(0, -1);
    if (v && snap && step && dec.isDecimal(v)) v = dec.normalize(dec.quantize(v, step, "down"));
    if (v !== value) onValueChange(v);
    onBlur?.(e);
  };

  const keyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    onKeyDown?.(e);
    if (e.defaultPrevented || !step) return;
    if (e.key === "ArrowUp" || e.key === "ArrowDown") {
      e.preventDefault();
      move(e.key === "ArrowUp" ? 1 : -1);
    }
  };

  const setMax = () => {
    if (!max || !editable) return;
    onValueChange(percentOf(max, 100, places, step));
  };

  const stepper = step ? (
    <span className="ml-1 flex items-center border-l border-line-1 pl-1">
      <button
        type="button"
        tabIndex={-1}
        aria-label={t("ui.decrease")}
        disabled={!editable}
        onClick={() => move(-1)}
        className="grid size-7 place-items-center rounded-1 text-fg-3 hover:bg-bg-3 hover:text-fg-1 disabled:opacity-50"
      >
        <Minus size={14} />
      </button>
      <button
        type="button"
        tabIndex={-1}
        aria-label={t("ui.increase")}
        disabled={!editable}
        onClick={() => move(1)}
        className="grid size-7 place-items-center rounded-1 text-fg-3 hover:bg-bg-3 hover:text-fg-1 disabled:opacity-50"
      >
        <Plus size={14} />
      </button>
    </span>
  ) : null;

  const maxBtn =
    hasMax && maxButton ? (
      <button
        type="button"
        disabled={!editable}
        onClick={setMax}
        className="mr-2 shrink-0 text-xs font-medium text-brand hover:brightness-110 disabled:opacity-50"
      >
        {t("ui.max")}
      </button>
    ) : null;

  const pct = dragPct ?? ratioPercent(value, max);

  return (
    <div className={cn("w-full", containerClassName)}>
      <Input
        {...rest}
        type="text"
        inputMode="decimal"
        autoComplete="off"
        spellCheck={false}
        value={value}
        disabled={disabled}
        readOnly={readOnly}
        onChange={change}
        onBlur={blur}
        onKeyDown={keyDown}
        className={cn("tabular-nums", align === "right" && "text-right", className)}
        unit={
          maxBtn || unit !== undefined ? (
            <span className="flex items-center">
              {maxBtn}
              {unit}
            </span>
          ) : undefined
        }
        suffix={
          stepper || suffix !== undefined ? (
            <span className="flex items-center">
              {suffix}
              {stepper}
            </span>
          ) : undefined
        }
      />
      {slider && hasMax && (
        <Slider
          className="mt-3 px-1"
          value={pct}
          min={0}
          max={100}
          step={1}
          marks={MARKS}
          markLabels
          formatMark={(m) => `${m}%`}
          formatValue={(v) => `${Math.round(v)}%`}
          tone={sliderTone}
          disabled={!editable}
          aria-label={typeof rest["aria-label"] === "string" ? rest["aria-label"] : undefined}
          onValueChange={(p) => {
            setDragPct(p);
            if (max) onValueChange(p <= 0 ? "" : percentOf(max, p, places, step));
          }}
          onValueCommit={() => setDragPct(null)}
        />
      )}
    </div>
  );
}
