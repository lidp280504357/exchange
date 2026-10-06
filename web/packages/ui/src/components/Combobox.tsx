import { coinName, LOCALES, type Locale } from "@exchange/core";
import { Check, ChevronDown, Search } from "lucide-react";
import { Popover as RPopover } from "radix-ui";
import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";
import { useControllable } from "../lib/useControllable";
import { CoinIcon } from "./CoinIcon";

export type ComboboxItem = {
  value: string;
  /** The main text, e.g. a coin symbol. */
  label: string;
  /** The second line, e.g. the coin's name. */
  description?: string;
  icon?: ReactNode;
  /** Extra words the filter matches (names in other languages, tags). */
  keywords?: string[];
  /** Right-aligned content (a price, "仅站内交易"). */
  trailing?: ReactNode;
  disabled?: boolean;
};

/**
 * filterItems keeps the items matching a query, best first: a label that
 * starts with it, then one containing it, then descriptions and keywords.
 */
export function filterItems(items: ComboboxItem[], query: string): ComboboxItem[] {
  const q = query.trim().toLowerCase();
  if (!q) return items;
  const scored: { item: ComboboxItem; score: number; i: number }[] = [];
  items.forEach((item, i) => {
    const label = item.label.toLowerCase();
    const desc = (item.description ?? "").toLowerCase();
    const words = (item.keywords ?? []).map((k) => k.toLowerCase());
    let score = -1;
    if (label.startsWith(q)) score = 0;
    else if (label.includes(q)) score = 1;
    else if (desc.startsWith(q)) score = 2;
    else if (desc.includes(q) || words.some((w) => w.includes(q))) score = 3;
    if (score >= 0) scored.push({ item, score, i });
  });
  return scored.sort((a, b) => a.score - b.score || a.i - b.i).map((s) => s.item);
}

/** coinItems builds coin search items: letter icon, symbol and name. */
export function coinItems(symbols: string[], locale: Locale): ComboboxItem[] {
  return symbols.map((s) => {
    return {
      value: s,
      label: s,
      description: coinName(s, locale),
      keywords: LOCALES.map((l) => coinName(s, l)),
      icon: <CoinIcon symbol={s} size={20} />,
    };
  });
}

export type ComboboxProps = {
  items: ComboboxItem[];
  value?: string | null;
  onValueChange?: (value: string, item: ComboboxItem) => void;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  /** A custom trigger (rendered asChild); the default shows the choice. */
  trigger?: ReactNode;
  placeholder?: ReactNode;
  searchPlaceholder?: string;
  emptyText?: ReactNode;
  size?: "sm" | "md" | "lg";
  disabled?: boolean;
  className?: string;
  contentClassName?: string;
  align?: "start" | "center" | "end";
  "aria-label"?: string;
};

const triggerSizes = { sm: "h-8 rounded-1 px-2.5 text-sm", md: "h-10 rounded-2 px-3 text-base", lg: "h-12 rounded-2 px-4 text-md" };

/**
 * Combobox is a searchable picker (coin search, pair switcher): a popover
 * with a filter field and a list that follows the keyboard (↑ ↓ Enter,
 * Esc closes), with the combobox/listbox roles for screen readers.
 */
export function Combobox({
  items, value, onValueChange, open, onOpenChange, trigger, placeholder, searchPlaceholder, emptyText, size = "md", disabled, className,
  contentClassName, align = "start", "aria-label": ariaLabel,
}: ComboboxProps) {
  const { t } = useTranslation();
  const id = useId();
  const [isOpen, setOpen] = useControllable(open, false, onOpenChange);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);
  const shown = useMemo(() => filterItems(items, query), [items, query]);
  const selected = items.find((i) => i.value === value);

  // Opening or typing puts the highlight on the choice, else the first item.
  useEffect(() => {
    if (!isOpen) return;
    const at = shown.findIndex((i) => i.value === value);
    setActive(at >= 0 && !query ? at : firstEnabled(shown, 0, 1));
  }, [isOpen, query, shown, value]);

  useEffect(() => {
    if (!isOpen) setQuery("");
  }, [isOpen]);

  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>(`[data-index="${active}"]`);
    el?.scrollIntoView?.({ block: "nearest" });
  }, [active]);

  const choose = (item: ComboboxItem | undefined) => {
    if (!item || item.disabled) return;
    onValueChange?.(item.value, item);
    setOpen(false);
  };

  const keyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const dir = e.key === "ArrowDown" ? 1 : -1;
      setActive((a) => firstEnabled(shown, wrap(a + dir, shown.length), dir));
    } else if (e.key === "Home" || e.key === "End") {
      e.preventDefault();
      setActive(e.key === "Home" ? firstEnabled(shown, 0, 1) : firstEnabled(shown, shown.length - 1, -1));
    } else if (e.key === "Enter") {
      e.preventDefault();
      choose(shown[active]);
    }
  };

  const optionId = (i: number) => `${id}-opt-${i}`;

  return (
    <RPopover.Root open={isOpen} onOpenChange={setOpen}>
      <RPopover.Trigger asChild disabled={disabled}>
        {trigger ?? (
          <button
            type="button"
            aria-label={ariaLabel}
            className={cn(
              "inline-flex min-w-0 items-center justify-between gap-2 border border-line-1 bg-bg-2 text-fg-1 outline-none transition-colors",
              "hover:border-line-2 focus-visible:ring-1 focus-visible:ring-brand disabled:cursor-not-allowed disabled:opacity-50",
              triggerSizes[size],
              className,
            )}
          >
            <span className="flex min-w-0 items-center gap-2 truncate">
              {selected ? (
                <>
                  {selected.icon}
                  <span className="truncate">{selected.label}</span>
                </>
              ) : (
                <span className="text-fg-3">{placeholder ?? t("ui.select")}</span>
              )}
            </span>
            <ChevronDown size={14} className="shrink-0 text-fg-3" />
          </button>
        )}
      </RPopover.Trigger>
      <RPopover.Portal>
        <RPopover.Content
          align={align}
          sideOffset={4}
          className={cn(
            "z-[var(--z-dropdown)] flex w-[max(var(--radix-popover-trigger-width),16rem)] flex-col overflow-hidden rounded-2 border border-line-1 bg-bg-2 shadow-pop",
            "origin-(--radix-popover-content-transform-origin) data-[state=open]:animate-pop-in data-[state=closed]:animate-pop-out",
            contentClassName,
          )}
        >
          <div className="flex items-center gap-2 border-b border-line-1 px-3">
            <Search size={14} className="shrink-0 text-fg-3" />
            <input
              role="combobox"
              aria-expanded
              aria-controls={`${id}-list`}
              aria-autocomplete="list"
              aria-activedescendant={shown[active] ? optionId(active) : undefined}
              aria-label={searchPlaceholder ?? t("ui.searchCoin")}
              placeholder={searchPlaceholder ?? t("ui.searchCoin")}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={keyDown}
              className="h-10 min-w-0 flex-1 bg-transparent text-sm text-fg-1 outline-none placeholder:text-fg-3 focus-visible:outline-none"
            />
          </div>
          <div ref={listRef} id={`${id}-list`} role="listbox" aria-label={ariaLabel} className="max-h-80 overflow-y-auto p-1">
            {shown.length === 0 && <div className="px-3 py-6 text-center text-sm text-fg-3">{emptyText ?? t("ui.noMatch")}</div>}
            {shown.map((item, i) => (
              <div
                key={item.value}
                id={optionId(i)}
                data-index={i}
                role="option"
                aria-selected={item.value === value}
                aria-disabled={item.disabled || undefined}
                onPointerMove={() => i !== active && !item.disabled && setActive(i)}
                onClick={() => choose(item)}
                className={cn(
                  "flex cursor-pointer select-none items-center gap-2.5 rounded-1 px-2 py-1.5",
                  i === active && "bg-bg-3",
                  item.disabled && "cursor-not-allowed opacity-50",
                )}
              >
                {item.icon}
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate text-sm font-medium text-fg-1">{item.label}</span>
                  {item.description && <span className="truncate text-xs text-fg-3">{item.description}</span>}
                </span>
                {item.trailing && <span className="shrink-0 text-xs text-fg-2">{item.trailing}</span>}
                {item.value === value && <Check size={14} className="shrink-0 text-brand" />}
              </div>
            ))}
          </div>
        </RPopover.Content>
      </RPopover.Portal>
    </RPopover.Root>
  );
}

function wrap(i: number, n: number): number {
  return n === 0 ? 0 : (i + n) % n;
}

// firstEnabled walks from `from` in direction dir to the first enabled item.
function firstEnabled(list: ComboboxItem[], from: number, dir: 1 | -1): number {
  const n = list.length;
  for (let k = 0; k < n; k++) {
    const i = wrap(from + k * dir, n);
    if (!list[i]?.disabled) return i;
  }
  return 0;
}
