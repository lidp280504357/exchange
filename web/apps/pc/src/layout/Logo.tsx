import { useBranding } from "@exchange/core/platform/index";

/**
 * Logo: the mark and the word. Both come from the platform profile (design
 * 2026-10-04 §4.1): its dark-background mark (the site is dark), else its
 * light one, else the built-in mark; its name.
 */
export function Logo({ compact }: { compact?: boolean }) {
  const p = useBranding();
  const mark = p.images.logo_dark ?? p.images.logo_light;
  return (
    <span className="flex items-center gap-2 font-semibold tracking-wide text-fg-1">
      {mark ? (
        <img src={mark} alt="" width={24} height={24} className="size-6 object-contain" />
      ) : (
        <svg width="24" height="24" viewBox="0 0 24 24" aria-hidden>
          <path d="M12 2l9 5v10l-9 5-9-5V7z" className="fill-brand" />
          <path d="M12 6l5 3v6l-5 3-5-3V9z" className="fill-bg-0" />
          <path d="M12 9.5l2.5 1.5v3L12 15.5 9.5 14v-3z" className="fill-brand" />
        </svg>
      )}
      {!compact && <span className="text-md uppercase">{p.name}</span>}
    </span>
  );
}
