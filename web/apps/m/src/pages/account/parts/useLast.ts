import { useState } from "react";

/**
 * useLast returns the value, or while it is null the last value it had:
 * a sheet driven by "what is open" (a device, a task) keeps its content
 * while it slides out instead of collapsing.
 */
export function useLast<T>(value: T | null): T | null {
  const [last, setLast] = useState(value);
  if (value !== null && value !== last) setLast(value);
  return value ?? last;
}
