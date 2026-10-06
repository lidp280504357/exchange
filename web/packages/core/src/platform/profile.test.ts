import { describe, expect, it } from "vitest";
import { heroFrom, heroHref } from "../content/hero";
import { brandForeground, creditsText, DEFAULT_PROFILE, registrationOpen, textOf } from "./profile";

describe("platform profile helpers (design 2026-10-04 §4.1)", () => {
  it("renders the welcome credits, or nothing", () => {
    const list = [
      { asset: "USDT", amount: "10000" },
      { asset: "BTC", amount: "0.1" },
      { asset: "ETH", amount: "2" },
    ];
    expect(creditsText(list, "zh-CN")).toBe("10,000 USDT、0.1 BTC、2 ETH");
    expect(creditsText(list, "en")).toBe("10,000 USDT, 0.1 BTC and 2 ETH");
    expect(creditsText(list.slice(0, 1), "en")).toBe("10,000 USDT");
    expect(creditsText([], "zh-CN")).toBe("");
    expect(creditsText(undefined, "en")).toBe("");
  });

  it("picks a text by language, Chinese when English is empty", () => {
    expect(textOf({ "zh-CN": "中文", en: "English" }, "en")).toBe("English");
    expect(textOf({ "zh-CN": "中文", en: "" }, "en")).toBe("中文");
    expect(textOf(undefined, "zh-CN")).toBe("");
    // Traditional Chinese falls back to the Simplified, left empty or absent.
    expect(textOf({ "zh-CN": "简体", "zh-TW": "繁體", en: "" }, "zh-TW")).toBe("繁體");
    expect(textOf({ "zh-CN": "简体", "zh-TW": "", en: "" }, "zh-TW")).toBe("简体");
    expect(textOf({ "zh-CN": "简体", en: "" }, "zh-TW")).toBe("简体");
  });

  it("puts dark text on a light brand and white on a dark one", () => {
    expect(brandForeground("#f0b90b")).toBe("#0b0e11");
    expect(brandForeground("#1e40af")).toBe("#ffffff");
    expect(brandForeground("nonsense")).toBe("#0b0e11");
  });

  it("keeps sign-ups open unless the profile closes them", () => {
    expect(registrationOpen(DEFAULT_PROFILE)).toBe(true);
    expect(registrationOpen({ registration: { status: "CLOSED", closed_text: { "zh-CN": "", en: "" } } })).toBe(false);
  });

  it("reads the hero's button from the body's first link, site paths and https only", () => {
    expect(heroFrom(" Title ", "Sub", "[Sign up](/register) and [more](/help)")).toEqual({
      title: "Title",
      subtitle: "Sub",
      cta: { text: "Sign up", href: "/register" },
    });
    expect(heroFrom("T", "", "no link").cta).toBeNull();
    expect(heroFrom("T", "", "[x](javascript:alert(1))").cta).toBeNull();
    expect(heroHref("https://example.com/promo")).toBe("https://example.com/promo");
    expect(heroHref("//evil.example")).toBeNull();
    expect(heroHref("http://example.com")).toBeNull();
  });
});
