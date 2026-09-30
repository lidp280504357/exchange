import { Search } from "lucide-react";
import { lazy, Suspense, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

// The dialog (Radix, scroll lock, focus trap, the market list) loads on
// the first ⌘K or click, so the top bar carries only the button.
const SearchDialog = lazy(() => import("./SearchDialog"));

/**
 * SearchPalette (⌘K / Ctrl+K, design §6.1): find a market by symbol or name
 * and jump to its terminal; arrow keys move, Enter opens, Esc closes.
 */
export function SearchPalette() {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    const down = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setLoaded(true);
        setOpen((o) => !o);
      }
    };
    window.addEventListener("keydown", down);
    return () => window.removeEventListener("keydown", down);
  }, []);

  return (
    <>
      <button
        type="button"
        onClick={() => {
          setLoaded(true);
          setOpen(true);
        }}
        onMouseEnter={() => setLoaded(true)}
        className="flex h-9 w-56 items-center gap-2 rounded-2 bg-bg-2 px-3 text-sm text-fg-3 transition-colors hover:text-fg-2"
        aria-label={t("nav.search")}
      >
        <Search size={16} />
        <span className="flex-1 text-left">{t("nav.search")}</span>
        <kbd className="rounded-1 border border-line-2 px-1.5 text-xs">⌘K</kbd>
      </button>
      {loaded && (
        <Suspense fallback={null}>
          <SearchDialog open={open} onOpenChange={setOpen} />
        </Suspense>
      )}
    </>
  );
}
