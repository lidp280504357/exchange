import { motion, useReducedMotion } from "motion/react";
import { Tabs as RTabs } from "radix-ui";
import { useId, type ReactNode } from "react";
import { cn } from "../lib/cn";
import { useControllable } from "../lib/useControllable";
import { thumbSpring } from "./Segmented";

export type TabItem = {
  value: string;
  label: ReactNode;
  icon?: ReactNode;
  /** A count after the label: "当前委托 (3)". */
  count?: number | string;
  disabled?: boolean;
};

/** The only two tab looks of the whole site (design §5.2). */
export type TabsVariant = "underline" | "pill";

export type TabsProps = {
  items: TabItem[];
  value?: string;
  defaultValue?: string;
  onValueChange?: (value: string) => void;
  variant?: TabsVariant;
  size?: "sm" | "md" | "lg";
  /** Tabs share the bar's width. */
  block?: boolean;
  /** Content at the right end of the bar ("全部撤单", filters). */
  extra?: ReactNode;
  /** TabsPanel elements; leave out for a bar that only switches state. */
  children?: ReactNode;
  className?: string;
  listClassName?: string;
  "aria-label"?: string;
};

const sizes = {
  underline: { sm: "h-8 text-sm", md: "h-10 text-base", lg: "h-12 text-md" },
  pill: { sm: "h-7 px-3 text-sm", md: "h-8 px-4 text-base", lg: "h-10 px-5 text-md" },
};

/**
 * Tabs in one of the site's two looks: "underline" (panels, terminals) or
 * "pill" (categories, filters). The indicator slides between tabs with
 * motion layoutId; Radix Tabs gives the roles and arrow-key navigation.
 */
export function Tabs({
  items, value, defaultValue, onValueChange, variant = "underline", size = "md", block, extra, children, className, listClassName,
  "aria-label": ariaLabel,
}: TabsProps) {
  const id = useId();
  const reduced = useReducedMotion();
  const [active, setActive] = useControllable(value, defaultValue ?? items[0]?.value ?? "", onValueChange);
  const underline = variant === "underline";
  return (
    <RTabs.Root value={active} onValueChange={setActive} className={cn("flex min-w-0 flex-col", className)}>
      <div className={cn("flex min-w-0 items-center gap-2", underline && "border-b border-line-1", listClassName)}>
        <RTabs.List
          aria-label={ariaLabel}
          className={cn("flex min-w-0 items-center overflow-x-auto [scrollbar-width:none]", underline ? "gap-5" : "gap-1", block && "flex-1")}
        >
          {items.map((it) => {
            const on = it.value === active;
            return (
              <RTabs.Trigger
                key={it.value}
                value={it.value}
                disabled={it.disabled}
                className={cn(
                  "relative isolate inline-flex shrink-0 select-none items-center justify-center gap-1.5 whitespace-nowrap font-medium outline-none transition-colors duration-[var(--t-fast)]",
                  "focus-visible:ring-1 focus-visible:ring-brand disabled:cursor-not-allowed disabled:opacity-50",
                  sizes[variant][size],
                  !underline && "rounded-full",
                  block && "flex-1",
                  on ? (underline ? "text-fg-1" : "text-brand") : "text-fg-3 hover:text-fg-1",
                )}
              >
                {on && (
                  <motion.span
                    layoutId={`${id}-indicator`}
                    aria-hidden
                    transition={reduced ? { duration: 0 } : thumbSpring}
                    className={cn(
                      "pointer-events-none absolute -z-10",
                      underline ? "inset-x-0 -bottom-px h-0.5 rounded-full bg-brand" : "inset-0 rounded-full bg-brand-soft",
                    )}
                  />
                )}
                {it.icon}
                {it.label}
                {it.count !== undefined && <span className={cn("text-xs", on ? "text-fg-2" : "text-fg-3")}>({it.count})</span>}
              </RTabs.Trigger>
            );
          })}
        </RTabs.List>
        {extra !== undefined && <div className="ml-auto flex shrink-0 items-center gap-2">{extra}</div>}
      </div>
      {children}
    </RTabs.Root>
  );
}

export type TabsPanelProps = { value: string; children?: ReactNode; className?: string; /** Keep the panel mounted while hidden (charts). */ keepMounted?: boolean };

/** TabsPanel is the content of one tab (a Radix Tabs.Content). */
export function TabsPanel({ value, children, className, keepMounted }: TabsPanelProps) {
  return (
    <RTabs.Content
      value={value}
      forceMount={keepMounted || undefined}
      className={cn("min-h-0 outline-none data-[state=active]:animate-fade-in data-[state=inactive]:hidden", className)}
    >
      {children}
    </RTabs.Content>
  );
}
