import { describe, expect, it } from "vitest";
import { plainAmount, shownDecimals, withQuery } from "./meta";

describe("assets page helpers", () => {
  it("shows rule amounts without padding, never rounded up", () => {
    expect(plainAmount("0.001000000000000000", 18)).toBe("0.001");
    expect(plainAmount("0.0002", 18)).toBe("0.0002");
    expect(plainAmount("1000.5", 6)).toBe("1,000.5");
    expect(plainAmount("10", 8)).toBe("10");
    expect(plainAmount("0.123456789", 6)).toBe("0.123456");
    expect(plainAmount(null, 8)).toBe("—");
  });

  it("caps list decimals at 8", () => {
    expect(shownDecimals(18)).toBe(8);
    expect(shownDecimals(6)).toBe(6);
  });

  it("builds links with their query, skipping empty values", () => {
    expect(withQuery("/assets/deposit", { asset: "ETH" })).toBe("/assets/deposit?asset=ETH");
    expect(withQuery("/assets/transfer", { asset: "USDT", from: "FUTURES" })).toBe("/assets/transfer?asset=USDT&from=FUTURES");
    expect(withQuery("/assets/withdraw", { asset: "", network: null })).toBe("/assets/withdraw");
  });
});
