// SHA-256 fed piece by piece (FIPS 180-4): the browser's crypto.subtle
// digests a whole buffer at once, which for an app of 500 MiB means holding
// it all in memory (review GF, A76 ②). Reads the file in slices instead.

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
  0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

const rotr = (x: number, n: number) => (x >>> n) | (x << (32 - n));

/** Sha256 hashes what update is given, in order; hex gives the digest once. */
export class Sha256 {
  private h = new Uint32Array([0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19]);
  private w = new Uint32Array(64);
  private buf = new Uint8Array(64);
  private bufLen = 0;
  private bytes = 0;

  /** update adds data to what is hashed. */
  update(data: Uint8Array): this {
    this.bytes += data.length;
    let i = 0;
    if (this.bufLen > 0) {
      const n = Math.min(64 - this.bufLen, data.length);
      this.buf.set(data.subarray(0, n), this.bufLen);
      this.bufLen += n;
      i = n;
      if (this.bufLen < 64) return this;
      this.block(this.buf, 0);
      this.bufLen = 0;
    }
    for (; i + 64 <= data.length; i += 64) this.block(data, i);
    if (i < data.length) {
      this.buf.set(data.subarray(i), 0);
      this.bufLen = data.length - i;
    }
    return this;
  }

  /** hex is the digest in lowercase hex; the hash takes nothing more after it. */
  hex(): string {
    const bits = this.bytes * 8;
    const pad = new Uint8Array((this.bufLen < 56 ? 56 : 120) - this.bufLen + 8);
    pad[0] = 0x80;
    const view = new DataView(pad.buffer);
    view.setUint32(pad.length - 8, Math.floor(bits / 0x100000000));
    view.setUint32(pad.length - 4, bits >>> 0);
    this.update(pad);
    return Array.from(this.h, (x) => x.toString(16).padStart(8, "0")).join("");
  }

  private block(d: Uint8Array, o: number) {
    const w = this.w;
    for (let t = 0; t < 16; t++) w[t] = (d[o + 4 * t]! << 24) | (d[o + 4 * t + 1]! << 16) | (d[o + 4 * t + 2]! << 8) | d[o + 4 * t + 3]!;
    for (let t = 16; t < 64; t++) {
      const s0 = rotr(w[t - 15]!, 7) ^ rotr(w[t - 15]!, 18) ^ (w[t - 15]! >>> 3);
      const s1 = rotr(w[t - 2]!, 17) ^ rotr(w[t - 2]!, 19) ^ (w[t - 2]! >>> 10);
      w[t] = (w[t - 16]! + s0 + w[t - 7]! + s1) | 0;
    }
    let [a, b, c, e, f, g, hh, dd] = [this.h[0]!, this.h[1]!, this.h[2]!, this.h[4]!, this.h[5]!, this.h[6]!, this.h[7]!, this.h[3]!];
    for (let t = 0; t < 64; t++) {
      const t1 = (hh + (rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)) + ((e & f) ^ (~e & g)) + K[t]! + w[t]!) | 0;
      const t2 = ((rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)) + ((a & b) ^ (a & c) ^ (b & c))) | 0;
      hh = g;
      g = f;
      f = e;
      e = (dd + t1) | 0;
      dd = c;
      c = b;
      b = a;
      a = (t1 + t2) | 0;
    }
    const h = this.h;
    h[0] = h[0]! + a;
    h[1] = h[1]! + b;
    h[2] = h[2]! + c;
    h[3] = h[3]! + dd;
    h[4] = h[4]! + e;
    h[5] = h[5]! + f;
    h[6] = h[6]! + g;
    h[7] = h[7]! + hh;
  }
}

/** sha256File hashes a file slice by slice, telling progress (0–1) after each. */
export async function sha256File(f: Blob, onProgress?: (done: number) => void, slice = 8 << 20): Promise<string> {
  const h = new Sha256();
  for (let at = 0; at < f.size; at += slice) {
    h.update(new Uint8Array(await f.slice(at, at + slice).arrayBuffer()));
    onProgress?.(Math.min(1, (at + slice) / f.size));
  }
  return h.hex();
}
