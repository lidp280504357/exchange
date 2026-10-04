import { Input, type InputProps } from "@exchange/ui";
import { Eye, EyeOff } from "lucide-react";
import { useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

/**
 * PasswordInput is a large Input for passwords: a 44 px show/hide toggle
 * and a Caps Lock warning (external keyboards). It passes everything else
 * through (react-hook-form's register, FormField's id and ARIA wiring).
 */
export function PasswordInput({ onKeyDown, onKeyUp, onBlur, size = "lg", ...props }: Omit<InputProps, "type" | "suffix">) {
  const { t } = useTranslation();
  const [shown, setShown] = useState(false);
  const [caps, setCaps] = useState(false);
  const readCaps = (e: KeyboardEvent<HTMLInputElement>) => setCaps(e.getModifierState?.("CapsLock") === true);
  return (
    <div className="flex flex-col gap-1">
      <Input
        {...props}
        size={size}
        type={shown ? "text" : "password"}
        spellCheck={false}
        autoCapitalize="none"
        autoCorrect="off"
        onKeyDown={(e) => {
          readCaps(e);
          onKeyDown?.(e);
        }}
        onKeyUp={(e) => {
          readCaps(e);
          onKeyUp?.(e);
        }}
        onBlur={(e) => {
          setCaps(false);
          onBlur?.(e);
        }}
        suffix={
          <button
            type="button"
            onClick={() => setShown((s) => !s)}
            aria-label={shown ? t("mAuth.hidePassword") : t("mAuth.showPassword")}
            aria-pressed={shown}
            className="grid size-tap place-items-center rounded-2 text-fg-3 transition-colors active:text-fg-1"
          >
            {shown ? <EyeOff size={18} /> : <Eye size={18} />}
          </button>
        }
      />
      {caps && (
        <p role="status" className="text-xs text-warn">
          {t("mAuth.capsLock")}
        </p>
      )}
    </div>
  );
}
