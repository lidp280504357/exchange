import "../test/setup";
import { rememberPairs, useApiProfiles, useCoinLogo } from "@exchange/core";
import { act, render, renderHook } from "@testing-library/react";
import { Profiler } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { CoinIcon } from "./CoinIcon";

describe("CoinIcon", () => {
  afterEach(() => useApiProfiles.setState({ profiles: {} }));

  it("is the coin's letter while no logo is known", () => {
    const { container } = render(<CoinIcon symbol="1000pepe" />);
    expect(container.textContent).toBe("P");
    expect(container.querySelector("img")).toBeNull();
  });

  it("takes the uploaded logo of the code as given, then of its base", () => {
    const { result } = renderHook(() => useCoinLogo("1000BONK", "BONK"));
    expect(result.current).toBeUndefined();
    act(() => rememberPairs([{ base_asset: "BONK", base_logo_url: "/v1/market/assets/BONK/logo?v=1" }]));
    expect(result.current).toBe("/v1/market/assets/BONK/logo?v=1");
    act(() => rememberPairs([{ base_asset: "1000BONK", base_logo_url: "/v1/market/assets/1000BONK/logo?v=1" }]));
    expect(result.current).toBe("/v1/market/assets/1000BONK/logo?v=1");
  });

  it("re-renders only when its own coin's logo changes", () => {
    let renders = 0;
    render(
      <Profiler id="btc" onRender={() => renders++}>
        <CoinIcon symbol="BTC" />
      </Profiler>,
    );
    const first = renders;
    act(() => rememberPairs([{ base_asset: "ASTRA", base_logo_url: "/v1/market/assets/ASTRA/logo?v=2" }]));
    expect(renders).toBe(first);
    act(() => rememberPairs([{ base_asset: "BTC", base_logo_url: "/v1/market/assets/BTC/logo?v=1" }]));
    expect(renders).toBeGreaterThan(first);
  });
});
