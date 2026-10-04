import { useBrandText, useBranding } from "@exchange/core/platform/index";
import { FlaskConical } from "lucide-react";

/**
 * LearningBanner: the platform profile's learning-mode text across the top
 * while that mode is on (design 2026-10-04 §4.3); a live exchange turns it
 * off in the console.
 */
export function LearningBanner() {
  const on = useBranding().learning_mode.enabled;
  const text = useBrandText((p) => p.learning_mode.text);
  if (!on || !text) return null;
  return (
    <div role="note" className="flex items-center justify-center gap-2 border-b border-line-1 bg-brand-soft px-4 py-1.5 text-xs text-fg-1">
      <FlaskConical size={14} className="shrink-0 text-brand" aria-hidden />
      {text}
    </div>
  );
}
