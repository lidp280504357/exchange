import type { ReactNode } from "react";
import { QrCode } from "../data/QrCode";

export type AppQrPanelProps = {
  /** "扫码下载 App". */
  title: string;
  /** A QR code each: what it holds, its caption ("Android") and its name ("Android 下载二维码", the caption without one). */
  codes: { key: string; value: string; caption: string; label?: string }[];
  /** The way to the download page, under the codes. */
  footer?: ReactNode;
  /** What shows instead of the codes while no app is offered ("暂未提供 App"). */
  empty?: ReactNode;
};

/**
 * AppQrPanel is the PC top bar's download panel (design 2026-10-07, App
 * download page §4, as Binance's top bar): a QR code for each app, and the
 * way to the download page; while none is offered, `empty` in their place.
 */
export function AppQrPanel({ title, codes, footer, empty }: AppQrPanelProps) {
  return (
    <div className="flex flex-col gap-3" data-testid="download-qrs">
      <div className="text-sm font-medium text-fg-1">{title}</div>
      {codes.length > 0 ? (
        <div className="flex gap-4">
          {codes.map((c) => (
            <figure key={c.key} className="flex flex-col items-center gap-1.5">
              <QrCode value={c.value} size={112} label={c.label ?? c.caption} />
              <figcaption className="text-xs text-fg-2">{c.caption}</figcaption>
            </figure>
          ))}
        </div>
      ) : (
        empty
      )}
      {footer}
    </div>
  );
}
