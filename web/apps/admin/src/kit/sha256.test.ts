import { describe, expect, it } from "vitest";
import { Sha256, sha256File } from "./sha256";

/** web is the platform's own digest, all at once. */
const web = async (b: Uint8Array) =>
  Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", new Uint8Array(b))), (x) => x.toString(16).padStart(2, "0")).join("");

describe("Sha256", () => {
  it("gives the standard's digests", () => {
    expect(new Sha256().hex()).toBe("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855");
    expect(new Sha256().update(new TextEncoder().encode("abc")).hex()).toBe("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
    const a = new Uint8Array(1_000_000).fill(0x61);
    expect(new Sha256().update(a).hex()).toBe("cdc76e5c9914fb9281a1c7e284d73e67f1809a48a497200e046d39ccc7112cd0");
  });

  it("is the same fed in pieces of any size", async () => {
    const data = new Uint8Array(200_003);
    let x = 7;
    for (let i = 0; i < data.length; i++) data[i] = (x = (x * 1103515245 + 12345) >>> 0) >>> 24;
    for (const piece of [1, 55, 56, 63, 64, 65, 1000, 65_536]) {
      const h = new Sha256();
      for (let at = 0; at < data.length; at += piece) h.update(data.subarray(at, at + piece));
      expect(h.hex()).toBe(await web(data));
    }
    for (const n of [0, 55, 56, 57, 63, 64, 119, 120, 128]) expect(new Sha256().update(data.subarray(0, n)).hex()).toBe(await web(data.subarray(0, n)));
  });

  it("hashes a file slice by slice, telling progress", async () => {
    const data = new Uint8Array(100_000).map((_, i) => i & 0xff);
    const seen: number[] = [];
    expect(await sha256File(new Blob([data]), (p) => seen.push(p), 30_000)).toBe(await web(data));
    expect(seen).toEqual([0.3, 0.6, 0.9, 1]);
  });
});
