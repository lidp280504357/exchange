import { describe, expect, it } from "vitest";
import { checkAntiPhishing, maskCode } from "./profile";

describe("anti-phishing code", () => {
  it("wants 4 to 20 letters and digits, or nothing", () => {
    expect(checkAntiPhishing("")).toBeNull();
    expect(checkAntiPhishing("Ab12")).toBeNull();
    expect(checkAntiPhishing("x".repeat(20))).toBeNull();
    expect(checkAntiPhishing("Ab1")).toBe("length");
    expect(checkAntiPhishing("x".repeat(21))).toBe("length");
    expect(checkAntiPhishing("ab 12")).toBe("chars");
    expect(checkAntiPhishing("码码码码")).toBe("chars");
  });

  it("masks all but the first two characters", () => {
    expect(maskCode("Ab3x9")).toBe("Ab***");
    expect(maskCode("Ab3x9Kq7")).toBe("Ab******");
    expect(maskCode("Ab12")).toBe("Ab***");
    expect(maskCode("")).toBe("");
  });
});
