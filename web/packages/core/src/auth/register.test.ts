import { describe, expect, it } from "vitest";
import { ApiError } from "../api/errors";
import {
  DEFAULT_REGION, guessRegion, initialRegisterState, regionName, REGIONS, registerReducer, termsFrom, ticketFresh, TICKET_FRESH_MS,
  type RegisterState,
} from "./register";

const err = (code: string, details: Record<string, unknown> = {}) => new ApiError(400, code, code, details);

describe("regions", () => {
  it("lists every ISO 3166-1 code once", () => {
    expect(REGIONS).toHaveLength(249);
    expect(new Set(REGIONS).size).toBe(249);
    expect(REGIONS.every((r) => /^[A-Z]{2}$/.test(r))).toBe(true);
  });

  it("guesses from the language, then the time zone", () => {
    expect(guessRegion(["en-GB", "en"])).toBe("GB");
    expect(guessRegion(["zh-Hant-TW"])).toBe("TW");
    expect(guessRegion(["zh"], "Asia/Shanghai")).toBe("CN");
    expect(guessRegion(["en"], "America/Chicago")).toBe("US");
    // Not a region code: "419" (Latin America), "EU".
    expect(guessRegion(["es-419", "en-EU"], "Nowhere/City")).toBe(DEFAULT_REGION);
    expect(guessRegion([])).toBe(DEFAULT_REGION);
  });

  it("names regions in the language", () => {
    expect(regionName("SG", "en")).toBe("Singapore");
    expect(regionName("SG", "zh-CN")).toBe("新加坡");
  });
});

describe("terms", () => {
  it("reads the current versions of AUTH_TERMS_OUTDATED", () => {
    expect(termsFrom(err("AUTH_TERMS_OUTDATED", { terms_version: "2026-10", risk_disclosure_version: "3" }))).toEqual({
      terms_version: "2026-10",
      risk_disclosure_version: "3",
    });
    expect(termsFrom(err("AUTH_TERMS_OUTDATED"))).toBeNull();
    expect(termsFrom(err("AUTH_PASSWORD_WEAK", { terms_version: "x", risk_disclosure_version: "y" }))).toBeNull();
  });
});

describe("sign-up flow", () => {
  const id = "ann@example.com";
  const verified = (at = 1_000): RegisterState => {
    const s = registerReducer(initialRegisterState, { type: "continue", identifier: id, now: 0 });
    return registerReducer(s, { type: "ticket", ticket: "tk", now: at });
  };

  it("goes from the form to the code to the request", () => {
    const s = registerReducer(initialRegisterState, { type: "continue", identifier: id, now: 0 });
    expect(s).toMatchObject({ step: "verify", identifier: id, ticket: "" });
    expect(verified()).toMatchObject({ step: "submitting", ticket: "tk", ticketAt: 1_000 });
  });

  it("keeps a fresh ticket when the form must be fixed", () => {
    const back = registerReducer(verified(), { type: "failed", error: err("AUTH_PASSWORD_WEAK") });
    expect(back).toMatchObject({ step: "form", ticket: "tk" });
    // Same identifier, still fresh: straight to the request.
    expect(registerReducer(back, { type: "continue", identifier: id, now: 2_000 }).step).toBe("submitting");
    // Too old, or another identifier: a new code.
    expect(registerReducer(back, { type: "continue", identifier: id, now: 1_000 + TICKET_FRESH_MS }).step).toBe("verify");
    const other = registerReducer(back, { type: "continue", identifier: "bob@example.com", now: 2_000 });
    expect(other).toMatchObject({ step: "verify", identifier: "bob@example.com", ticket: "" });
  });

  it("asks for a new code when the ticket is spent", () => {
    const s = registerReducer(verified(), { type: "failed", error: err("AUTH_TICKET_INVALID") });
    expect(s).toMatchObject({ step: "verify", ticket: "", attempt: 1 });
    expect(ticketFresh(s, id, 2_000)).toBe(false);
  });

  it("drops the ticket of a taken identifier", () => {
    const s = registerReducer(verified(), { type: "failed", error: err("AUTH_IDENTITY_TAKEN") });
    expect(s).toMatchObject({ step: "form", ticket: "" });
  });

  it("goes back to the form", () => {
    const s = registerReducer(registerReducer(initialRegisterState, { type: "continue", identifier: id, now: 0 }), { type: "back" });
    expect(s.step).toBe("form");
  });
});
