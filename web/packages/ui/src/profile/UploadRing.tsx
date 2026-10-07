import type { AvatarUpload } from "@exchange/core/user/avatar";
import { cn } from "../lib/cn";

/**
 * UploadRing circles an avatar with its upload's progress (put it in the
 * avatar's relative box): a dimmed picture under a ring that fills as the
 * picture goes up, turning while it is prepared or the server saves it.
 */
export function UploadRing({ state }: { state: AvatarUpload }) {
  if (state.phase !== "preparing" && state.phase !== "uploading") return null;
  const r = 47;
  const c = 2 * Math.PI * r;
  const progress = state.phase === "uploading" && state.progress < 1 ? state.progress : null;
  return (
    <span aria-hidden className="pointer-events-none absolute -inset-1.5">
      <span className="absolute inset-1.5 rounded-full bg-bg-0/40" />
      <svg viewBox="0 0 100 100" className={cn("absolute inset-0 size-full -rotate-90", progress === null && "animate-spin")}>
        <circle cx="50" cy="50" r={r} fill="none" strokeWidth="3" className="stroke-bg-3" />
        <circle
          cx="50"
          cy="50"
          r={r}
          fill="none"
          strokeWidth="3"
          strokeLinecap="round"
          strokeDasharray={c}
          strokeDashoffset={progress === null ? c * 0.72 : c * (1 - progress)}
          className="stroke-brand transition-[stroke-dashoffset] duration-[var(--t-base)]"
        />
      </svg>
    </span>
  );
}
