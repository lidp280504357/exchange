import { authApi, unwrap } from "../api/client";
import { ApiError } from "../api/errors";
import { deviceId } from "../session/store";
import { stepUpHeaders } from "./stepup";

// The password policy (requirements §5.2), mirrored from auth-service's
// domain.CheckPassword so sign-up, reset and change show the rules live:
// 10 to 128 characters, not one short unit repeated or a run of
// consecutive characters, and not containing the email or phone number.
// The list of common passwords stays on the server, which decides
// (AUTH_PASSWORD_WEAK, shown under the field).

export const PASSWORD_MIN = 10;
export const PASSWORD_MAX = 128;

/** The rules shown under a new-password field. */
export type PasswordRule = "length" | "pattern" | "identity";

export type PasswordCheck = { rule: PasswordRule; ok: boolean };

const encoder = new TextEncoder();

// The server compares bytes of the UTF-8 form (Go strings); so do we.
function bytesOf(s: string): Uint8Array {
  return encoder.encode(s);
}

function sameBytes(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

/** repetitive: one unit of up to 3 bytes repeated ("aaaaaaaaaa", "abcabcabcabc"). */
export function repetitive(s: string): boolean {
  const b = bytesOf(s);
  for (let unit = 1; unit <= 3 && unit < b.length; unit++) {
    const repeated = new Uint8Array(b.length);
    for (let i = 0; i < b.length; i++) repeated[i] = b[i % unit]!;
    if (sameBytes(repeated, b)) return true;
  }
  return false;
}

const isDigit = (c: number) => c >= 48 && c <= 57;

/** sequential: a run up or down ("0123456789", "123456789012" wraps, "zyxwvuts"). */
export function sequential(s: string): boolean {
  const b = bytesOf(s);
  const step = (x: number, y: number, d: number) => (isDigit(x) && isDigit(y) ? y - 48 === (x - 48 + d + 10) % 10 : y === x + d);
  let up = true;
  let down = true;
  for (let i = 1; i < b.length; i++) {
    up = up && step(b[i - 1]!, b[i]!, 1);
    down = down && step(b[i - 1]!, b[i]!, -1);
  }
  return up || down;
}

function utf8Length(s: string): number {
  return bytesOf(s).length;
}

/** containsIdentity: the password holds the email, its local part (4+ bytes) or the phone. */
export function containsIdentity(pw: string, identifiers: readonly string[]): boolean {
  const lower = pw.toLowerCase();
  for (const raw of identifiers) {
    const id = raw.trim().toLowerCase();
    if (!id) continue;
    const local = id.split("@")[0] ?? "";
    if (lower.includes(id) || (utf8Length(local) >= 4 && lower.includes(local))) return true;
  }
  return false;
}

/**
 * passwordChecks evaluates each rule on its own, for the live list under
 * the field. identifiers are the email and phone the password must not
 * contain; without any, the identity rule is left out.
 */
export function passwordChecks(pw: string, identifiers: readonly string[] = []): PasswordCheck[] {
  const n = [...pw].length;
  const lower = pw.toLowerCase();
  const checks: PasswordCheck[] = [
    { rule: "length", ok: n >= PASSWORD_MIN && n <= PASSWORD_MAX },
    { rule: "pattern", ok: n > 0 && !repetitive(lower) && !sequential(lower) },
  ];
  if (identifiers.some((i) => i.trim() !== "")) checks.push({ rule: "identity", ok: n > 0 && !containsIdentity(pw, identifiers) });
  return checks;
}

/** passwordAcceptable is the server's verdict as far as the page can tell (common passwords aside). */
export function passwordAcceptable(pw: string, identifiers: readonly string[] = []): boolean {
  return passwordChecks(pw, identifiers).every((c) => c.ok);
}

/** 0 fails a rule; 1 fair, 2 good, 3 strong, 4 very strong. */
export type PasswordStrength = 0 | 1 | 2 | 3 | 4;

/**
 * passwordStrength scores an acceptable password by length and by how
 * many kinds of characters it mixes (lower, upper, digits, the rest).
 */
export function passwordStrength(pw: string, identifiers: readonly string[] = []): PasswordStrength {
  if (!passwordAcceptable(pw, identifiers)) return 0;
  const n = [...pw].length;
  const kinds = [/\p{Ll}/u, /\p{Lu}/u, /\p{Nd}/u, /[^\p{Ll}\p{Lu}\p{Nd}]/u].filter((re) => re.test(pw)).length;
  let score = 1;
  if (n >= 12) score++;
  if (n >= 16) score++;
  if (kinds >= 3) score++;
  return Math.min(4, score) as PasswordStrength;
}

/** resetPassword sets a new password with a PASSWORD_RESET ticket; every session ends. */
export async function resetPassword(ticket: string, password: string): Promise<void> {
  await unwrap(authApi.POST("/v1/auth/password/reset/complete", { body: { otp_ticket: ticket, new_password: password, device_id: deviceId() } }));
}

/** changePassword replaces the password after a step-up; the other sessions end. */
export async function changePassword(current: string, next: string, stepUpToken: string): Promise<void> {
  await unwrap(
    authApi.POST("/v1/auth/password/change", {
      params: { header: stepUpHeaders(stepUpToken) },
      body: { current_password: current, new_password: next },
    }),
  );
}

// The reset page's flow, shared by both sites: the account, its
// PASSWORD_RESET code, then the new password. A ticket that is spent or
// expired sends the page back to the code; a weak password stays put.

export type ResetStep = "identify" | "verify" | "password";

export type ResetState = {
  step: ResetStep;
  /** The normalized email or phone number the code goes to. */
  identifier: string;
  ticket: string;
  /** Bumped when the code step must start over. */
  attempt: number;
};

export const initialResetState: ResetState = { step: "identify", identifier: "", ticket: "", attempt: 0 };

export type ResetAction =
  | { type: "identified"; identifier: string }
  | { type: "ticket"; ticket: string }
  /** reset/complete failed. */
  | { type: "failed"; error: unknown }
  /** Back to the account step (another account). */
  | { type: "back" };

export function resetReducer(s: ResetState, a: ResetAction): ResetState {
  switch (a.type) {
    case "identified":
      return { ...s, step: "verify", identifier: a.identifier, ticket: "" };
    case "ticket":
      return { ...s, step: "password", ticket: a.ticket };
    case "failed": {
      const code = a.error instanceof ApiError ? a.error.code : "";
      if (code === "AUTH_TICKET_INVALID" || code.startsWith("AUTH_OTP_")) return { ...s, step: "verify", ticket: "", attempt: s.attempt + 1 };
      return s;
    }
    case "back":
      return { ...s, step: "identify", ticket: "" };
  }
}
