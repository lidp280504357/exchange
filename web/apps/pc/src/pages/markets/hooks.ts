import { useBranding } from "@exchange/core/platform/index";
import { useEffect, useSyncExternalStore } from "react";

// Small page helpers shared by the markets and content pages.

/** usePageTitle names the browser tab while the page is open. */
export function usePageTitle(title: string | undefined): void {
  const brand = useBranding().name;
  useEffect(() => {
    if (!title) return;
    const before = document.title;
    document.title = `${title} · ${brand}`;
    return () => {
      document.title = before;
    };
  }, [title, brand]);
}

/** useMediaQuery follows a CSS media query (wide layouts at 1280 px and up). */
export function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (fn) => {
      const m = window.matchMedia(query);
      m.addEventListener("change", fn);
      return () => m.removeEventListener("change", fn);
    },
    () => window.matchMedia(query).matches,
    () => false,
  );
}

/** isTyping reports whether a key press goes to a text field (shortcuts stay out of the way). */
export function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  if (!el) return false;
  return el.isContentEditable || el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.tagName === "SELECT";
}
