import { QRCodeSVG } from "qrcode.react";
import type { ReactNode } from "react";
import { cn } from "../lib/cn";

export type QrCodeProps = {
  /** The text to encode (an address, an otpauth:// link). */
  value: string;
  /** Edge length in px (default 160). */
  size?: number;
  className?: string;
  /** The accessible name, e.g. "充值地址二维码". */
  label?: string;
  /**
   * A mark in a white badge at the centre (an app's platform): the code is
   * then drawn at error correction H, so it still scans with the badge over it.
   */
  logo?: ReactNode;
};

/**
 * QrCode draws dark modules on a white card whatever the theme, so every
 * scanner reads it (deposit addresses, authenticator binding, app
 * downloads). A logo sits in a white rounded badge a quarter of the code's
 * side (6% of its area; level H restores up to 30%).
 */
export function QrCode({ value, size = 160, className, label, logo }: QrCodeProps) {
  const badge = Math.round(size * 0.24);
  return (
    <div role="img" aria-label={label} className={cn("relative inline-block rounded-2 bg-white p-3 text-black", className)}>
      <QRCodeSVG value={value} size={size} fgColor="currentColor" bgColor="transparent" level={logo ? "H" : "M"} />
      {logo && (
        <span
          data-testid="qr-logo"
          className="absolute left-1/2 top-1/2 flex -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-2 bg-white"
          style={{ width: badge, height: badge, padding: Math.round(badge * 0.16) }}
        >
          {logo}
        </span>
      )}
    </div>
  );
}
