import { describe, expect, it } from "vitest";
import { LANGUAGES, localeOf, matchLocale, negotiateLocale, restoredLocale } from "./store";

describe("localeOf", () => {
  it("takes Traditional Chinese for zh-Hant, Taiwan, Hong Kong and Macao", () => {
    for (const tag of ["zh-TW", "zh-HK", "zh-MO", "zh-Hant", "zh-Hant-TW", "zh-hant-hk", "zh-TW-x-test"]) expect(localeOf(tag), tag).toBe("zh-TW");
  });

  it("takes Simplified Chinese for the other Chinese, English for the rest", () => {
    for (const tag of ["zh", "zh-CN", "zh-SG", "zh-Hans", "zh-Hans-HK", "ZH-cn"]) expect(localeOf(tag), tag).toBe("zh-CN");
    for (const tag of ["en", "en-US", "ja-JP", "fr", ""]) expect(localeOf(tag), tag).toBe("en");
  });
});

describe("the language of a visitor who has not chosen one (F30)", () => {
  it("is the first of the browser's preferences the site has", () => {
    expect(matchLocale("fr-FR")).toBeNull();
    expect(matchLocale("en-GB")).toBe("en");
    expect(negotiateLocale(["fr-FR", "zh-HK", "en-US"], "en")).toBe("zh-TW");
    expect(negotiateLocale(["de", "en-AU", "zh-CN"], "zh-CN")).toBe("en");
    expect(negotiateLocale(["zh-Hans-SG"], "en")).toBe("zh-CN");
  });

  it("is the platform's fallback when the browser asks for none of them", () => {
    expect(negotiateLocale(["fr-FR", "ja"], "en")).toBe("en");
    expect(negotiateLocale(["fr-FR", "ja"], "zh-TW")).toBe("zh-TW");
    expect(negotiateLocale([], "zh-CN")).toBe("zh-CN");
  });

  it("starts a page as the user chose, else negotiated afresh", () => {
    expect(restoredLocale({ locale: "en", localeChosen: true }, ["zh-CN"])).toBe("en");
    expect(restoredLocale({ locale: "en", localeChosen: false, fallbackLocale: "zh-TW" }, ["fr"])).toBe("zh-TW");
    expect(restoredLocale({ locale: "zh-TW", localeChosen: false }, ["zh-CN", "en"])).toBe("zh-CN");
    expect(restoredLocale({}, ["ko"])).toBe("en");
  });

  it("lists each language with its own and its English name", () => {
    expect(LANGUAGES.map((l) => `${l.locale} ${l.name} ${l.english}`)).toEqual([
      "zh-CN 简体中文 Simplified Chinese",
      "zh-TW 繁體中文 Traditional Chinese",
      "en English English",
    ]);
  });
});
