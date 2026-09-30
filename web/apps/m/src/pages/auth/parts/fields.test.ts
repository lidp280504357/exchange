import { describe, expect, it } from "vitest";
import { clock, digitsOnly, identifierProblemKey, parsedOrNull } from "./fields";

describe("identifier fields", () => {
  it("names the problem of what was typed", () => {
    expect(identifierProblemKey("ann@example.com")).toBeNull();
    expect(identifierProblemKey("+65 9123 4567")).toBeNull();
    expect(identifierProblemKey("")).toBe("empty");
    expect(identifierProblemKey("ann@example")).toBe("email");
    expect(identifierProblemKey("91234567")).toBe("phoneCode");
    // Either kind: letters without "@" are neither.
    expect(identifierProblemKey("ann.example.com")).toBe("either");
    // One kind: the kind's own message.
    expect(identifierProblemKey("ann.example.com", "EMAIL")).toBe("email");
    expect(identifierProblemKey("ann@example.com", "PHONE")).toBe("phone");
  });

  it("normalizes what parses", () => {
    expect(parsedOrNull(" Ann@Example.com ")).toEqual({ kind: "EMAIL", channel: "EMAIL", value: "ann@example.com" });
    expect(parsedOrNull("0065 9123 4567", "PHONE")?.value).toBe("+6591234567");
    expect(parsedOrNull("nope")).toBeNull();
  });
});

describe("clock", () => {
  it("counts a lock down in minutes and seconds", () => {
    expect(clock(900)).toBe("15:00");
    expect(clock(61.2)).toBe("1:02");
    expect(clock(5)).toBe("0:05");
    expect(clock(-3)).toBe("0:00");
  });
});

describe("digitsOnly", () => {
  it("keeps up to six digits of a pasted code", () => {
    expect(digitsOnly("123 456")).toBe("123456");
    expect(digitsOnly("code: 98-76-54-32")).toBe("987654");
    expect(digitsOnly("abc")).toBe("");
    expect(digitsOnly("12345678", 8)).toBe("12345678");
  });
});
