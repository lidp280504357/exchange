import { describe, expect, it } from "vitest";
import { offsetMinutes, zoneLabel, zoneOffset, zoneOptions } from "./preferences";

// A winter moment: no daylight saving in the northern zones.
const JAN = Date.parse("2026-01-15T12:00:00Z");

describe("time zones", () => {
  it("gives each zone its offset at a moment", () => {
    expect(zoneOffset("Asia/Shanghai", JAN)).toBe("UTC+08:00");
    expect(zoneOffset("America/New_York", JAN)).toBe("UTC-05:00");
    expect(zoneOffset("Asia/Kolkata", JAN)).toBe("UTC+05:30");
    expect(zoneOffset("UTC", JAN)).toBe("UTC+00:00");
    expect(zoneOffset("Not/AZone", JAN)).toBe("");
  });

  it("reads offsets as minutes", () => {
    expect(offsetMinutes("UTC+05:30")).toBe(330);
    expect(offsetMinutes("UTC-03:00")).toBe(-180);
    expect(offsetMinutes("")).toBe(0);
  });

  it("lists UTC first, then west to east", () => {
    const list = zoneOptions(["Asia/Tokyo", "America/New_York", "Europe/London", "Not/AZone", "Asia/Shanghai"], JAN);
    expect(list.map((z) => z.zone)).toEqual(["UTC", "America/New_York", "Europe/London", "Asia/Shanghai", "Asia/Tokyo"]);
  });

  it("reads zone names aloud", () => {
    expect(zoneLabel("America/New_York")).toBe("America/New York");
  });
});
