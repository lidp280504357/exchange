import { dec } from "@exchange/core";
import { useRef } from "react";

/**
 * useDirection remembers which way a price last moved ("up" or "down"),
 * so the last price keeps its colour until it moves again.
 */
export function useDirection(value: string | null | undefined): "up" | "down" | null {
  const prev = useRef<string | null>(null);
  const dir = useRef<"up" | "down" | null>(null);
  if (value && dec.isDecimal(value) && prev.current !== value) {
    if (prev.current && dec.isDecimal(prev.current)) dir.current = dec.gt(value, prev.current) ? "up" : dec.lt(value, prev.current) ? "down" : dir.current;
    prev.current = value;
  }
  return dir.current;
}
