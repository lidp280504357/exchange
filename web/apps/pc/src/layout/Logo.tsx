/** Logo: the mark and the word. */
export function Logo({ compact }: { compact?: boolean }) {
  return (
    <span className="flex items-center gap-2 font-semibold tracking-wide text-fg-1">
      <svg width="24" height="24" viewBox="0 0 24 24" aria-hidden>
        <path d="M12 2l9 5v10l-9 5-9-5V7z" className="fill-brand" />
        <path d="M12 6l5 3v6l-5 3-5-3V9z" className="fill-bg-0" />
        <path d="M12 9.5l2.5 1.5v3L12 15.5 9.5 14v-3z" className="fill-brand" />
      </svg>
      {!compact && <span className="text-md">ASTRAS</span>}
    </span>
  );
}
