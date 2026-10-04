import { useBrandText, useTestMode } from "@exchange/core/platform/index";
import { FlaskConical } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * TestModeBanner: while the exchange is in test mode (design 2026-10-04
 * §4.1) and the console shows its banner, the profile's banner text (else
 * "测试模式") across the top; a live exchange turns test mode off.
 */
export function TestModeBanner() {
  const { t } = useTranslation();
  const { enabled, banner } = useTestMode();
  const text = useBrandText((p) => p.test_mode.text);
  if (!enabled || !banner) return null;
  return (
    <div role="note" className="flex items-center justify-center gap-2 border-b border-line-1 bg-brand-soft px-4 py-1.5 text-xs text-fg-1">
      <FlaskConical size={14} className="shrink-0 text-brand" aria-hidden />
      {text || t("pc.testMode")}
    </div>
  );
}
