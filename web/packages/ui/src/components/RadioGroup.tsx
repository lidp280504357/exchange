import { RadioGroup as RRadio } from "radix-ui";
import { useId, type ReactNode } from "react";
import { cn } from "../lib/cn";

export type RadioOption = { value: string; label: ReactNode; description?: ReactNode; disabled?: boolean };

export type RadioGroupProps = {
  value?: string;
  defaultValue?: string;
  onValueChange?: (value: string) => void;
  options: RadioOption[];
  orientation?: "horizontal" | "vertical";
  /** Cards with a border instead of bare dots (network choice). */
  variant?: "default" | "card";
  disabled?: boolean;
  name?: string;
  className?: string;
  "aria-label"?: string;
};

/** RadioGroup: one choice of a few, arrow keys move between them. */
export function RadioGroup({
  value, defaultValue, onValueChange, options, orientation = "vertical", variant = "default", disabled, name, className,
  "aria-label": ariaLabel,
}: RadioGroupProps) {
  const id = useId();
  return (
    <RRadio.Root
      value={value}
      defaultValue={defaultValue}
      onValueChange={onValueChange}
      orientation={orientation}
      disabled={disabled}
      name={name}
      aria-label={ariaLabel}
      className={cn("flex gap-3", orientation === "vertical" ? "flex-col" : "flex-row flex-wrap", className)}
    >
      {options.map((o) => {
        const itemId = `${id}-${o.value}`;
        const dot = (
          <RRadio.Item
            id={itemId}
            value={o.value}
            disabled={o.disabled}
            className={cn(
              "grid size-4 shrink-0 place-items-center rounded-full border border-line-2 bg-bg-2 transition-colors",
              "hover:border-fg-3 data-[state=checked]:border-brand disabled:cursor-not-allowed disabled:opacity-50",
              // A card is its whole label; a bare dot gets a 44 px touch target (hit-area) on touch screens.
              variant === "card" ? "absolute right-3 top-3" : "hit-area",
            )}
          >
            <RRadio.Indicator className="block size-2 animate-fade-in rounded-full bg-brand" />
          </RRadio.Item>
        );
        return variant === "card" ? (
          <label
            key={o.value}
            htmlFor={itemId}
            className={cn(
              "relative flex cursor-pointer flex-col gap-1 rounded-2 border border-line-1 bg-bg-2 p-3 pr-9 transition-colors hover:border-line-2",
              "has-[[data-state=checked]]:border-brand has-[[data-state=checked]]:bg-brand-soft",
              o.disabled && "cursor-not-allowed opacity-50",
            )}
          >
            {dot}
            <span className="text-sm font-medium text-fg-1">{o.label}</span>
            {/* fg-2: a checked card's brand tint takes fg-3 under 4.5:1. */}
            {o.description && <span className="text-xs text-fg-2">{o.description}</span>}
          </label>
        ) : (
          <div key={o.value} className="flex items-start gap-2">
            <span className="flex h-5 items-center">{dot}</span>
            <label htmlFor={itemId} className={cn("cursor-pointer select-none text-sm leading-5 text-fg-1", o.disabled && "cursor-not-allowed opacity-50")}>
              {o.label}
              {o.description && <span className="block text-xs text-fg-3">{o.description}</span>}
            </label>
          </div>
        );
      })}
    </RRadio.Root>
  );
}
