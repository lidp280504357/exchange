import type { ReactNode } from "react";
import { cn } from "../lib/cn";
import { hashString, type IdentityColor } from "../lib/identity";

// The built-in avatars (design 2026-10-07, avatars and usernames §1 #4): a
// user without an uploaded picture gets one of twelve, chosen by the user
// ID, so the sites and the console show the same one. Each is an identity
// colour (--id-*, deep enough under white in both themes) with a white
// geometric figure, no text; drawn in a 48 × 48 box, inside the circle the
// avatar clips to.

/** How many built-in avatars there are. */
export const DEFAULT_AVATARS = 12;

const round = { stroke: "currentColor", strokeLinejoin: "round", strokeLinecap: "round" } as const;

const ARTS: { color: IdentityColor; figure: ReactNode }[] = [
  {
    // A sun in its halo.
    color: "orange",
    figure: (
      <>
        <circle cx="24" cy="24" r="14.5" fill="none" stroke="currentColor" strokeWidth="3" opacity={0.45} />
        <circle cx="24" cy="24" r="8" />
      </>
    ),
  },
  {
    // Two peaks.
    color: "amber",
    figure: (
      <>
        <path d="M8 33 16.5 20.5 25 33Z" strokeWidth="2" {...round} opacity={0.5} />
        <path d="M16 33 27 15.5 38 33Z" strokeWidth="2" {...round} />
      </>
    ),
  },
  {
    // A flower of four petals.
    color: "lime",
    figure: (
      <>
        <g opacity={0.5}>
          <circle cx="24" cy="16" r="7" />
          <circle cx="32" cy="24" r="7" />
          <circle cx="24" cy="32" r="7" />
          <circle cx="16" cy="24" r="7" />
        </g>
        <circle cx="24" cy="24" r="5.5" />
      </>
    ),
  },
  {
    // A hexagonal nut.
    color: "emerald",
    figure: (
      <>
        <path fillRule="evenodd" d="M24 11 35.26 17.5V30.5L24 37 12.74 30.5V17.5ZM24 18.5 19.24 21.25V26.75L24 29.5 28.76 26.75V21.25Z" strokeWidth="2" {...round} />
        <circle cx="24" cy="24" r="2.5" opacity={0.5} />
      </>
    ),
  },
  {
    // Two waves.
    color: "teal",
    figure: (
      <>
        <path d="M8 20q4-5 8 0t8 0 8 0 8 0" fill="none" strokeWidth="3.2" {...round} />
        <path d="M8 28.5q4-5 8 0t8 0 8 0 8 0" fill="none" strokeWidth="3.2" {...round} opacity={0.5} />
      </>
    ),
  },
  {
    // A diamond in a diamond.
    color: "cyan",
    figure: (
      <>
        <path d="M24 9.5 38.5 24 24 38.5 9.5 24Z" strokeWidth="2" {...round} opacity={0.35} />
        <path d="M24 17 31 24 24 31 17 24Z" strokeWidth="2" {...round} />
      </>
    ),
  },
  {
    // Three arches.
    color: "sky",
    figure: (
      <>
        <path d="M7 33a17 17 0 0 1 34 0" fill="none" strokeWidth="3.2" {...round} opacity={0.3} />
        <path d="M12.5 33a11.5 11.5 0 0 1 23 0" fill="none" strokeWidth="3.2" {...round} opacity={0.6} />
        <path d="M18 33a6 6 0 0 1 12 0" fill="none" strokeWidth="3.2" {...round} />
      </>
    ),
  },
  {
    // Four dots, a diagonal lit.
    color: "blue",
    figure: (
      <>
        <g opacity={0.45}>
          <circle cx="31" cy="17" r="5" />
          <circle cx="17" cy="31" r="5" />
        </g>
        <circle cx="17" cy="17" r="5" />
        <circle cx="31" cy="31" r="5" />
      </>
    ),
  },
  {
    // A sparkle and a small one.
    color: "indigo",
    figure: (
      <>
        <path d="M22 9.5C22.9 17.1 25.9 20.1 33.5 21 25.9 21.9 22.9 24.9 22 32.5 21.1 24.9 18.1 21.9 10.5 21 18.1 20.1 21.1 17.1 22 9.5Z" />
        <path d="M33 28C33.4 31.3 34.7 32.6 38 33 34.7 33.4 33.4 34.7 33 38 32.6 34.7 31.3 33.4 28 33 31.3 32.6 32.6 31.3 33 28Z" opacity={0.5} />
      </>
    ),
  },
  {
    // A crescent and a star: the moon's dark part is the background colour.
    color: "violet",
    figure: (
      <>
        <circle cx="22.5" cy="25" r="12" />
        <circle cx="29" cy="19.5" r="10" style={{ fill: "var(--id-violet)" }} />
        <circle cx="34.5" cy="32" r="2" opacity={0.6} />
      </>
    ),
  },
  {
    // A plus on a tile.
    color: "fuchsia",
    figure: (
      <>
        <rect x="12" y="12" width="24" height="24" rx="7" opacity={0.35} />
        <path d="M24 17v14M17 24h14" fill="none" strokeWidth="5" {...round} />
      </>
    ),
  },
  {
    // A coin split in two.
    color: "rose",
    figure: (
      <>
        <path d="M14 32.4A13 13 0 0 1 32.4 14Z" />
        <path d="M34 15.6A13 13 0 0 1 15.6 34Z" opacity={0.45} />
      </>
    ),
  },
];

/** defaultAvatarIndex is the built-in avatar of a user ID: the same everywhere. */
export function defaultAvatarIndex(seed: string): number {
  return hashString(seed.toLowerCase()) % DEFAULT_AVATARS;
}

export type DefaultAvatarProps = {
  /** The user ID it is chosen by. */
  seed?: string;
  /** Or the image itself (0 to 11), for the catalogue. */
  index?: number;
  /** Its accessible name; without one it is decoration. */
  label?: string;
  className?: string;
};

/** DefaultAvatar draws a built-in avatar, filling its box. */
export function DefaultAvatar({ seed, index, label, className }: DefaultAvatarProps) {
  const i = (((index ?? defaultAvatarIndex(seed ?? "")) % DEFAULT_AVATARS) + DEFAULT_AVATARS) % DEFAULT_AVATARS;
  const art = ARTS[i];
  if (!art) return null;
  return (
    <svg
      viewBox="0 0 48 48"
      fill="currentColor"
      className={cn("block size-full text-white", className)}
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      data-avatar-default={i}
    >
      <rect width="48" height="48" style={{ fill: `var(--id-${art.color})` }} />
      {art.figure}
    </svg>
  );
}
