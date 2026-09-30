import { useCallback, useRef, useState } from "react";

/**
 * useControllable backs a value that a parent may control (value given) or
 * leave to the component (defaultValue): dialogs and sheets use it so
 * pages can pass `open` or just a trigger.
 */
export function useControllable<T>(
  value: T | undefined,
  defaultValue: T,
  onChange?: (next: T) => void,
): [T, (next: T) => void] {
  const [inner, setInner] = useState(defaultValue);
  const controlled = value !== undefined;
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const set = useCallback(
    (next: T) => {
      if (!controlled) setInner(next);
      onChangeRef.current?.(next);
    },
    [controlled],
  );
  return [controlled ? value : inner, set];
}
