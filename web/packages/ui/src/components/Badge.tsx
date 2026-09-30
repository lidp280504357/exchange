import { formatPercent } from "@exchange/core";
import { X } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";
import { toneOf } from "./PriceText";

export type BadgeTone = "neutral" | "brand" | "up" | "down" | "info" | "warn" | "danger" | "success";
export type BadgeVariant = "soft" | "solid" | "outline";

export type BadgeProps = {
  tone?: BadgeTone;
  variant?: BadgeVariant;
  size?: "sm" | "md";
  icon?: ReactNode;
  /** A status dot before the text. */
  dot?: boolean;
  children?: ReactNode;
  className?: string;
  title?: string;
};

// Literal class names per tone and variant (the Tailwind scanner needs them).
const tones: Record<BadgeTone, Record<BadgeVariant, string>> = {
  neutral: { soft: "bg-bg-3 text-fg-2", solid: "bg-fg-3 text-bg-0", outline: "border-line-2 text-fg-2" },
  brand: { soft: "bg-brand-soft text-brand", solid: "bg-brand text-brand-fg", outline: "border-brand text-brand" },
  up: { soft: "bg-up/15 text-up", solid: "bg-up text-white", outline: "border-up text-up" },
  down: { soft: "bg-down/15 text-down", solid: "bg-down text-white", outline: "border-down text-down" },
  info: { soft: "bg-info/15 text-info", solid: "bg-info text-white", outline: "border-info text-info" },
  warn: { soft: "bg-warn/15 text-warn", solid: "bg-warn text-black", outline: "border-warn text-warn" },
  danger: { soft: "bg-danger/15 text-danger", solid: "bg-danger text-white", outline: "border-danger text-danger" },
  success: { soft: "bg-success/15 text-success", solid: "bg-success text-white", outline: "border-success text-success" },
};

const dots: Record<BadgeTone, string> = {
  neutral: "bg-fg-3", brand: "bg-brand", up: "bg-up", down: "bg-down", info: "bg-info", warn: "bg-warn", danger: "bg-danger", success: "bg-success",
};

const sizes = { sm: "h-5 px-1.5 text-xs gap-1", md: "h-6 px-2 text-sm gap-1.5" };

/** Badge is a small pill for statuses, counts and changes, in eight tones. */
export function Badge({ tone = "neutral", variant = "soft", size = "sm", icon, dot, children, className, title }: BadgeProps) {
  return (
    <span
      title={title}
      className={cn(
        "inline-flex shrink-0 items-center whitespace-nowrap rounded-full font-medium tabular-nums",
        variant === "outline" && "border",
        tones[tone][variant],
        sizes[size],
        className,
      )}
    >
      {dot && <span aria-hidden className={cn("size-1.5 rounded-full", variant === "solid" ? "bg-current" : dots[tone])} />}
      {icon}
      {children}
    </span>
  );
}

export type TagProps = BadgeProps & {
  /** Shows a remove button (filter chips). */
  onRemove?: () => void;
  removeLabel?: string;
};

/** Tag is a rectangular label (categories, filters), removable when onRemove is given. */
export function Tag({ onRemove, removeLabel, children, className, variant = "outline", ...rest }: TagProps) {
  const { t } = useTranslation();
  return (
    <Badge {...rest} variant={variant} className={cn("rounded-1", onRemove && "pr-0.5", className)}>
      {children}
      {onRemove && (
        <button
          type="button"
          aria-label={removeLabel ?? t("ui.clear")}
          onClick={onRemove}
          className="grid size-4 place-items-center rounded-1 opacity-70 hover:bg-bg-3 hover:opacity-100"
        >
          <X size={10} />
        </button>
      )}
    </Badge>
  );
}

export type ChangeBadgeProps = Omit<BadgeProps, "tone" | "children"> & {
  /** A change as a fraction: "0.0231" shows "+2.31%". */
  value: string | null | undefined;
  decimals?: number;
};

/** ChangeBadge shows a 24h change as a solid green or red percentage. */
export function ChangeBadge({ value, decimals = 2, variant = "solid", className, ...rest }: ChangeBadgeProps) {
  const tone = toneOf(value);
  return (
    <Badge {...rest} variant={variant} tone={tone === "neutral" ? "neutral" : tone} className={cn("min-w-16 justify-center rounded-1", className)}>
      {formatPercent(value, decimals)}
    </Badge>
  );
}
