import { LANGUAGES, type Locale } from "@exchange/core/settings/store";
import { Combobox } from "./Combobox";
import { Select, type SelectSize } from "./Select";

export type LanguageSelectProps = {
  value: Locale;
  onValueChange: (locale: Locale) => void;
  /** Select's sizes; the searchable list (more than SEARCH_FROM languages) takes sm to lg. */
  size?: Exclude<SelectSize, "xs">;
  className?: string;
  "aria-label"?: string;
};

/** From this many languages on, the list is searchable. */
const SEARCH_FROM = 7;

/**
 * LanguageSelect is the sites' language setting (F30): a dropdown of the
 * languages core lists (LANGUAGES), each by its own name with its English
 * name beside it; with more than six of them it becomes a searchable list
 * that also finds a language by its English name or tag.
 */
export function LanguageSelect({ value, onValueChange, size = "md", className, "aria-label": ariaLabel }: LanguageSelectProps) {
  if (LANGUAGES.length >= SEARCH_FROM) {
    return (
      <Combobox
        items={LANGUAGES.map((l) => ({
          value: l.locale,
          label: l.name,
          lang: l.locale,
          description: l.english !== l.name ? l.english : undefined,
          keywords: [l.english, l.locale],
        }))}
        value={value}
        onValueChange={(v) => onValueChange(v as Locale)}
        size={size === "md" || size === "lg" ? size : "sm"}
        className={className}
        aria-label={ariaLabel}
      />
    );
  }
  return (
    <Select
      value={value}
      onValueChange={(v) => onValueChange(v as Locale)}
      size={size}
      className={className}
      aria-label={ariaLabel}
      options={LANGUAGES.map((l) => ({
        value: l.locale,
        label: <span lang={l.locale}>{l.name}</span>,
        hint: l.english !== l.name ? l.english : undefined,
      }))}
    />
  );
}
