import { Slot } from "radix-ui";
import type { ButtonHTMLAttributes, ReactNode } from "react";
import { cn } from "../lib/cn";
import { Spinner } from "./Spinner";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger" | "buy" | "sell";
export type ButtonSize = "sm" | "md" | "lg";

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant;
  size?: ButtonSize;
  /** Shows a spinner and blocks clicks while an action runs. */
  loading?: boolean;
  /** An icon before the label. */
  icon?: ReactNode;
  block?: boolean;
  /** Renders the child element (a link) with the button's look. */
  asChild?: boolean;
};

// The rise, fall and danger fills carry black text: white reads at 2.1,
// 3.5 and 3.8:1 on them, black at 9.9, 6.0 and 5.6:1.
const variants: Record<ButtonVariant, string> = {
  primary: "bg-brand text-brand-fg hover:brightness-110",
  secondary: "bg-bg-3 text-fg-1 hover:bg-line-2",
  ghost: "bg-transparent text-fg-2 hover:bg-bg-2 hover:text-fg-1",
  danger: "bg-danger text-black hover:brightness-110",
  buy: "bg-up text-black hover:brightness-110",
  sell: "bg-down text-black hover:brightness-110",
};

// On touch screens the medium and large buttons are 44 px touch targets
// (the 14 px root makes h-12 42 px); a small one takes hit-area where it
// needs one.
const sizes: Record<ButtonSize, string> = {
  sm: "h-8 px-3 text-sm gap-1.5 rounded-1",
  md: "h-10 px-4 text-base gap-2 rounded-2 pointer-coarse:min-h-tap",
  lg: "h-12 px-6 text-md gap-2 rounded-2 pointer-coarse:min-h-tap",
};

/**
 * Button: primary (brand), secondary, ghost, danger, and the trade sides
 * buy/sell, in three sizes. Presses shrink to 0.98 with CSS (no JS timer).
 */
export function Button({
  variant = "primary", size = "md", loading = false, icon, block, asChild, className, children, disabled, ...rest
}: ButtonProps) {
  const Comp = asChild ? Slot.Root : "button";
  return (
    <Comp
      {...rest}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      className={cn(
        "inline-flex select-none items-center justify-center whitespace-nowrap font-medium transition-[filter,background-color,color,transform] duration-[var(--t-fast)] ease-out active:scale-[0.98] disabled:cursor-not-allowed disabled:opacity-50",
        variants[variant],
        sizes[size],
        block && "w-full",
        className,
      )}
    >
      {loading ? <Spinner size={size === "sm" ? 12 : 16} /> : icon}
      {/* asChild: the child element (a link) becomes the button and takes the icon inside it. */}
      {asChild ? <Slot.Slottable>{children}</Slot.Slottable> : <span>{children}</span>}
    </Comp>
  );
}
