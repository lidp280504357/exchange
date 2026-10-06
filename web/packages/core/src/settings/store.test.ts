import { describe, expect, it } from "vitest";
import { localeOf } from "./store";

describe("localeOf", () => {
  it("takes Traditional Chinese for zh-Hant, Taiwan, Hong Kong and Macao", () => {
    for (const tag of ["zh-TW", "zh-HK", "zh-MO", "zh-Hant", "zh-Hant-TW", "zh-hant-hk", "zh-TW-x-test"]) expect(localeOf(tag), tag).toBe("zh-TW");
  });

  it("takes Simplified Chinese for the other Chinese, English for the rest", () => {
    for (const tag of ["zh", "zh-CN", "zh-SG", "zh-Hans", "zh-Hans-HK", "ZH-cn"]) expect(localeOf(tag), tag).toBe("zh-CN");
    for (const tag of ["en", "en-US", "ja-JP", "fr", ""]) expect(localeOf(tag), tag).toBe("en");
  });
});
