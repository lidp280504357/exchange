import { Check, Copy } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";
import { copyText } from "../lib/clipboard";

export type CopyButtonProps = {
  /** The text put on the clipboard. */
  value: string;
  /** Visible text next to the icon (default: icon only). */
  children?: ReactNode;
  /** The icon-only button's name when "copy" alone would not say what it copies ("复制 SHA-256"). */
  label?: string;
  size?: number;
  onCopied?: () => void;
  className?: string;
};

/**
 * CopyButton copies a value (an address, a trace ID, an order ID) and
 * shows a check for 1.5 s; the "copied" notice is announced politely.
 */
export function CopyButton({ value, children, label, size = 14, onCopied, className }: CopyButtonProps) {
  const { t } = useTranslation();
  const [done, setDone] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const copy = async () => {
    if (!(await copyText(value))) return;
    setDone(true);
    onCopied?.();
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setDone(false), 1500);
  };
  return (
    <button
      type="button"
      onClick={copy}
      aria-label={children ? undefined : done ? t("common.copied") : (label ?? t("common.copy"))}
      title={done ? t("common.copied") : (label ?? t("common.copy"))}
      className={cn(
        "hit-area inline-flex shrink-0 items-center gap-1 rounded-1 text-fg-3 transition-colors hover:text-fg-1",
        done && "text-success hover:text-success",
        children ? "px-1.5 py-0.5 text-xs" : "p-0.5",
        className,
      )}
    >
      {done ? <Check size={size} className="animate-fade-in" /> : <Copy size={size} />}
      {children}
      <span aria-live="polite" className="sr-only">
        {done ? t("common.copied") : ""}
      </span>
    </button>
  );
}
