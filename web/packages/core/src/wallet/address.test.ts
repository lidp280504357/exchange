import { describe, expect, it } from "vitest";
import { bitcoinNetwork, checkAddress, decodeSegwit } from "./address";

const evm = { format: "EVM" };
const tron = { format: "TRON" };
const btc = { format: "BTC", chain: "bitcoin" };
const btcTest = { format: "BTC", chain: "bitcoin-testnet" };

describe("EVM addresses", () => {
  it("accepts 0x and 40 hex digits in one case", () => {
    expect(checkAddress("0x52908400098527886E0F7030069857D2E4169EE7", evm)).toEqual({ ok: true, reason: null, unverified: false });
    expect(checkAddress("0x52908400098527886e0f7030069857d2e4169ee7", evm)).toEqual({ ok: true, reason: null, unverified: false });
    expect(checkAddress("  0x52908400098527886E0F7030069857D2E4169EE7 \n", evm).ok).toBe(true);
  });

  it("leaves a mixed-case (EIP-55) checksum to the server", () => {
    expect(checkAddress("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", evm)).toEqual({ ok: true, reason: null, unverified: true });
  });

  it("refuses other forms", () => {
    for (const bad of [
      "",
      "0x52908400098527886E0F7030069857D2E4169EE", // 39 digits
      "0x52908400098527886E0F7030069857D2E4169EE77", // 41 digits
      "52908400098527886E0F7030069857D2E4169EE7", // no 0x
      "0X52908400098527886E0F7030069857D2E4169EE7", // upper-case X
      "0xG2908400098527886E0F7030069857D2E4169EE7", // not hex
      "0x5290840009852788 6E0F7030069857D2E4169EE7", // a space inside
    ]) {
      expect(checkAddress(bad, evm), bad).toEqual({ ok: false, reason: "ADDRESS_FORMAT", unverified: false });
    }
  });

  it("treats an unknown format as EVM, like the server", () => {
    expect(checkAddress("0x52908400098527886E0F7030069857D2E4169EE7", { format: "" }).ok).toBe(true);
  });
});

describe("TRON addresses", () => {
  it("accepts T and 33 Base58 characters, the checksum left to the server", () => {
    expect(checkAddress("TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", tron)).toEqual({ ok: true, reason: null, unverified: true });
  });

  it("refuses other forms", () => {
    for (const bad of [
      "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6", // 33 characters
      "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6tt", // 35 characters
      "AR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", // not T
      "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj60", // 0 is not Base58
      "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLjOt", // nor is O
      "0x52908400098527886E0F7030069857D2E4169EE7",
    ]) {
      expect(checkAddress(bad, tron).reason, bad).toBe("ADDRESS_FORMAT");
    }
  });
});

describe("Bitcoin addresses", () => {
  it("accepts bech32 and bech32m addresses with a valid checksum", () => {
    expect(checkAddress("bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq", btc)).toEqual({ ok: true, reason: null, unverified: false });
    // BIP-173/350 vectors: upper case, and a taproot (v1, bech32m) address.
    expect(checkAddress("BC1QW508D6QEJXTDG4Y5R3ZARVARY0C5XW7KV8F3T4", btc).ok).toBe(true);
    expect(checkAddress("bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vqzk5jj0", btc).ok).toBe(true);
    expect(checkAddress("tb1qrp33g0q5c5txsp9arysrx4k6zdkfs4nce4xj0gdcccefvpysxf3q0sl5k7", btcTest).ok).toBe(true);
  });

  it("finds a typo through the checksum", () => {
    expect(checkAddress("bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdr", btc).reason).toBe("ADDRESS_CHECKSUM");
    expect(checkAddress("bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mqd", btc).reason).toBe("ADDRESS_CHECKSUM");
    // A witness v1 program with a bech32 (not bech32m) checksum (BIP-350).
    expect(checkAddress("bc1pw508d6qejxtdg4y5r3zarvary0c5xw7kw508d6qejxtdg4y5r3zarvary0c5xw7k7grplx", btc).reason).toBe("ADDRESS_CHECKSUM");
  });

  it("refuses an address of another network", () => {
    expect(checkAddress("tb1qrp33g0q5c5txsp9arysrx4k6zdkfs4nce4xj0gdcccefvpysxf3q0sl5k7", btc).reason).toBe("ADDRESS_NETWORK");
    expect(checkAddress("bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq", btcTest).reason).toBe("ADDRESS_NETWORK");
    expect(checkAddress("mipcBbFg9gMiCh81Kj8tqqdgoZub1ZJRfn", btc).reason).toBe("ADDRESS_NETWORK");
    expect(checkAddress("1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", btcTest).reason).toBe("ADDRESS_NETWORK");
  });

  it("accepts Base58 addresses of the network, the checksum left to the server", () => {
    expect(checkAddress("1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", btc)).toEqual({ ok: true, reason: null, unverified: true });
    expect(checkAddress("3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy", btc).ok).toBe(true);
    expect(checkAddress("mipcBbFg9gMiCh81Kj8tqqdgoZub1ZJRfn", btcTest).ok).toBe(true);
    expect(checkAddress("2MzQwSSnBHWHqSAqtTVQ6v47XtaisrJa1Vc", btcTest).ok).toBe(true);
  });

  it("refuses malformed strings", () => {
    for (const bad of [
      "",
      "bc1QAR0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq", // mixed case
      "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdb", // b is not in the bech32 charset
      "bc1qqqqq", // too short for a checksum
      "1BvBMSEY", // too short
      "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN0", // 0 is not Base58
      "7BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", // no network starts with 7
      "0x52908400098527886E0F7030069857D2E4169EE7",
    ]) {
      expect(checkAddress(bad, btc).reason, bad).toBe("ADDRESS_FORMAT");
    }
  });

  it("decodes the witness version and program", () => {
    const d = decodeSegwit("bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq");
    expect("reason" in d).toBe(false);
    if (!("reason" in d)) {
      expect(d.hrp).toBe("bc");
      expect(d.version).toBe(0);
      expect(d.program).toHaveLength(20);
    }
    const t = decodeSegwit("bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vqzk5jj0");
    expect("reason" in t ? t.reason : t.version).toBe(1);
  });

  it("reads the network from the chain name", () => {
    expect(bitcoinNetwork("bitcoin")).toBe("bc");
    expect(bitcoinNetwork("")).toBe("bc");
    expect(bitcoinNetwork("bitcoin-testnet")).toBe("tb");
    expect(bitcoinNetwork("bitcoin-signet")).toBe("tb");
    expect(bitcoinNetwork("bitcoin-regtest")).toBe("bcrt");
  });
});

describe("memos", () => {
  it("asks for a memo where the network needs one, after the address itself", () => {
    const rules = { format: "EVM", memoRequired: true };
    expect(checkAddress("0x52908400098527886E0F7030069857D2E4169EE7", rules).reason).toBe("MEMO_REQUIRED");
    expect(checkAddress("0x52908400098527886E0F7030069857D2E4169EE7", rules, " 12345 ").ok).toBe(true);
    expect(checkAddress("0x5290", rules).reason).toBe("ADDRESS_FORMAT");
  });
});
