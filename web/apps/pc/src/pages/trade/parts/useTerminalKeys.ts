import { useEffect, useRef } from "react";

function typing(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  if (!el) return false;
  const tag = el.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || el.isContentEditable;
}

/**
 * useTerminalKeys binds the terminal's shortcuts (design §6.2): B and S
 * switch the order side, "/" opens the pair search. Keys typed into a
 * field, or with a modifier, are left alone; Esc is the popovers' own.
 */
export function useTerminalKeys(handlers: { onBuy: () => void; onSell: () => void; onSearch: () => void }) {
  const ref = useRef(handlers);
  ref.current = handlers;
  useEffect(() => {
    const down = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey || typing(e.target)) return;
      const k = e.key.toLowerCase();
      if (k === "b") ref.current.onBuy();
      else if (k === "s") ref.current.onSell();
      else if (k === "/") {
        e.preventDefault();
        ref.current.onSearch();
      }
    };
    window.addEventListener("keydown", down);
    return () => window.removeEventListener("keydown", down);
  }, []);
}
