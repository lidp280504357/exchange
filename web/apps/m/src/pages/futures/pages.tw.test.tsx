import { vi } from "vitest";
import { describePages } from "./pages.smoke";

// The settings store takes its first language from navigator.language: set
// it before any module creates the store (vi.hoisted runs before imports).
vi.hoisted(() => {
  Object.defineProperty(globalThis, "navigator", { value: { language: "zh-TW" }, configurable: true, writable: true });
});

describePages("zh-TW");
