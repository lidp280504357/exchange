import { useTerminalPrefs } from "@exchange/core";
import { useRef, type PointerEvent } from "react";
import { useTranslation } from "react-i18next";

const MIN = 160;

/**
 * PanelResizer: the handle between the terminal and the orders panel;
 * dragging it sets the panel's height (kept on the device). Arrow keys
 * move it by 20 px.
 */
export function PanelResizer() {
  const { t } = useTranslation();
  const height = useTerminalPrefs((s) => s.panelHeight);
  const set = useTerminalPrefs((s) => s.set);
  const start = useRef<{ y: number; h: number } | null>(null);
  const clamp = (h: number) => Math.round(Math.max(MIN, Math.min(window.innerHeight * 0.6, h)));

  const down = (e: PointerEvent<HTMLDivElement>) => {
    e.currentTarget.setPointerCapture(e.pointerId);
    start.current = { y: e.clientY, h: height };
  };
  const move = (e: PointerEvent<HTMLDivElement>) => {
    if (!start.current) return;
    set({ panelHeight: clamp(start.current.h + start.current.y - e.clientY) });
  };
  const up = () => {
    start.current = null;
  };

  return (
    <div
      role="separator"
      aria-orientation="horizontal"
      aria-label={t("pcTrade.resizePanel")}
      aria-valuenow={height}
      tabIndex={0}
      onPointerDown={down}
      onPointerMove={move}
      onPointerUp={up}
      onPointerCancel={up}
      onKeyDown={(e) => {
        if (e.key === "ArrowUp" || e.key === "ArrowDown") {
          e.preventDefault();
          set({ panelHeight: clamp(height + (e.key === "ArrowUp" ? 20 : -20)) });
        }
      }}
      className="group relative h-1 shrink-0 cursor-row-resize bg-bg-0 outline-none"
    >
      <span className="absolute inset-x-0 top-1/2 h-px -translate-y-1/2 bg-line-1 transition-colors group-hover:bg-brand group-focus-visible:bg-brand" />
    </div>
  );
}
