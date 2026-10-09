import type { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeAll, describe, expect, it } from "vitest";
import { initI18n } from "../i18n/index";
import { needsFallbackLocale, useSettings } from "../settings/store";
import { awaitFallbackLocale } from "./hooks";

// A first visit in a language the site lacks waits for the platform's
// fallback language before its first screen; everyone else starts at once (F33).

const prefer = (tags: string[]) => Object.defineProperty(globalThis.navigator, "languages", { value: tags, configurable: true });
const client = (answer: Promise<unknown>) => ({ fetchQuery: () => answer }) as unknown as QueryClient;

describe("the first screen's language", () => {
  beforeAll(() => {
    initI18n({ "zh-TW": {} });
  });
  afterEach(() => {
    prefer(["en-US"]);
    useSettings.setState({ locale: "zh-CN", localeChosen: false, fallbackLocale: null });
  });

  it("waits only for a first visit whose browser asks for none of the site's languages", () => {
    prefer(["ja", "ko"]);
    useSettings.setState({ localeChosen: false, fallbackLocale: null });
    expect(needsFallbackLocale()).toBe(true);
    useSettings.setState({ fallbackLocale: "zh-CN" });
    expect(needsFallbackLocale()).toBe(false);
    useSettings.setState({ fallbackLocale: null, localeChosen: true });
    expect(needsFallbackLocale()).toBe(false);
    prefer(["ja", "zh-HK"]);
    useSettings.setState({ localeChosen: false });
    expect(needsFallbackLocale()).toBe(false);
  });

  it("starts in the platform's fallback language once read", async () => {
    prefer(["ja"]);
    useSettings.setState({ locale: "en", localeChosen: false, fallbackLocale: null });
    await awaitFallbackLocale(client(Promise.resolve({ default_locale: "zh-TW" })));
    expect(useSettings.getState()).toMatchObject({ locale: "zh-TW", fallbackLocale: "zh-TW" });
  });

  it("does not wait past its limit for a profile that does not come", async () => {
    prefer(["ja"]);
    useSettings.setState({ locale: "en", localeChosen: false, fallbackLocale: null });
    const started = Date.now();
    await awaitFallbackLocale(client(new Promise(() => {})), 20);
    expect(Date.now() - started).toBeLessThan(1000);
    expect(useSettings.getState().locale).toBe("en");
  });
});
