import { createContext, useContext, useEffect, useState, type ReactNode } from "react";

// The top bar belongs to the shell, its content to the page (design §7.1):
// a page sets its title (or a pair switcher) and the buttons on the right
// with usePageHeader; the shell draws them. Leaving the page clears them.

export type PageHeader = {
  /** The title of a sub page, or custom content (the terminal's pair switcher). */
  title?: ReactNode;
  /** Buttons on the right (search, notifications, a menu). */
  right?: ReactNode;
  /** Where the back button goes instead of the previous page (sub pages). */
  back?: string;
};

type Setter = (h: PageHeader | null) => void;

const SetHeader = createContext<Setter>(() => {});
const HeaderValue = createContext<PageHeader | null>(null);

/** HeaderProvider holds the current page's header for the shell. */
export function HeaderProvider({ children }: { children: ReactNode }) {
  const [header, setHeader] = useState<PageHeader | null>(null);
  return (
    <SetHeader.Provider value={setHeader}>
      <HeaderValue.Provider value={header}>{children}</HeaderValue.Provider>
    </SetHeader.Provider>
  );
}

/** useHeader is what the shell draws. */
export function useHeader(): PageHeader | null {
  return useContext(HeaderValue);
}

/**
 * usePageHeader sets the page's header while it is shown; pass the values
 * the header depends on in deps (the title's text, the symbol).
 */
export function usePageHeader(header: PageHeader, deps: readonly unknown[]): void {
  const set = useContext(SetHeader);
  useEffect(() => {
    set(header);
    return () => set(null);
    // The caller lists what the header depends on (deps).
  }, deps);
}
