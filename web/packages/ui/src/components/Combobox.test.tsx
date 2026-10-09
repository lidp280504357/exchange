import "../test/setup";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Combobox, coinItems, filterItems, type ComboboxItem } from "./Combobox";

const items: ComboboxItem[] = [
  { value: "BTC", label: "BTC", description: "Bitcoin", keywords: ["比特币"] },
  { value: "BNB", label: "BNB", description: "BNB" },
  { value: "WBTC", label: "WBTC", description: "Wrapped Bitcoin" },
  { value: "ETH", label: "ETH", description: "Ethereum", keywords: ["以太坊"], disabled: true },
];

describe("filterItems", () => {
  it("ranks label prefixes first, then contains, then names and keywords", () => {
    expect(filterItems(items, "b").map((i) => i.value)).toEqual(["BTC", "BNB", "WBTC"]);
    expect(filterItems(items, "btc").map((i) => i.value)).toEqual(["BTC", "WBTC"]);
    expect(filterItems(items, "bitcoin").map((i) => i.value)).toEqual(["BTC", "WBTC"]);
    expect(filterItems(items, "以太").map((i) => i.value)).toEqual(["ETH"]);
    expect(filterItems(items, "  ").length).toBe(4);
    expect(filterItems(items, "doge")).toEqual([]);
  });

  it("builds coin items with names in both languages", () => {
    const [btc] = coinItems(["BTC"], "en");
    expect(btc?.description).toBe("Bitcoin");
    expect(btc?.keywords).toContain("比特币");
  });
});

describe("Combobox", () => {
  it("filters and picks with the keyboard, skipping disabled items", () => {
    const onChange = vi.fn();
    render(<Combobox items={items} value={null} onValueChange={onChange} open aria-label="Coin" />);
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "b" } });
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual(["BTCBitcoin", "BNBBNB", "WBTCWrapped Bitcoin"]);
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).toHaveBeenCalledWith("BNB", items[1]);
  });

  it("marks a label in another language with its lang, in the list and on the trigger", () => {
    const languages: ComboboxItem[] = [
      { value: "zh-TW", label: "繁體中文", lang: "zh-TW", description: "Traditional Chinese" },
      { value: "en", label: "English", lang: "en" },
    ];
    const { container } = render(<Combobox items={languages} value="zh-TW" onValueChange={() => {}} aria-label="Language" />);
    expect(container.querySelector('button [lang="zh-TW"]')?.textContent).toBe("繁體中文");
    fireEvent.click(screen.getByRole("button", { name: "Language" }));
    expect(screen.getAllByRole("option").map((o) => o.querySelector("[lang]")?.getAttribute("lang"))).toEqual(["zh-TW", "en"]);
  });
});
