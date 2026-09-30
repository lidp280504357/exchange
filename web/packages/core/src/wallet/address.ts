// Local address checks (design §6.2 withdraw step 3, §9.4): quick,
// synchronous checks of an address's form for its network, run while the
// user types so a typo shows at once. They follow the server's rules
// (internal/wallet/domain/address.go) where that is cheap:
//
// - EVM: 0x and 40 hex digits. A mixed-case address carries an EIP-55
//   checksum, which needs keccak: it is left to the server (unverified).
// - TRON: T and 33 Base58 characters; the Base58Check hash is the server's.
// - Bitcoin: bech32 (witness v0) or bech32m (v1+) with its checksum, which
//   is pure arithmetic, for the network's prefix (bc, tb, bcrt); or a
//   Base58 address whose first character fits the network (1/3 on mainnet,
//   m/n/2 on testnets), its checksum again the server's.
//
// POST /v1/wallet/withdraw-addresses/validate gives the authoritative
// answer; these checks only spare it the obvious mistakes.

/** How a network writes its addresses (WalletNetwork.address_format). */
export type AddressFormat = "EVM" | "TRON" | "BTC";

/** Why an address is refused: the validate endpoint's reason codes. */
export type AddressReason = "ADDRESS_FORMAT" | "ADDRESS_CHECKSUM" | "ADDRESS_NETWORK" | "MEMO_REQUIRED" | "ADDRESS_OWN";

export type AddressCheck = {
  /** The address passed the local checks (the server still decides). */
  ok: boolean;
  /** Why it failed; null when ok. */
  reason: AddressReason | null;
  /** It passed, but a checksum only the server verifies remains (mixed-case EVM, Base58). */
  unverified: boolean;
};

export type AddressRules = {
  /** EVM, TRON or BTC; anything else is checked as EVM, like the server does. */
  format: string;
  /** The network's chain (WalletNetwork.chain): picks the Bitcoin network. */
  chain?: string;
  memoRequired?: boolean;
};

const BASE58 = /^[1-9A-HJ-NP-Za-km-z]+$/;

const pass = (unverified = false): AddressCheck => ({ ok: true, reason: null, unverified });
const fail = (reason: AddressReason): AddressCheck => ({ ok: false, reason, unverified: false });

/**
 * checkAddress checks an address (and the memo, for networks that need
 * one) against its network's format. Surrounding spaces are ignored, as
 * the server ignores them.
 */
export function checkAddress(address: string, rules: AddressRules, memo = ""): AddressCheck {
  const a = address.trim();
  let out: AddressCheck;
  switch (rules.format) {
    case "TRON":
      out = checkTron(a);
      break;
    case "BTC":
      out = checkBitcoin(a, bitcoinNetwork(rules.chain ?? ""));
      break;
    default:
      out = checkEvm(a);
  }
  if (out.ok && rules.memoRequired && memo.trim() === "") return fail("MEMO_REQUIRED");
  return out;
}

function checkEvm(a: string): AddressCheck {
  if (!/^0x[0-9a-fA-F]{40}$/.test(a)) return fail("ADDRESS_FORMAT");
  const hex = a.slice(2);
  const mixed = /[a-f]/.test(hex) && /[A-F]/.test(hex);
  return pass(mixed);
}

function checkTron(a: string): AddressCheck {
  if (a.length !== 34 || !a.startsWith("T") || !BASE58.test(a)) return fail("ADDRESS_FORMAT");
  return pass(true);
}

/** The Bitcoin networks, by the human-readable part of their segwit addresses. */
export type BitcoinNetwork = "bc" | "tb" | "bcrt";

/** bitcoinNetwork reads the network from a chain name, as the server does: regtest, test/signet, else mainnet. */
export function bitcoinNetwork(chain: string): BitcoinNetwork {
  const c = chain.toLowerCase();
  if (c.includes("regtest")) return "bcrt";
  if (c.includes("test") || c.includes("signet")) return "tb";
  return "bc";
}

const SEGWIT_PREFIXES = ["bc1", "tb1", "bcrt1"];

// First characters of Base58 addresses: P2PKH and P2SH on mainnet (0x00,
// 0x05) and on the test networks (0x6f, 0xc4).
const BASE58_LEADS: Record<"main" | "test", string> = { main: "13", test: "mn2" };

function checkBitcoin(a: string, net: BitcoinNetwork): AddressCheck {
  const lower = a.toLowerCase();
  if (SEGWIT_PREFIXES.some((p) => lower.startsWith(p))) {
    const d = decodeSegwit(a);
    if ("reason" in d) return fail(d.reason);
    if (d.hrp !== net) return fail("ADDRESS_NETWORK");
    if (d.version === 0 && d.program.length !== 20 && d.program.length !== 32) return fail("ADDRESS_FORMAT");
    return pass();
  }
  if (a.length < 26 || a.length > 35 || !BASE58.test(a)) return fail("ADDRESS_FORMAT");
  const own = net === "bc" ? BASE58_LEADS.main : BASE58_LEADS.test;
  const other = net === "bc" ? BASE58_LEADS.test : BASE58_LEADS.main;
  const lead = a.charAt(0);
  if (own.includes(lead)) return pass(true);
  if (other.includes(lead)) return fail("ADDRESS_NETWORK");
  return fail("ADDRESS_FORMAT");
}

const BECH32_CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";
const BECH32_GENERATOR = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3];
/** Checksum constants of bech32 (witness v0, BIP-173) and bech32m (v1-16, BIP-350). */
const BECH32 = 1;
const BECH32M = 0x2bc830a3;

function polymod(values: number[]): number {
  let chk = 1;
  for (const v of values) {
    const top = chk >>> 25;
    chk = ((chk & 0x1ffffff) << 5) ^ v;
    for (let i = 0; i < 5; i++) if ((top >>> i) & 1) chk ^= BECH32_GENERATOR[i]!;
  }
  return chk >>> 0;
}

function hrpExpand(hrp: string): number[] {
  const out: number[] = [];
  for (let i = 0; i < hrp.length; i++) out.push(hrp.charCodeAt(i) >> 5);
  out.push(0);
  for (let i = 0; i < hrp.length; i++) out.push(hrp.charCodeAt(i) & 31);
  return out;
}

// convertBits regroups 5-bit words into bytes, refusing leftover bits.
function convertBits(data: number[], from: number, to: number): number[] | null {
  let acc = 0;
  let bits = 0;
  const maxv = (1 << to) - 1;
  const out: number[] = [];
  for (const v of data) {
    acc = (acc << from) | v;
    bits += from;
    while (bits >= to) {
      bits -= to;
      out.push((acc >> bits) & maxv);
    }
    acc &= (1 << bits) - 1; // keep only the bits not yet used
  }
  if (bits >= from || ((acc << (to - bits)) & maxv) !== 0) return null;
  return out;
}

export type Segwit = { hrp: string; version: number; program: number[] };

/**
 * decodeSegwit decodes a bech32/bech32m segwit address into its prefix,
 * witness version and program, or says why it cannot: a malformed string
 * (ADDRESS_FORMAT) or a checksum that does not match (ADDRESS_CHECKSUM).
 */
export function decodeSegwit(address: string): Segwit | { reason: "ADDRESS_FORMAT" | "ADDRESS_CHECKSUM" } {
  const a = address.trim();
  if (a.length > 90 || (a.toLowerCase() !== a && a.toUpperCase() !== a)) return { reason: "ADDRESS_FORMAT" };
  const lower = a.toLowerCase();
  const sep = lower.lastIndexOf("1");
  if (sep < 1 || sep + 7 > lower.length) return { reason: "ADDRESS_FORMAT" };
  const hrp = lower.slice(0, sep);
  const data: number[] = [];
  for (const c of lower.slice(sep + 1)) {
    const i = BECH32_CHARSET.indexOf(c);
    if (i < 0) return { reason: "ADDRESS_FORMAT" };
    data.push(i);
  }
  if (data.length < 7) return { reason: "ADDRESS_FORMAT" }; // a version and the 6-character checksum at least
  const version = data[0]!;
  if (version > 16) return { reason: "ADDRESS_FORMAT" };
  if (polymod([...hrpExpand(hrp), ...data]) !== (version === 0 ? BECH32 : BECH32M)) return { reason: "ADDRESS_CHECKSUM" };
  const program = convertBits(data.slice(1, -6), 5, 8);
  if (!program || program.length < 2 || program.length > 40) return { reason: "ADDRESS_FORMAT" };
  return { hrp, version, program };
}
