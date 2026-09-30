import { isParsed, kindOf, parseIdentifier, type IdentityKind, type ParsedIdentifier } from "@exchange/core/auth/identity";
import type { useTranslation } from "react-i18next";

// Field rules shared by sign-in, sign-up, reset and the account's sheets:
// the checks live in @exchange/core, the messages under mAuth.

export type Translate = ReturnType<typeof useTranslation>["t"];

/**
 * identifierProblemKey is the mAuth.idProblem.* key of a typed email or
 * phone number, or null when it parses. Without a kind (a field taking
 * either), text with letters and no "@" reads "either".
 */
export function identifierProblemKey(raw: string, kind?: IdentityKind): string | null {
  const r = parseIdentifier(raw, kind ?? kindOf(raw));
  if (isParsed(r)) return null;
  if (!kind && r.problem !== "empty" && !raw.includes("@") && /\p{L}/u.test(raw)) return "either";
  return r.problem;
}

/** parsedOrNull normalizes an identifier, or returns null when it cannot be right. */
export function parsedOrNull(raw: string, kind?: IdentityKind): ParsedIdentifier | null {
  const r = parseIdentifier(raw, kind ?? kindOf(raw));
  return isParsed(r) ? r : null;
}

/** clock renders seconds as m:ss (a lock's countdown). */
export function clock(seconds: number): string {
  const s = Math.max(0, Math.ceil(seconds));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

/** digitsOnly keeps the digits of a typed or pasted code, at most `max` of them. */
export function digitsOnly(raw: string, max = 6): string {
  return raw.replace(/\D/g, "").slice(0, max);
}
