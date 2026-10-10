import { describe, expect, it } from "vitest";
import { ALL, chosen } from "./filters";

// A select shows the option of a value from the address in any letter case:
// the server takes ?kind=bot as BOT (A107).
describe("chosen", () => {
  const options = [{ value: ALL }, { value: "BOT" }, { value: "TEST" }];

  it("is the option of the value, in any case", () => {
    expect(chosen(options, "BOT")).toBe("BOT");
    expect(chosen(options, "bot")).toBe("BOT");
    expect(chosen(options, "Test")).toBe("TEST");
  });

  it("is ALL without a value, the value itself for no option", () => {
    expect(chosen(options, "")).toBe(ALL);
    expect(chosen(options, "ROBOT")).toBe("ROBOT");
  });
});
