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
// Solid fills carry black text: white reads at 2.1-3.8:1 on the rise, fall
// and danger fills, black at 5.6:1 and more (the fills are the same on both
// themes). Soft and outline text takes the tones' strong shades (darker on
// the light theme); on soft fills rise, fall, info and danger take their
// soft-fg shades (theme.css), which also read on the dark theme's tints.
const tones: Record<BadgeTone, Record<BadgeVariant, string>> = {
  neutral: { soft: "bg-bg-3 text-fg-2", solid: "bg-fg-3 text-bg-0", outline: "border-line-2 text-fg-2" },
  brand: { soft: "bg-brand-soft text-brand-strong", solid: "bg-brand text-brand-fg", outline: "border-brand text-brand-strong" },
  up: { soft: "bg-up/15 text-up-soft-fg", solid: "bg-up text-black", outline: "border-up text-up-strong" },
  down: { soft: "bg-down/15 text-down-soft-fg", solid: "bg-down text-black", outline: "border-down text-down-strong" },
  info: { soft: "bg-info/15 text-info-soft-fg", solid: "bg-info text-black", outline: "border-info text-info-strong" },
  warn: { soft: "bg-warn/15 text-warn-strong", solid: "bg-warn text-black", outline: "border-warn text-warn-strong" },
  danger: { soft: "bg-danger/15 text-danger-soft-fg", solid: "bg-danger text-black", outline: "border-danger text-danger-strong" },
  success: { soft: "bg-success/15 text-success-strong", solid: "bg-success text-black", outline: "border-success text-success-strong" },
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
