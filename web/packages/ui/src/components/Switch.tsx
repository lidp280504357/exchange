import { Switch as RSwitch } from "radix-ui";
import { useId, type ReactNode } from "react";
import { cn } from "../lib/cn";

export type SwitchProps = {
  checked?: boolean;
  defaultChecked?: boolean;
  onCheckedChange?: (checked: boolean) => void;
  label?: ReactNode;
  description?: ReactNode;
  /** Put the label before the switch (settings rows). */
  labelFirst?: boolean;
  size?: "sm" | "md";
  disabled?: boolean;
  name?: string;
  id?: string;
  className?: string;
  "aria-label"?: string;
};

const sizes = {
  sm: { root: "h-4 w-7", thumb: "size-3 data-[state=checked]:translate-x-3" },
  md: { root: "h-5 w-9", thumb: "size-4 data-[state=checked]:translate-x-4" },
};

/** Switch is an on/off toggle for settings that apply at once. */
export function Switch({
  checked, defaultChecked, onCheckedChange, label, description, labelFirst, size = "md", disabled, name, id, className,
  "aria-label": ariaLabel,
}: SwitchProps) {
  const auto = useId();
  const switchId = id ?? auto;
  const s = sizes[size];
  const control = (
    <RSwitch.Root
      id={switchId}
      name={name}
      checked={checked}
      defaultChecked={defaultChecked}
      onCheckedChange={onCheckedChange}
      disabled={disabled}
      aria-label={ariaLabel}
      className={cn(
        "relative inline-flex shrink-0 cursor-pointer items-center rounded-full bg-bg-3 p-0.5 transition-colors duration-[var(--t-base)]",
        "data-[state=checked]:bg-brand disabled:cursor-not-allowed disabled:opacity-50",
        s.root,
        !label && className,
      )}
    >
      <RSwitch.Thumb
        className={cn("block rounded-full bg-white shadow-pop transition-transform duration-[var(--t-base)] ease-out", s.thumb)}
      />
    </RSwitch.Root>
  );
  if (!label) return control;
  return (
    <div className={cn("flex items-center gap-3", labelFirst && "justify-between", className)}>
      {!labelFirst && control}
      <label htmlFor={switchId} className={cn("cursor-pointer select-none text-sm text-fg-1", disabled && "cursor-not-allowed opacity-50")}>
        {/* The label stays on one line; a description below it may wrap. */}
        <span className="whitespace-nowrap">{label}</span>
        {description && <span className="block text-xs text-fg-3">{description}</span>}
      </label>
      {labelFirst && control}
    </div>
  );
}
