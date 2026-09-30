import type { ButtonHTMLAttributes, ReactNode, Ref } from "react";
import { cn } from "../lib/cn";

export type IconButtonVariant = "ghost" | "secondary" | "primary" | "outline";
export type IconButtonSize = "xs" | "sm" | "md" | "lg";

export type IconButtonProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children"> & {
  /** The icon (a lucide icon element). */
  icon: ReactNode;
  /** The accessible name: icon-only buttons must say what they do. */
  label: string;
  variant?: IconButtonVariant;
  size?: IconButtonSize;
  /** A toggle's pressed state (favourite star, indicator switch). */
  active?: boolean;
  round?: boolean;
  ref?: Ref<HTMLButtonElement>;
};

const variants: Record<IconButtonVariant, string> = {
  ghost: "bg-transparent text-fg-2 hover:bg-bg-2 hover:text-fg-1",
  secondary: "bg-bg-3 text-fg-1 hover:bg-line-2",
  primary: "bg-brand text-brand-fg hover:brightness-110",
  outline: "border border-line-2 bg-transparent text-fg-2 hover:border-fg-3 hover:text-fg-1",
};

const sizes: Record<IconButtonSize, string> = {
  xs: "size-6 [&_svg]:size-3.5",
  sm: "size-8 [&_svg]:size-4",
  md: "size-10 [&_svg]:size-[18px]",
  lg: "size-12 [&_svg]:size-5",
};

/**
 * IconButton is a square button with only an icon; `label` becomes its
 * aria-label and title. `active` marks a pressed toggle (aria-pressed).
 */
export function IconButton({
  icon, label, variant = "ghost", size = "sm", active, round, className, type = "button", ref, ...rest
}: IconButtonProps) {
  return (
    <button
      ref={ref}
      type={type}
      aria-label={label}
      title={label}
      aria-pressed={active}
      {...rest}
      className={cn(
        "inline-flex shrink-0 select-none items-center justify-center transition-[filter,background-color,color,transform] duration-[var(--t-fast)] ease-out active:scale-[0.94] disabled:cursor-not-allowed disabled:opacity-50",
        round ? "rounded-full" : "rounded-2",
        variants[variant],
        sizes[size],
        active && "bg-brand-soft text-brand hover:bg-brand-soft hover:text-brand",
        className,
      )}
    >
      {icon}
    </button>
  );
}
