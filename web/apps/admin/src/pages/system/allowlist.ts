// The access restriction's list as the settings page checks it before
// saving (N1): whether the caller's own address is in it, so that the page
// warns before the server refuses (ADMIN_ACCESS_SELF_LOCKOUT). The server
// normalizes and decides; this only reads.

type Addr = { v6: boolean; n: bigint };

/** addr reads an IPv4 or IPv6 address (an IPv4-mapped one as IPv4); null for anything else. */
export function addr(s: string): Addr | null {
  s = s.trim();
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(s)) {
    const parts = s.split(".").map(Number);
    if (parts.some((p) => p > 255)) return null;
    return { v6: false, n: parts.reduce((acc, p) => (acc << 8n) | BigInt(p), 0n) };
  }
  if (!s.includes(":") || s.includes("%")) return null;
  const mapped = /^::ffff:(\d{1,3}(?:\.\d{1,3}){3})$/i.exec(s);
  if (mapped?.[1]) return addr(mapped[1]);
  const halves = s.split("::");
  if (halves.length > 2) return null;
  const groups = (h: string | undefined) => (h ? h.split(":") : []);
  const head = groups(halves[0]);
  const tail = halves.length === 2 ? groups(halves[1]) : [];
  const fill = 8 - head.length - tail.length;
  if ((halves.length === 2 && fill < 1) || (halves.length === 1 && head.length !== 8)) return null;
  const all = [...head, ...Array<string>(halves.length === 2 ? fill : 0).fill("0"), ...tail];
  if (all.some((g) => !/^[0-9a-f]{1,4}$/i.test(g))) return null;
  return { v6: true, n: all.reduce((acc, g) => (acc << 16n) | BigInt(parseInt(g, 16)), 0n) };
}

/** covers reports whether an entry of the list (an address or a CIDR prefix) holds ip. */
export function covers(entry: string, ip: string): boolean {
  const [base, bitsText] = entry.trim().split("/");
  const net = addr(base ?? "");
  const host = addr(ip);
  if (!net || !host || net.v6 !== host.v6) return false;
  const width = net.v6 ? 128 : 32;
  const bits = bitsText === undefined ? width : Number(bitsText);
  if (!Number.isInteger(bits) || bits < 1 || bits > width) return false;
  const shift = BigInt(width - bits);
  return net.n >> shift === host.n >> shift;
}

/** entries are the list's lines, trimmed, without the empty ones. */
export const entries = (text: string) =>
  text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean);

/** holds reports whether any entry holds ip. */
export const holds = (list: string[], ip: string) => list.some((e) => covers(e, ip));
