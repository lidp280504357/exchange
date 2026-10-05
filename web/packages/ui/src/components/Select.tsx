import { Check, ChevronDown } from "lucide-react";
import { Select as RSelect } from "radix-ui";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";

export type SelectOption = {
  value: string;
  label: ReactNode;
  icon?: ReactNode;
  disabled?: boolean;
  /** A note beside the label in the list (why it is off, say), not in the trigger. */
  hint?: ReactNode;
};
export type SelectSize = "xs" | "sm" | "md" | "lg";

export type SelectProps = {
  value?: string;
  defaultValue?: string;
  onValueChange?: (value: string) => void;
  options: SelectOption[];
  placeholder?: ReactNode;
  /** What the trigger shows instead of the chosen option's label (the order book's "≈ 0.1"). */
  display?: ReactNode;
  size?: SelectSize;
  /** Borderless trigger for toolbars (order book step, chart interval). */
  variant?: "default" | "ghost";
  disabled?: boolean;
  invalid?: boolean;
  name?: string;
  id?: string;
  className?: string;
  contentClassName?: string;
  "aria-label"?: string;
  "aria-describedby"?: string;
};

const sizes: Record<SelectSize, string> = {
  xs: "h-6 gap-1 rounded-1 px-1.5 text-xs",
  sm: "h-8 gap-1.5 rounded-1 px-2.5 text-sm",
  md: "h-10 gap-2 rounded-2 px-3 text-base",
  lg: "h-12 gap-2 rounded-2 px-4 text-md",
};

/**
 * Select replaces every hand-written <select> (design §2): Radix Select
 * with the tokens, keyboard type-ahead and a checked item.
 */
export function Select({
  value, defaultValue, onValueChange, options, placeholder, display, size = "md", variant = "default", disabled, invalid, name, id, className,
  contentClassName, "aria-label": ariaLabel, "aria-describedby": describedBy,
}: SelectProps) {
  const { t } = useTranslation();
  return (
    <RSelect.Root value={value} defaultValue={defaultValue} onValueChange={onValueChange} disabled={disabled} name={name}>
      <RSelect.Trigger
        id={id}
        aria-label={ariaLabel}
        aria-describedby={describedBy}
        aria-invalid={invalid || undefined}
        className={cn(
          "inline-flex min-w-0 items-center justify-between whitespace-nowrap text-fg-1 outline-none transition-colors duration-[var(--t-fast)]",
          "focus-visible:ring-1 focus-visible:ring-brand disabled:cursor-not-allowed disabled:opacity-50 data-[placeholder]:text-fg-3",
          variant === "default" && "border bg-bg-2 hover:border-line-2",
          variant === "default" && (invalid ? "border-danger" : "border-line-1"),
          variant === "ghost" && "bg-transparent text-fg-2 hover:bg-bg-2 hover:text-fg-1",
          sizes[size],
          className,
        )}
      >
        <span className="flex min-w-0 items-center gap-2 truncate">
          <RSelect.Value placeholder={placeholder ?? t("ui.select")}>{display}</RSelect.Value>
        </span>
        <RSelect.Icon className="shrink-0 text-fg-3">
          <ChevronDown size={size === "xs" ? 12 : 14} />
        </RSelect.Icon>
      </RSelect.Trigger>
      <RSelect.Portal>
        <RSelect.Content
          position="popper"
          sideOffset={4}
          className={cn(
            "z-[var(--z-dropdown)] max-h-[var(--radix-select-content-available-height)] min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-2 border border-line-1 bg-bg-2 shadow-pop",
            "origin-(--radix-select-content-transform-origin) data-[state=open]:animate-pop-in data-[state=closed]:animate-pop-out",
            contentClassName,
          )}
        >
          <RSelect.Viewport className="p-1">
            {options.map((o) => (
              <RSelect.Item
                key={o.value}
                value={o.value}
                disabled={o.disabled}
                className={cn(
                  "relative flex cursor-pointer select-none items-center gap-2 rounded-1 py-1.5 pl-2 pr-7 text-fg-1 outline-none",
                  "data-[highlighted]:bg-bg-3 data-[disabled]:cursor-not-allowed data-[disabled]:opacity-50",
                  size === "xs" || size === "sm" ? "text-sm" : "text-base",
                )}
              >
                {o.icon}
                <RSelect.ItemText>{o.label}</RSelect.ItemText>
                {o.hint && <span className="text-xs text-fg-3">{o.hint}</span>}
                <RSelect.ItemIndicator className="absolute right-2 inline-flex text-brand">
                  <Check size={14} />
                </RSelect.ItemIndicator>
              </RSelect.Item>
            ))}
          </RSelect.Viewport>
        </RSelect.Content>
      </RSelect.Portal>
    </RSelect.Root>
  );
}
