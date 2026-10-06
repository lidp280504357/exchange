import { afterEach, describe, expect, it } from "vitest";
import { coinName, coinProfile } from "../coins";
import { apiProfile, rememberAssets, rememberPairs, useApiProfiles } from "./profiles";

describe("asset profiles from the API", () => {
  afterEach(() => useApiProfiles.setState({ profiles: {} }));

  it("lay display names, introductions and logos over the static profiles", () => {
    rememberPairs([{ base_asset: "ASTRA", base_display_name: "Astra Prime", base_logo_url: "/v1/market/assets/ASTRA/logo?v=2" }]);
    rememberAssets([{ asset_code: "BTC", display_name: null, description: { en: "Digital gold" }, links: {}, logo_url: null }]);
    expect(coinName("ASTRA", "zh-CN")).toBe("Astra Prime");
    expect(coinProfile("ASTRA")?.logo).toBe("/v1/market/assets/ASTRA/logo?v=2");
    // BTC keeps its static name and Chinese text, takes the English one.
    expect(coinProfile("BTC")?.name.en).toBe("Bitcoin");
    expect(coinProfile("BTC")?.intro.en).toBe("Digital gold");
    expect(coinProfile("BTC")?.intro["zh-CN"]).not.toBe("");
  });

  it("change the store only when something changed", () => {
    rememberPairs([{ base_asset: "ASTRA", base_display_name: "Astra", base_logo_url: "/logo?v=1" }]);
    const before = useApiProfiles.getState();
    rememberPairs([{ base_asset: "ASTRA", base_display_name: "Astra", base_logo_url: "/logo?v=1" }]);
    expect(useApiProfiles.getState()).toBe(before);
    // A removed logo goes.
    rememberPairs([{ base_asset: "ASTRA", base_display_name: "Astra", base_logo_url: null }]);
    expect(apiProfile("ASTRA")?.logo).toBeUndefined();
  });

  it("leave coins nobody touched to the static profiles", () => {
    rememberPairs([{ base_asset: "ETH", base_display_name: null, base_logo_url: null }]);
    expect(coinProfile("ETH")?.logo).toBeUndefined();
    expect(coinName("ETH", "en")).toBe("Ethereum");
  });

  it("name coins in Traditional Chinese, generated from the Simplified", () => {
    expect(coinName("BTC", "zh-CN")).toBe("比特币");
    expect(coinName("BTC", "zh-TW")).toBe("比特幣");
    expect(coinName("1000PEPE", "zh-TW")).toBe(coinName("PEPE", "zh-TW"));
    expect(coinProfile("ETH")?.intro["zh-TW"]).toMatch(/以太坊/);
  });

  it("show operators' Simplified introduction where they wrote no Traditional one", () => {
    rememberAssets([{ asset_code: "BTC", display_name: "Bitcoin", description: { "zh-CN": "数字黄金" }, links: {}, logo_url: null }]);
    expect(coinProfile("BTC")?.intro["zh-TW"]).toBe("数字黄金");
    expect(coinName("BTC", "zh-TW")).toBe("Bitcoin");
    rememberAssets([{ asset_code: "BTC", display_name: null, description: { "zh-CN": "数字黄金", "zh-TW": "數位黃金" }, links: {}, logo_url: null }]);
    expect(coinProfile("BTC")?.intro["zh-TW"]).toBe("數位黃金");
    expect(coinProfile("BTC")?.intro["zh-CN"]).toBe("数字黄金");
    // Only English from operators: the bundled Chinese stays.
    rememberAssets([{ asset_code: "ETH", display_name: null, description: { en: "World computer" }, links: {}, logo_url: null }]);
    expect(coinProfile("ETH")?.intro["zh-TW"]).toMatch(/以太坊/);
  });
});
