import { describe, expect, it } from "vitest";
import { ApiError } from "../api/errors";
import {
  initialResetState, passwordAcceptable, passwordChecks, passwordStrength, repetitive, resetReducer, sequential,
} from "./password";

// The rules of internal/auth/domain/password.go that the page can judge:
// length, repeats, runs and the identity. Common passwords are the
// server's call (AUTH_PASSWORD_WEAK), so the page does not judge them.
// The strings are made-up probes of each rule.
const cases: { pw: string; ids?: string[]; weak: boolean }[] = [
  { pw: "plum orbit 47", weak: false },
  { pw: "4829107365", weak: false },
  { pw: "短短的中文密码也可以很长", weak: false },
  { pw: "short1!", weak: true },
  { pw: "x".repeat(129), weak: true },
  { pw: "aaaaaaaaaaaa", weak: true },
  { pw: "abcabcabcabc", weak: true },
  { pw: "0123456789", weak: true },
  { pw: "123456789012", weak: true },
  { pw: "zyxwvutsrqpo", weak: true },
  { pw: "alice.smith2026", ids: ["alice.smith@example.com"], weak: true },
  { pw: "my+8613800138000pw", ids: ["+8613800138000"], weak: true },
  // A local part under 4 bytes is too short to count.
  { pw: "bobcat river 9", ids: ["bob@example.com"], weak: false },
];

describe("password rules", () => {
  it.each(cases)("agrees with the server on %j", ({ pw, ids, weak }) => {
    expect(passwordAcceptable(pw, ids)).toBe(!weak);
  });

  it("reports each rule on its own", () => {
    const byRule = (pw: string, ids?: string[]) => Object.fromEntries(passwordChecks(pw, ids).map((c) => [c.rule, c.ok]));
    expect(byRule("")).toEqual({ length: false, pattern: false });
    expect(byRule("short1!")).toEqual({ length: false, pattern: true });
    expect(byRule("aaaaaaaaaaaa")).toMatchObject({ length: true, pattern: false });
    expect(byRule("alice.smith2026", ["alice.smith@example.com"])).toEqual({ length: true, pattern: true, identity: false });
    // Without an identifier the identity rule is not shown.
    expect(passwordChecks("alice.smith2026").map((c) => c.rule)).not.toContain("identity");
    expect(passwordChecks("alice.smith2026", [" "]).map((c) => c.rule)).not.toContain("identity");
  });

  it("finds runs and repeats", () => {
    expect(repetitive("abab")).toBe(true);
    expect(repetitive("abcd")).toBe(false);
    expect(sequential("7890123")).toBe(true);
    expect(sequential("fedcba")).toBe(true);
    expect(sequential("abce")).toBe(false);
  });

  it("walks the reset flow", () => {
    const e = (code: string) => new ApiError(400, code, code);
    let s = resetReducer(initialResetState, { type: "identified", identifier: "ann@example.com" });
    expect(s).toMatchObject({ step: "verify", identifier: "ann@example.com" });
    s = resetReducer(s, { type: "ticket", ticket: "tk" });
    expect(s).toMatchObject({ step: "password", ticket: "tk" });
    // A weak password stays on the password step with the ticket.
    expect(resetReducer(s, { type: "failed", error: e("AUTH_PASSWORD_WEAK") })).toBe(s);
    // A spent ticket needs a new code.
    const spent = resetReducer(s, { type: "failed", error: e("AUTH_TICKET_INVALID") });
    expect(spent).toMatchObject({ step: "verify", ticket: "", attempt: 1 });
    expect(resetReducer(spent, { type: "back" })).toMatchObject({ step: "identify", ticket: "" });
  });

  it("scores strength by length and mix", () => {
    expect(passwordStrength("")).toBe(0);
    expect(passwordStrength("aaaaaaaaaaaa")).toBe(0);
    expect(passwordStrength("4829107365")).toBe(1);
    expect(passwordStrength("quietmeadows")).toBe(2);
    expect(passwordStrength("Kx7#quill-Ro")).toBe(3);
    expect(passwordStrength("lantern over the bay")).toBe(3);
    expect(passwordStrength("Lantern over the bay 9")).toBe(4);
  });
});
