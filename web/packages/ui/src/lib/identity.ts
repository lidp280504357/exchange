// Identity colours for letter icons (coins, avatars): a symbol always gets
// the same colour, from the --id-* tokens. Class names are literal so the
// Tailwind scanner sees them.

export const identityPalette = {
  orange: "bg-id-orange",
  amber: "bg-id-amber",
  lime: "bg-id-lime",
  emerald: "bg-id-emerald",
  teal: "bg-id-teal",
  cyan: "bg-id-cyan",
  sky: "bg-id-sky",
  blue: "bg-id-blue",
  indigo: "bg-id-indigo",
  violet: "bg-id-violet",
  fuchsia: "bg-id-fuchsia",
  rose: "bg-id-rose",
} as const;

export type IdentityColor = keyof typeof identityPalette;

const names = Object.keys(identityPalette) as IdentityColor[];

/** hashString is a small stable string hash (FNV-1a, 32 bit). */
export function hashString(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

/**
 * identityColor picks a colour for a key: the preferred palette name when
 * it is one (a coin profile's `color`), otherwise one derived from the key.
 */
export function identityColor(key: string, preferred?: string): IdentityColor {
  if (preferred && preferred in identityPalette) return preferred as IdentityColor;
  return names[hashString(key.toUpperCase()) % names.length] ?? "blue";
}

/** identityClass returns the background class of a key's colour. */
export function identityClass(key: string, preferred?: string): string {
  return identityPalette[identityColor(key, preferred)];
}
