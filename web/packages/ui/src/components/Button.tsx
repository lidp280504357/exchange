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

const variants: Record<ButtonVariant, string> = {
  primary: "bg-brand text-brand-fg hover:brightness-110",
  secondary: "bg-bg-3 text-fg-1 hover:bg-line-2",
  ghost: "bg-transparent text-fg-2 hover:bg-bg-2 hover:text-fg-1",
  danger: "bg-danger text-white hover:brightness-110",
  buy: "bg-up text-white hover:brightness-110",
  sell: "bg-down text-white hover:brightness-110",
};

const sizes: Record<ButtonSize, string> = {
  sm: "h-8 px-3 text-sm gap-1.5 rounded-1",
  md: "h-10 px-4 text-base gap-2 rounded-2",
  lg: "h-12 px-6 text-md gap-2 rounded-2",
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
