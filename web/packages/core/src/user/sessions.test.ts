import { describe, expect, it } from "vitest";
import { describeDevice, deviceName, flattenHistory, sortSessions, type DeviceSession, type LoginEvent } from "./sessions";

const UA = {
  chromeMac: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
  edgeWin: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 Edg/128.0.2739.42",
  safariIphone: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
  chromeAndroid: "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Mobile Safari/537.36",
  ipad: "Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
  firefoxLinux: "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0",
};

describe("devices", () => {
  it("names browsers, systems and kinds", () => {
    expect(describeDevice(UA.chromeMac)).toEqual({ kind: "desktop", browser: "Chrome 128", os: "macOS" });
    expect(describeDevice(UA.edgeWin)).toEqual({ kind: "desktop", browser: "Edge 128", os: "Windows" });
    expect(describeDevice(UA.safariIphone)).toEqual({ kind: "mobile", browser: "Safari 17", os: "iOS" });
    expect(describeDevice(UA.chromeAndroid)).toEqual({ kind: "mobile", browser: "Chrome 128", os: "Android" });
    expect(describeDevice(UA.ipad)).toEqual({ kind: "tablet", browser: "Safari 17", os: "iPadOS" });
    expect(describeDevice(UA.firefoxLinux)).toEqual({ kind: "desktop", browser: "Firefox 130", os: "Linux" });
  });

  it("says little about what it does not know", () => {
    expect(describeDevice("Go-http-client/1.1")).toEqual({ kind: "unknown", browser: "", os: "" });
    expect(describeDevice("", "APP").kind).toBe("mobile");
    expect(deviceName(describeDevice(UA.chromeMac))).toBe("Chrome 128 · macOS");
    expect(deviceName(describeDevice(""))).toBe("");
  });

  it("puts this session first, then the most recently seen", () => {
    const s = (id: string, seen: string, current = false) => ({ session_id: id, last_seen_at: seen, current }) as DeviceSession;
    const sorted = sortSessions([s("a", "2026-09-29T00:00:00Z"), s("b", "2026-09-28T00:00:00Z", true), s("c", "2026-09-30T00:00:00Z")]);
    expect(sorted.map((x) => x.session_id)).toEqual(["b", "c", "a"]);
  });

  it("joins sign-in pages once each", () => {
    const e = (at: string, result = "SUCCESS") =>
      ({ method: "PASSWORD", result, identity: "a***@example.com", device_id: "web-1", user_agent: "", ip: "1.2.*.*", new_device: false, created_at: at }) as LoginEvent;
    const rows = flattenHistory([{ items: [e("2026-09-30T10:00:00Z"), e("2026-09-30T09:00:00Z")] }, { items: [e("2026-09-30T09:00:00Z"), e("2026-09-30T09:00:00Z", "LOCKED")] }]);
    expect(rows.map((r) => r.result)).toEqual(["SUCCESS", "SUCCESS", "LOCKED"]);
    expect(new Set(rows.map((r) => r.key)).size).toBe(3);
  });
});
