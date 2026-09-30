import { Check, Minus } from "lucide-react";
import { Checkbox as RCheckbox } from "radix-ui";
import { useId, type ReactNode } from "react";
import { cn } from "../lib/cn";

export type CheckboxProps = {
  checked?: boolean | "indeterminate";
  defaultChecked?: boolean;
  onCheckedChange?: (checked: boolean) => void;
  label?: ReactNode;
  description?: ReactNode;
  disabled?: boolean;
  invalid?: boolean;
  name?: string;
  id?: string;
  className?: string;
  "aria-label"?: string;
};

/**
 * Checkbox with an optional label and description; "indeterminate" shows
 * a dash (a table's "select all" with some rows selected).
 */
export function Checkbox({
  checked, defaultChecked, onCheckedChange, label, description, disabled, invalid, name, id, className, "aria-label": ariaLabel,
}: CheckboxProps) {
  const auto = useId();
  const boxId = id ?? auto;
  const box = (
    <RCheckbox.Root
      id={boxId}
      name={name}
      checked={checked}
      defaultChecked={defaultChecked}
      disabled={disabled}
      aria-label={ariaLabel}
      aria-invalid={invalid || undefined}
      onCheckedChange={(c) => onCheckedChange?.(c === true)}
      className={cn(
        "grid size-4 shrink-0 place-items-center rounded-1 border bg-bg-2 text-brand-fg transition-colors duration-[var(--t-fast)]",
        "data-[state=checked]:border-brand data-[state=checked]:bg-brand data-[state=indeterminate]:border-brand data-[state=indeterminate]:bg-brand",
        "disabled:cursor-not-allowed disabled:opacity-50",
        invalid ? "border-danger" : "border-line-2 hover:border-fg-3",
        !label && className,
      )}
    >
      <RCheckbox.Indicator className="animate-fade-in">
        {checked === "indeterminate" ? <Minus size={12} strokeWidth={3} /> : <Check size={12} strokeWidth={3} />}
      </RCheckbox.Indicator>
    </RCheckbox.Root>
  );
  if (!label) return box;
  return (
    <div className={cn("flex items-start gap-2", className)}>
      <span className="flex h-5 items-center">{box}</span>
      <label htmlFor={boxId} className={cn("cursor-pointer select-none text-sm leading-5 text-fg-1", disabled && "cursor-not-allowed opacity-50")}>
        {label}
        {description && <span className="block text-xs text-fg-3">{description}</span>}
      </label>
    </div>
  );
}
