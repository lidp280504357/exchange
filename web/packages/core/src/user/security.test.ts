import { describe, expect, it } from "vitest";
import { deriveIdentities, firstChannel, groupSecret, securitySummary, stepUpChannels, stepUpMethodFor } from "./security";

const login = (identity: string, at: string) => ({ identity, created_at: at });
const changed = (channel: string, mask: string, at: string, old?: string) => {
  const data: Record<string, string> = { channel, new: mask };
  if (old) data.old = old;
  return { type: "IDENTITY_CHANGED", data, created_at: at };
};

describe("bound identities", () => {
  it("takes the latest masked identity per kind", () => {
    const got = deriveIdentities(
      [login("a***@example.com", "2026-09-30T10:00:00Z"), login("+86138****1234", "2026-09-29T10:00:00Z"), login("o***@old.com", "2026-09-01T10:00:00Z")],
      [changed("EMAIL", "a***@example.com", "2026-09-10T10:00:00Z", "o***@old.com")],
    );
    expect(got).toEqual({ EMAIL: "a***@example.com", PHONE: "+86138****1234" });
  });

  it("learns a bound identity from its notice when it was never used to sign in", () => {
    const got = deriveIdentities([login("a***@example.com", "2026-09-01T10:00:00Z")], [changed("SMS", "+65****67", "2026-09-20T10:00:00Z")]);
    expect(got).toEqual({ EMAIL: "a***@example.com", PHONE: "+65****67" });
  });

  it("prefers a newer rebind over older sign-ins", () => {
    const got = deriveIdentities([login("o***@old.com", "2026-09-01T10:00:00Z")], [changed("EMAIL", "n***@new.com", "2026-09-05T10:00:00Z", "o***@old.com")]);
    expect(got.EMAIL).toBe("n***@new.com");
  });

  it("ignores what it cannot place", () => {
    const got = deriveIdentities(
      [login("***", "2026-09-01T10:00:00Z")],
      [{ type: "WELCOME", data: {}, created_at: "2026-09-01T10:00:00Z" }, changed("PIGEON", "x", "2026-09-02T10:00:00Z")],
    );
    expect(got).toEqual({});
  });
});

describe("step-up for bindings", () => {
  it("uses the authenticator app first", () => {
    expect(stepUpMethodFor("rebind", "EMAIL", { EMAIL: "a", PHONE: "b" }, true)).toBe("TOTP");
  });
  it("binds through the identity the account has", () => {
    expect(stepUpMethodFor("bind", "PHONE", { EMAIL: "a" }, false)).toBe("EMAIL");
    expect(stepUpMethodFor("bind", "EMAIL", { PHONE: "b" }, false)).toBe("SMS");
    // Nothing known: binding a phone still means the account has an email.
    expect(stepUpMethodFor("bind", "PHONE", {}, false)).toBe("EMAIL");
  });
  it("rebinds through the other identity, or itself when alone", () => {
    expect(stepUpMethodFor("rebind", "EMAIL", { EMAIL: "a", PHONE: "b" }, false)).toBe("SMS");
    expect(stepUpMethodFor("rebind", "PHONE", { EMAIL: "a", PHONE: "b" }, false)).toBe("EMAIL");
    expect(stepUpMethodFor("rebind", "EMAIL", { EMAIL: "a" }, false)).toBe("EMAIL");
  });
});

describe("the step-up's channels (F32)", () => {
  it("offers both, with their targets, a kind the account lacks not bound", () => {
    expect(stepUpChannels({ EMAIL: "a***@example.com", PHONE: "+86138****1234" })).toEqual([
      { channel: "EMAIL", target: "a***@example.com", bound: true },
      { channel: "SMS", target: "+86138****1234", bound: true },
    ]);
    const phoneOnly = stepUpChannels({ PHONE: "+86138****1234" });
    expect(phoneOnly.map((c) => [c.channel, c.bound])).toEqual([
      ["EMAIL", false],
      ["SMS", true],
    ]);
    expect(firstChannel(phoneOnly)).toBe("SMS");
    expect(firstChannel(stepUpChannels({ EMAIL: "a***@example.com" }))).toBe("EMAIL");
  });
  it("offers both without targets when it knows neither", () => {
    for (const none of [undefined, {}]) {
      expect(stepUpChannels(none).map((c) => [c.channel, c.target, c.bound])).toEqual([
        ["EMAIL", undefined, true],
        ["SMS", undefined, true],
      ]);
    }
    expect(firstChannel(stepUpChannels(undefined))).toBe("EMAIL");
  });
});

describe("security summary", () => {
  it("scores and levels the account", () => {
    const base = { totp: false, email: true, phone: false, antiPhishing: false };
    expect(securitySummary(base)).toEqual({ score: 2, max: 6, level: "low", missing: ["totp", "phone", "antiPhishing"] });
    expect(securitySummary({ ...base, phone: true, antiPhishing: true }).level).toBe("medium");
    // High needs the authenticator app.
    expect(securitySummary({ ...base, totp: true }).level).toBe("medium");
    expect(securitySummary({ ...base, totp: true, antiPhishing: true })).toMatchObject({ score: 5, level: "high", missing: ["phone"] });
    expect(securitySummary({ totp: true, email: true, phone: true, antiPhishing: true })).toMatchObject({ score: 6, missing: [] });
  });
});

describe("authenticator secret", () => {
  it("reads in groups of four", () => {
    expect(groupSecret("JBSWY3DPEHPK3PXPJB")).toBe("JBSW Y3DP EHPK 3PXP JB");
    expect(groupSecret(" jbsw y3dp ")).toBe("jbsw y3dp");
    expect(groupSecret("")).toBe("");
  });
});
