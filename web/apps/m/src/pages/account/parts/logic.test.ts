import { describe, expect, it } from "vitest";
import { countBadge, entrance, primaryIdentity, shortId, totpState } from "./logic";

describe("shortId", () => {
  it("keeps the ends of a long ID", () => {
    expect(shortId("0192e4c8-1b2c-7d3e-8f40-51627e8f7e8f")).toBe("0192e4c8…7e8f");
    expect(shortId("short-id")).toBe("short-id");
  });
});

describe("primaryIdentity", () => {
  it("prefers the email, then the phone", () => {
    expect(primaryIdentity({ EMAIL: "a***@example.com", PHONE: "+86138****1234" })).toBe("a***@example.com");
    expect(primaryIdentity({ PHONE: "+86138****1234" })).toBe("+86138****1234");
    expect(primaryIdentity({})).toBeNull();
    expect(primaryIdentity(undefined)).toBeNull();
  });
});

describe("countBadge", () => {
  it("caps at 99+", () => {
    expect(countBadge(0)).toBe("0");
    expect(countBadge(7)).toBe("7");
    expect(countBadge(99)).toBe("99");
    expect(countBadge(100)).toBe("99+");
    expect(countBadge(-2)).toBe("0");
  });
});

describe("totpState", () => {
  it("reads the binding", () => {
    expect(totpState({ enabled: true, pending: false })).toBe("on");
    expect(totpState({ enabled: false, pending: true })).toBe("pending");
    expect(totpState({ enabled: false, pending: false })).toBe("off");
    expect(totpState(undefined)).toBe("off");
  });
});

describe("entrance", () => {
  it("staggers only the first screen of rows", () => {
    expect(entrance(0)).toBe("initial");
    expect(entrance(11)).toBe("initial");
    expect(entrance(12)).toBe(false);
    expect(entrance(40)).toBe(false);
  });
});
