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
});
