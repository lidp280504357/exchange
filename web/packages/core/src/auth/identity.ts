import type { OtpChannel } from "./otp";

// Emails and phone numbers as accounts use them (requirements §5.2):
// emails are trimmed and lower-cased; phone numbers carry their country
// code (E.164, +8613812341234). The checks mirror auth-service's
// domain.ParseIdentifier closely enough to catch typos before a request;
// the server has the final say (libphonenumber validates the number).

/** The kind of an identity: an email address or a phone number. */
export type IdentityKind = "EMAIL" | "PHONE";

/** channelOf is the OTP channel that reaches an identity kind. */
export function channelOf(kind: IdentityKind): OtpChannel {
  return kind === "PHONE" ? "SMS" : "EMAIL";
}

/** kindOf tells an email from a phone number (anything with "@" is an email). */
export function kindOf(identifier: string): IdentityKind {
  return identifier.includes("@") ? "EMAIL" : "PHONE";
}

/** Why an identifier cannot be used as typed. */
export type IdentifierProblem = "empty" | "email" | "phone" | "phoneCode";

export function normalizeEmail(raw: string): string {
  return raw.trim().toLowerCase();
}

/**
 * normalizePhone drops the spaces, dashes, dots and brackets people type
 * and turns an international 00 prefix into "+".
 */
export function normalizePhone(raw: string): string {
  const compact = raw.trim().replace(/[\s\-.()]/g, "");
  return compact.startsWith("00") ? `+${compact.slice(2)}` : compact;
}

/** checkEmail mirrors the server's email rules. */
export function checkEmail(raw: string): IdentifierProblem | null {
  const v = normalizeEmail(raw);
  if (!v) return "empty";
  const at = v.indexOf("@");
  if (at <= 0) return "email";
  const domain = v.slice(at + 1);
  if (
    !domain.includes(".") ||
    domain.includes("@") ||
    /[\s<>()[\],;:"]/.test(v) ||
    domain.startsWith(".") ||
    domain.endsWith(".") ||
    v.length > 254
  ) {
    return "email";
  }
  return null;
}

/** checkPhone wants a country code and 7 to 15 digits (E.164). */
export function checkPhone(raw: string): IdentifierProblem | null {
  const v = normalizePhone(raw);
  if (!v) return "empty";
  if (!v.startsWith("+")) return /^\d+$/.test(v) ? "phoneCode" : "phone";
  return /^\+[1-9]\d{6,14}$/.test(v) ? null : "phone";
}

export type ParsedIdentifier = { kind: IdentityKind; channel: OtpChannel; value: string };

/**
 * parseIdentifier normalizes an email or a phone number; without `kind`
 * it is inferred ("@" means email). Returns the problem instead when the
 * input cannot be right.
 */
export function parseIdentifier(raw: string, kind: IdentityKind = kindOf(raw)): ParsedIdentifier | { problem: IdentifierProblem } {
  const problem = kind === "EMAIL" ? checkEmail(raw) : checkPhone(raw);
  if (problem) return { problem };
  const value = kind === "EMAIL" ? normalizeEmail(raw) : normalizePhone(raw);
  return { kind, channel: channelOf(kind), value };
}

/** isParsed narrows parseIdentifier's result. */
export function isParsed(r: ParsedIdentifier | { problem: IdentifierProblem }): r is ParsedIdentifier {
  return !("problem" in r);
}

/**
 * maskIdentifier hides most of an identity the way the server does
 * (internal/platform/pii): a***@example.com, +86138****1234.
 */
export function maskIdentifier(value: string): string {
  const v = value.trim();
  if (v.includes("@")) {
    const at = v.indexOf("@");
    const local = v.slice(0, at);
    const domain = v.slice(at + 1);
    if (!local || !domain) return maskMiddle(v);
    return `${[...local][0]}***@${domain}`;
  }
  const n = v.length;
  if (n >= 11) return `${v.slice(0, 6)}****${v.slice(n - 4)}`;
  if (n >= 6) return `${v.slice(0, 3)}****${v.slice(n - 2)}`;
  return "****";
}

function maskMiddle(s: string): string {
  const r = [...s];
  if (r.length <= 2) return "***";
  return `${r[0]}***${r[r.length - 1]}`;
}

/** kindOfMask tells which kind a masked identity is (the server masks both shapes). */
export function kindOfMask(mask: string): IdentityKind | null {
  if (mask.includes("@")) return "EMAIL";
  if (mask.startsWith("+")) return "PHONE";
  return null;
}
