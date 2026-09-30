import { useEffect } from "react";
import { useNavigationType } from "react-router";

/**
 * useScrollTop starts a page at the top when it was reached by a link
 * (the page scrolls with the window, so it would otherwise open at the
 * previous page's offset). Going back (POP) leaves the position to the
 * browser, and an #anchor in the address wins. `key` is what makes it a
 * new page (the path, an article's slug), not its query string.
 */
export function useScrollTop(key: string): void {
  const type = useNavigationType();
  useEffect(() => {
    if (type === "POP" || window.location.hash) return;
    window.scrollTo(0, 0);
    // Only a new key (a new page) scrolls; the navigation type is read at that moment.
  }, [key]);
}
