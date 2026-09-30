import { useEffect, useMemo, useRef, useState, type MouseEvent, type PointerEvent } from "react";
import { LongPress, type LongPressOptions } from "./longPress";

/**
 * useLongPress returns the handlers of an element that also reacts to a
 * long press (see LongPress): spread them on a row or a link. The click
 * that follows a long press is dropped, and the browser's own long-press
 * menu stays closed. Keyboard and screen reader users need another way to
 * the same action (the favourite star beside the row).
 */
export function useLongPress(onLongPress: () => void, options?: LongPressOptions) {
  const cb = useRef(onLongPress);
  useEffect(() => {
    cb.current = onLongPress;
  });
  const [press] = useState(() => new LongPress(() => cb.current(), options));
  useEffect(() => () => press.cancel(), [press]);
  return useMemo(
    () => ({
      onPointerDown: (e: PointerEvent) => {
        if (e.pointerType === "mouse" && e.button !== 0) return;
        press.start({ x: e.clientX, y: e.clientY });
      },
      onPointerMove: (e: PointerEvent) => press.move({ x: e.clientX, y: e.clientY }),
      onPointerUp: () => press.end(),
      onPointerCancel: () => press.cancel(),
      onPointerLeave: () => press.cancel(),
      onClickCapture: (e: MouseEvent) => {
        if (press.consumeClick()) {
          e.preventDefault();
          e.stopPropagation();
        }
      },
      onContextMenu: (e: MouseEvent) => e.preventDefault(),
    }),
    [press],
  );
}
