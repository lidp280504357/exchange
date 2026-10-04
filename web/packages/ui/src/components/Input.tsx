import { X } from "lucide-react";
import { useId, useRef, useState, type ChangeEvent, type InputHTMLAttributes, type ReactNode, type Ref } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";
import { mergeRefs, setNativeValue } from "../lib/refs";

export type InputSize = "sm" | "md" | "lg";

export type InputProps = Omit<InputHTMLAttributes<HTMLInputElement>, "size" | "prefix"> & {
  size?: InputSize;
  /** Before the value: an icon, or an inline label such as "价格". */
  prefix?: ReactNode;
  /** The unit after the value, e.g. "USDT". */
  unit?: ReactNode;
  /** After everything: a button or a menu. */
  suffix?: ReactNode;
  /** Shows a clear button while there is text. */
  clearable?: boolean;
  onClear?: () => void;
  /** The new text on every change (alongside the native onChange). */
  onValueChange?: (value: string) => void;
  /** true marks the field invalid; a string is also shown under it. */
  error?: boolean | string;
  /** A hint under the field (hidden while an error message shows). */
  hint?: ReactNode;
  containerClassName?: string;
  /** Classes of the bordered box. */
  boxClassName?: string;
  ref?: Ref<HTMLInputElement>;
};

const sizes: Record<InputSize, { box: string; text: string; pad: string }> = {
  sm: { box: "h-8 rounded-1", text: "text-sm", pad: "px-2.5" },
  md: { box: "h-10 rounded-2", text: "text-base", pad: "px-3" },
  lg: { box: "h-12 rounded-2", text: "text-md", pad: "px-4" },
};

/** isInvalid reads the error state from the props (FormField sets aria-invalid). */
export function isInvalid(error: unknown, ariaInvalid: unknown): boolean {
  return Boolean(error) || ariaInvalid === true || ariaInvalid === "true";
}

/**
 * Input is the text field of the design system: prefix, unit and suffix
 * slots, a clear button, an error state with its message, three sizes.
 * The focus ring sits on the box, not the bare input.
 */
export function Input({
  size = "md", prefix, unit, suffix, clearable, onClear, onValueChange, error, hint, containerClassName, boxClassName, className,
  ref, id, disabled, readOnly, value, defaultValue, onChange, ...rest
}: InputProps) {
  const { t } = useTranslation();
  const auto = useId();
  const inputId = id ?? auto;
  const inner = useRef<HTMLInputElement>(null);
  const [typed, setTyped] = useState(() => String(defaultValue ?? "").length > 0);
  const hasValue = value !== undefined ? String(value).length > 0 : typed;
  const invalid = isInvalid(error, rest["aria-invalid"]);
  const message = typeof error === "string" && error ? error : null;
  const msgId = `${inputId}-msg`;
  const describedBy = [rest["aria-describedby"], message || hint ? msgId : null].filter(Boolean).join(" ") || undefined;
  const s = sizes[size];

  const change = (e: ChangeEvent<HTMLInputElement>) => {
    onChange?.(e);
    onValueChange?.(e.target.value);
    if (value === undefined) setTyped(e.target.value.length > 0);
  };

  const clear = () => {
    const el = inner.current;
    if (el) {
      setNativeValue(el, "");
      el.focus();
    }
    onClear?.();
  };

  return (
    <div className={cn("w-full", containerClassName)}>
      <div
        className={cn(
          "flex w-full items-center border bg-bg-2 transition-[border-color,box-shadow] duration-[var(--t-fast)]",
          "focus-within:border-brand focus-within:ring-1 focus-within:ring-brand",
          invalid ? "border-danger focus-within:border-danger focus-within:ring-danger" : "border-line-1 hover:border-line-2",
          disabled && "cursor-not-allowed opacity-50",
          s.box,
          // A large field is a 44 px touch target on touch screens (the
          // 14 px root makes h-12 42 px, the field inside it 40).
          size === "lg" && "pointer-coarse:h-auto",
          boxClassName,
        )}
      >
        {prefix !== undefined && <span className={cn("flex shrink-0 items-center text-fg-3", s.text, size === "sm" ? "pl-2.5" : "pl-3")}>{prefix}</span>}
        <input
          {...rest}
          ref={mergeRefs(inner, ref)}
          id={inputId}
          value={value}
          defaultValue={defaultValue}
          disabled={disabled}
          readOnly={readOnly}
          onChange={change}
          aria-invalid={invalid || undefined}
          aria-describedby={describedBy}
          className={cn(
            "h-full min-w-0 flex-1 bg-transparent text-fg-1 outline-none placeholder:text-fg-3 focus-visible:outline-none disabled:cursor-not-allowed",
            s.text,
            s.pad,
            size === "lg" && "pointer-coarse:min-h-tap",
            className,
          )}
        />
        {clearable && hasValue && !disabled && !readOnly && (
          <button
            type="button"
            tabIndex={-1}
            aria-label={t("ui.clear")}
            onClick={clear}
            className="hit-area mr-1 grid size-5 shrink-0 place-items-center rounded-full text-fg-3 hover:bg-bg-3 hover:text-fg-1"
          >
            <X size={12} />
          </button>
        )}
        {unit !== undefined && <span className={cn("shrink-0 text-fg-3", size === "lg" ? "text-base" : "text-sm", size === "sm" ? "pr-2.5" : "pr-3")}>{unit}</span>}
        {suffix !== undefined && <span className="flex shrink-0 items-center pr-1">{suffix}</span>}
      </div>
      {(message || hint) && (
        <p id={msgId} className={cn("mt-1 text-xs", message ? "text-danger" : "text-fg-3")}>
          {message ?? hint}
        </p>
      )}
    </div>
  );
}
