import { Input, Sheet, cn, filterItems, type ComboboxItem } from "@exchange/ui";
import { Check, Search, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

export type PickerSheetProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  /** The choices (the combobox items of the design system: label, description, keywords). */
  items: ComboboxItem[];
  value: string;
  onChoose: (value: string) => void;
  searchPlaceholder: string;
};

/**
 * PickerSheet is a long single choice on the phone (region, time zone):
 * a bottom sheet with a search field that stays on top and the list of
 * choices, the current one marked and scrolled into view. The keyboard
 * does not open by itself: focus lands on the close button first.
 */
export function PickerSheet({ open, onOpenChange, title, items, value, onChoose, searchPlaceholder }: PickerSheetProps) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange} title={title} closeButton className="h-[85dvh]">
      <PickerBody
        items={items}
        value={value}
        label={title}
        searchPlaceholder={searchPlaceholder}
        onChoose={(v) => {
          onChoose(v);
          onOpenChange(false);
        }}
      />
    </Sheet>
  );
}

// The body mounts with the sheet, so every opening starts with an empty search.
function PickerBody({
  items, value, label, searchPlaceholder, onChoose,
}: {
  items: ComboboxItem[];
  value: string;
  label: string;
  searchPlaceholder: string;
  onChoose: (value: string) => void;
}) {
  const { t } = useTranslation();
  const [query, setQuery] = useState("");
  const shown = useMemo(() => filterItems(items, query), [items, query]);
  const list = useRef<HTMLUListElement>(null);

  useEffect(() => {
    const id = requestAnimationFrame(() => {
      list.current?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView({ block: "center" });
    });
    return () => cancelAnimationFrame(id);
  }, []);

  return (
    <>
      <div className="sticky top-0 z-10 -mx-4 bg-bg-1 px-4 pb-2">
        <Input
          size="lg"
          inputMode="search"
          enterKeyHint="search"
          autoComplete="off"
          autoCorrect="off"
          autoCapitalize="none"
          spellCheck={false}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={searchPlaceholder}
          aria-label={searchPlaceholder}
          prefix={<Search size={18} />}
          suffix={
            query ? (
              <button type="button" aria-label={t("ui.clear")} onClick={() => setQuery("")} className="grid size-tap place-items-center text-fg-3">
                <X size={16} />
              </button>
            ) : undefined
          }
        />
      </div>
      {shown.length === 0 ? (
        <p className="py-12 text-center text-sm text-fg-3">{t("ui.noMatch")}</p>
      ) : (
        <ul ref={list} role="listbox" aria-label={label} className="flex flex-col">
          {shown.map((item) => {
            const on = item.value === value;
            return (
              <li key={item.value} role="presentation">
                <button
                  type="button"
                  role="option"
                  aria-selected={on}
                  onClick={() => onChoose(item.value)}
                  className={cn(
                    "flex min-h-tap w-full items-center gap-3 rounded-2 px-3 py-2 text-left transition-colors active:bg-bg-2",
                    on && "bg-brand-soft",
                  )}
                >
                  <span className={cn("min-w-0 flex-1 truncate text-base", on ? "font-medium text-brand" : "text-fg-1")}>{item.label}</span>
                  {item.description && <span className="shrink-0 text-xs text-fg-3 tabular-nums">{item.description}</span>}
                  {on && <Check size={16} className="shrink-0 text-brand" aria-hidden />}
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </>
  );
}
