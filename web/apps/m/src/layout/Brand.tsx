import { useBrandText, useBranding } from "@exchange/core/platform/index";
import { FlaskConical } from "lucide-react";

/**
 * BrandMark: the platform profile's mark (the dark-background one, else the
 * light one) and name in the top bars (design 2026-10-04 §4.1).
 */
export function BrandMark() {
  const p = useBranding();
  const mark = p.images.logo_dark ?? p.images.logo_light;
  return (
    <span className="flex min-w-0 items-center gap-2 font-semibold uppercase tracking-wide text-fg-1">
      {mark && <img src={mark} alt="" width={22} height={22} className="size-[22px] shrink-0 object-contain" />}
      <span className="truncate">{p.name}</span>
    </span>
  );
}

/**
 * LearningStrip: the profile's learning-mode text under the top bar while
 * that mode is on (design 2026-10-04 §4.3); a live exchange turns it off.
 */
export function LearningStrip() {
  const on = useBranding().learning_mode.enabled;
  const text = useBrandText((p) => p.learning_mode.text);
  if (!on || !text) return null;
  return (
    <div role="note" className="flex items-center justify-center gap-1.5 bg-brand-soft px-4 py-1 text-center text-xs text-fg-1">
      <FlaskConical size={12} className="shrink-0 text-brand" aria-hidden />
      <span className="min-w-0">{text}</span>
    </div>
  );
}
