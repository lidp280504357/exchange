import { describe, expect, it } from "vitest";
import { androidVersion, devicePlatform, fileSize, installUrl, offeredApps, parsePlatform, qrUrl, type AppDownload } from "./apps";

const notes = { "zh-CN": "", en: "" };
const link: AppDownload = {
  mode: "LINK", url: "https://apps.apple.com/app/id1", install_url: null, ios_install: "APP_STORE", package: null, version: null, build: null,
  min_os: null, size: null, sha256: null, mobileconfig_url: null, notes, updated_at: "2026-10-07T03:00:00Z",
};
const apk: AppDownload = {
  mode: "FILE", url: "https://astras.vip/downloads/android/0192a000-0000-7000-8000-000000000001.apk", install_url: null, ios_install: null,
  package: "vip.astras.app", version: "1.2.0", build: "42", min_os: "24", size: 50_541_363, sha256: "ab".repeat(32), mobileconfig_url: null, notes,
  updated_at: "2026-10-07T03:00:00Z",
};
const ipa: AppDownload = {
  ...apk, url: "https://astras.vip/downloads/ios/0192a000-0000-7000-8000-000000000002.ipa", ios_install: "OTA",
  install_url: "itms-services://?action=download-manifest&url=https://astras.vip/downloads/ios/0192a000-0000-7000-8000-000000000002.plist",
};

describe("the apps to download", () => {
  it("list the offered ones, the phone's own first", () => {
    expect(offeredApps(undefined)).toEqual([]);
    expect(offeredApps({ android: null, ios: null })).toEqual([]);
    expect(offeredApps({ android: apk, ios: link }).map((a) => a.platform)).toEqual(["android", "ios"]);
    expect(offeredApps({ android: apk, ios: link }, "ios").map((a) => a.platform)).toEqual(["ios", "android"]);
    expect(offeredApps({ android: null, ios: link }, "android").map((a) => a.platform)).toEqual(["ios"]);
  });

  it("know the phone's platform", () => {
    expect(devicePlatform("Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15")).toBe("ios");
    expect(devicePlatform("Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 Chrome/140.0 Mobile Safari/537.36")).toBe("android");
    // An iPad asking for the desktop site says Macintosh, but it has touch.
    expect(devicePlatform("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15", 5)).toBe("ios");
    expect(devicePlatform("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15", 0)).toBeNull();
    expect(devicePlatform("Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/140.0")).toBeNull();
    expect(parsePlatform("ios")).toBe("ios");
    expect(parsePlatform("IOS")).toBeNull();
    expect(parsePlatform(null)).toBeNull();
  });

  it("install a link or a file, an uploaded iOS app over the air", () => {
    expect(installUrl(link)).toBe(link.url);
    expect(installUrl(apk)).toBe(apk.url);
    expect(installUrl(ipa)).toMatch(/^itms-services:/);
  });

  it("put a link in its QR code, the download page for a file", () => {
    expect(qrUrl("ios", link, "https://astras.vip/download")).toBe(link.url);
    expect(qrUrl("android", apk, "https://astras.vip/download")).toBe("https://astras.vip/download?platform=android");
    expect(qrUrl("ios", ipa, "https://astras.vip/download")).toBe("https://astras.vip/download?platform=ios");
  });

  it("name an Android API level's version", () => {
    expect(androidVersion("24")).toBe("7.0");
    expect(androidVersion("35")).toBe("15");
    expect(androidVersion("19")).toBeNull();
    expect(androidVersion(null)).toBeNull();
  });

  it("show sizes in KB and MB", () => {
    expect(fileSize(50_541_363)).toBe("48.2 MB");
    expect(fileSize(880_000)).toBe("859 KB");
    expect(fileSize(512)).toBe("512 B");
    expect(fileSize(1_610_612_736)).toBe("1,536.0 MB");
    expect(fileSize(null)).toBe("—");
  });
});
