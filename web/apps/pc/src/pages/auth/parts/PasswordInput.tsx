import { Input, type InputProps } from "@exchange/ui";
import { Eye, EyeOff } from "lucide-react";
import { useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

/**
 * PasswordInput is an Input for passwords: a show/hide toggle and a Caps
 * Lock warning. It passes everything else through (react-hook-form's
 * register, FormField's id and ARIA wiring).
 */
export function PasswordInput({ onKeyDown, onKeyUp, onBlur, ...props }: Omit<InputProps, "type" | "suffix">) {
  const { t } = useTranslation();
  const [shown, setShown] = useState(false);
  const [caps, setCaps] = useState(false);
  const readCaps = (e: KeyboardEvent<HTMLInputElement>) => setCaps(e.getModifierState?.("CapsLock") === true);
  return (
    <div className="flex flex-col gap-1">
      <Input
        {...props}
        type={shown ? "text" : "password"}
        spellCheck={false}
        autoCapitalize="off"
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
            aria-label={shown ? t("pcAuth.hidePassword") : t("pcAuth.showPassword")}
            aria-pressed={shown}
            title={shown ? t("pcAuth.hidePassword") : t("pcAuth.showPassword")}
            className="grid size-8 place-items-center rounded-1 text-fg-3 transition-colors hover:text-fg-1"
          >
            {shown ? <EyeOff size={16} /> : <Eye size={16} />}
          </button>
        }
      />
      {caps && (
        <p role="status" className="text-xs text-warn">
          {t("pcAuth.capsLock")}
        </p>
      )}
    </div>
  );
}
