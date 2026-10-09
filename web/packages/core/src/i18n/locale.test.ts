import { afterEach, beforeAll, describe, expect, it } from "vitest";
import { useSettings } from "../settings/store";
import { followFallbackLocale, i18n, initI18n, setLocale } from "./index";

// The language a visitor has not chosen follows the browser, then the
// platform's fallback language; one they chose stays (F30).

const prefer = (tags: string[]) => Object.defineProperty(globalThis.navigator, "languages", { value: tags, configurable: true });

describe("the fallback language", () => {
  beforeAll(() => {
    initI18n({ "zh-TW": {} });
  });
  afterEach(() => {
    prefer(["en-US"]);
    useSettings.setState({ locale: "zh-CN", localeChosen: false, fallbackLocale: "en" });
  });

  it("decides for a browser that asks for none of the site's languages, until the user chooses", () => {
    prefer(["fr-FR", "de"]);
    useSettings.setState({ locale: "en", localeChosen: false, fallbackLocale: "en" });
    followFallbackLocale("zh-TW");
    expect(useSettings.getState()).toMatchObject({ locale: "zh-TW", fallbackLocale: "zh-TW", localeChosen: false });
    expect(i18n.language).toBe("zh-TW");
    setLocale("en");
    followFallbackLocale("zh-CN");
    expect(useSettings.getState()).toMatchObject({ locale: "en", fallbackLocale: "zh-CN", localeChosen: true });
  });

  it("leaves a browser's own language alone", () => {
    prefer(["ja", "zh-HK"]);
    useSettings.setState({ locale: "zh-TW", localeChosen: false, fallbackLocale: "en" });
    followFallbackLocale("zh-CN");
    expect(useSettings.getState().locale).toBe("zh-TW");
  });
});
