import { QRCodeSVG } from "qrcode.react";
import { cn } from "../lib/cn";

export type QrCodeProps = {
  /** The text to encode (an address, an otpauth:// link). */
  value: string;
  /** Edge length in px (default 160). */
  size?: number;
  className?: string;
  /** The accessible name, e.g. "充值地址二维码". */
  label?: string;
};

/**
 * QrCode draws dark modules on a white card whatever the theme, so every
 * scanner reads it (deposit addresses, authenticator binding).
 */
export function QrCode({ value, size = 160, className, label }: QrCodeProps) {
  return (
    <div role="img" aria-label={label} className={cn("inline-block rounded-2 bg-white p-3 text-black", className)}>
      <QRCodeSVG value={value} size={size} fgColor="currentColor" bgColor="transparent" level="M" />
    </div>
  );
}
