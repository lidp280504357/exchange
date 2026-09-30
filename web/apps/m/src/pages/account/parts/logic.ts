import type { BoundIdentities } from "@exchange/core/user/security";

// Small pure rules of the "me" tab and the account pages.

/** shortId keeps the ends of an ID: "0192e4c8…7e8f". */
export function shortId(id: string): string {
  return id.length > 14 ? `${id.slice(0, 8)}…${id.slice(-4)}` : id;
}

/** primaryIdentity is the identity the "me" card shows: the masked email, else the phone number, else null. */
export function primaryIdentity(bound: BoundIdentities | undefined): string | null {
  return bound?.EMAIL || bound?.PHONE || null;
}

/** countBadge caps a count for a badge: 5 → "5", 120 → "99+". */
export function countBadge(n: number): string {
  const v = Math.max(0, Math.floor(n));
  return v > 99 ? "99+" : String(v);
}

export type TotpState = "on" | "pending" | "off";

/** totpState reads the authenticator binding: bound, set up but unconfirmed, or none. */
export function totpState(status: { enabled: boolean; pending: boolean } | undefined): TotpState {
  if (status?.enabled === true) return "on";
  return status?.pending === true ? "pending" : "off";
}

/** How many rows of a list get the entrance stagger (design §5.3: the first screen only). */
export const FIRST_SCREEN_ROWS = 12;

/** entrance is a row's motion `initial`: the stagger for the first screen, none for rows loaded later. */
export function entrance(index: number): "initial" | false {
  return index < FIRST_SCREEN_ROWS ? "initial" : false;
}
