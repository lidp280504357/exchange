import { passwordChecks, passwordStrength } from "@exchange/core/auth/password";
import { cn } from "@exchange/ui";
import { Check, Dot } from "lucide-react";
import { useTranslation } from "react-i18next";

// Literal classes per strength (the Tailwind scanner needs them).
const barTone = ["bg-bg-3", "bg-danger", "bg-warn", "bg-success", "bg-success"] as const;
const textTone = ["text-fg-3", "text-danger", "text-warn", "text-success", "text-success"] as const;

/**
 * PasswordStrength shows a new password's strength as four bars and the
 * rules the page can check, live (password.ts). Common passwords are the
 * server's call: AUTH_PASSWORD_WEAK shows under the field.
 */
export function PasswordStrength({ password, identifiers = [] }: { password: string; identifiers?: string[] }) {
  const { t } = useTranslation();
  const strength = passwordStrength(password, identifiers);
  const checks = passwordChecks(password, identifiers);
  return (
    <div className="flex flex-col gap-2 rounded-2 bg-bg-1 p-3">
      <div className="flex items-center gap-3">
        <span className="shrink-0 text-xs text-fg-3">{t("pcAuth.strength")}</span>
        <div aria-hidden className="grid flex-1 grid-cols-4 gap-1">
          {[1, 2, 3, 4].map((i) => (
            <span
              key={i}
              className={cn("h-1 rounded-full transition-colors duration-[var(--t-base)]", i <= strength ? barTone[strength] : "bg-bg-3")}
            />
          ))}
        </div>
        <span aria-live="polite" className={cn("w-12 shrink-0 text-right text-xs font-medium", textTone[strength])}>
          {t(`pcAuth.strengthLevel.${password ? strength : 0}`)}
        </span>
      </div>
      <ul className="flex flex-col gap-1 text-xs">
        {checks.map((c) => (
          <li key={c.rule} className={cn("flex items-center gap-1.5 transition-colors", c.ok ? "text-success" : "text-fg-3")}>
            {c.ok ? <Check size={12} strokeWidth={3} aria-hidden /> : <Dot size={12} strokeWidth={4} aria-hidden />}
            {t(`pcAuth.rule.${c.rule}`)}
          </li>
        ))}
        <li className="flex items-center gap-1.5 text-fg-3">
          <Dot size={12} strokeWidth={4} aria-hidden />
          {t("pcAuth.commonHint")}
        </li>
      </ul>
    </div>
  );
}
