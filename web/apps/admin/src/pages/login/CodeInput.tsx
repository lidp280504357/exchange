import { cn } from "@exchange/ui";
import { useRef, type ClipboardEvent, type KeyboardEvent } from "react";

const LENGTH = 6;

/**
 * CodeInput takes a 6-digit authenticator code in six boxes: typing moves
 * to the next box, Backspace to the previous one, and pasting (or the
 * browser's one-time-code autofill) fills them all.
 */
export function CodeInput({ value, onChange, label }: { value: string; onChange: (code: string) => void; label: string }) {
  const boxes = useRef<(HTMLInputElement | null)[]>([]);
  const digits = Array.from({ length: LENGTH }, (_, i) => value[i] ?? "");
  const focus = (i: number) => boxes.current[Math.max(0, Math.min(LENGTH - 1, i))]?.focus();
  const set = (i: number, text: string) => {
    const typed = text.replace(/\D/g, "");
    if (!typed) return;
    // Several digits at once (autofill into one box) fill from here on.
    const next = (value.slice(0, i) + typed).slice(0, LENGTH);
    onChange(next);
    focus(next.length);
  };
  const onKey = (i: number) => (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Backspace") {
      e.preventDefault();
      if (digits[i]) onChange(value.slice(0, i));
      else if (i > 0) {
        onChange(value.slice(0, i - 1));
        focus(i - 1);
      }
    } else if (e.key === "ArrowLeft") focus(i - 1);
    else if (e.key === "ArrowRight") focus(i + 1);
  };
  const onPaste = (e: ClipboardEvent<HTMLInputElement>) => {
    const code = e.clipboardData.getData("text").replace(/\D/g, "").slice(0, LENGTH);
    if (!code) return;
    e.preventDefault();
    onChange(code);
    focus(code.length);
  };
  return (
    <div role="group" aria-label={label} className="flex gap-2">
      {digits.map((d, i) => (
        <input
          key={i}
          ref={(el) => {
            boxes.current[i] = el;
          }}
          value={d}
          inputMode="numeric"
          autoComplete={i === 0 ? "one-time-code" : "off"}
          maxLength={i === 0 ? LENGTH : 1}
          aria-label={`${label} ${i + 1}`}
          onChange={(e) => set(i, e.target.value)}
          onKeyDown={onKey(i)}
          onPaste={onPaste}
          onFocus={(e) => e.target.select()}
          className={cn(
            "h-12 w-full min-w-0 rounded-2 border bg-bg-1 text-center font-mono text-lg text-fg-1 outline-none transition-[border-color,transform] duration-[var(--t-fast)]",
            "focus:-translate-y-0.5 focus:border-brand",
            d ? "border-fg-3" : "border-line-2",
          )}
        />
      ))}
    </div>
  );
}
