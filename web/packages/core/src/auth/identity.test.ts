import { describe, expect, it } from "vitest";
import { checkEmail, checkPhone, isParsed, kindOfMask, maskIdentifier, normalizePhone, parseIdentifier } from "./identity";

describe("identifiers", () => {
  it("checks emails like the server", () => {
    expect(checkEmail(" Alice@Example.com ")).toBeNull();
    expect(checkEmail("")).toBe("empty");
    expect(checkEmail("alice")).toBe("email");
    expect(checkEmail("@example.com")).toBe("email");
    expect(checkEmail("alice@example")).toBe("email");
    expect(checkEmail("alice@.example.com")).toBe("email");
    expect(checkEmail("alice@example.com.")).toBe("email");
    expect(checkEmail("al ice@example.com")).toBe("email");
    expect(checkEmail("alice@ex@ample.com")).toBe("email");
  });

  it("wants phone numbers with their country code", () => {
    expect(normalizePhone(" +86 138-1234 (1234) ")).toBe("+8613812341234");
    expect(normalizePhone("0065 9123 4567")).toBe("+6591234567");
    expect(checkPhone("+86 138 1234 1234")).toBeNull();
    expect(checkPhone("13812341234")).toBe("phoneCode");
    expect(checkPhone("+0123")).toBe("phone");
    expect(checkPhone("+86abc")).toBe("phone");
    expect(checkPhone(" ")).toBe("empty");
  });

  it("parses and infers the kind", () => {
    const email = parseIdentifier(" Bob@Example.COM ");
    expect(isParsed(email) && email).toEqual({ kind: "EMAIL", channel: "EMAIL", value: "bob@example.com" });
    const phone = parseIdentifier("+65 9123 4567");
    expect(isParsed(phone) && phone).toEqual({ kind: "PHONE", channel: "SMS", value: "+6591234567" });
    expect(parseIdentifier("91234567")).toEqual({ problem: "phoneCode" });
    expect(parseIdentifier("bob@example.com", "PHONE")).toEqual({ problem: "phone" });
  });

  it("masks like the server", () => {
    expect(maskIdentifier("alice@example.com")).toBe("a***@example.com");
    expect(maskIdentifier("+8613812341234")).toBe("+86138****1234");
    expect(maskIdentifier("+6591234")).toBe("+65****34");
    expect(maskIdentifier("123")).toBe("****");
    expect(kindOfMask("a***@example.com")).toBe("EMAIL");
    expect(kindOfMask("+86138****1234")).toBe("PHONE");
    expect(kindOfMask("***")).toBeNull();
  });
});
