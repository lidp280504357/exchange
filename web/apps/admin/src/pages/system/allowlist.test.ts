import { describe, expect, it } from "vitest";
import { addr, covers, entries, holds } from "./allowlist";

// The settings page's check of the access list (N1): whether the caller's
// address is in it, as admin-service's domain.ConsoleAccess.Allows decides.
describe("the access list", () => {
  it("reads IPv4 and IPv6 addresses, an IPv4-mapped one as IPv4", () => {
    expect(addr("203.0.113.7")).toEqual({ v6: false, n: 0xcb007107n });
    expect(addr("2001:db8::1")?.n).toBe(0x20010db8000000000000000000000001n);
    expect(addr("::ffff:203.0.113.7")).toEqual(addr("203.0.113.7"));
    for (const bad of ["203.0.113", "256.0.0.1", "2001:db8::1::2", "fe80::1%eth0", "example.com", "1:2:3:4:5:6:7"]) {
      expect(addr(bad)).toBeNull();
    }
  });

  it("holds an address in an entry's network, of the same family", () => {
    expect(covers("203.0.113.0/24", "203.0.113.250")).toBe(true);
    expect(covers("203.0.113.7", "203.0.113.7")).toBe(true);
    expect(covers("203.0.113.7", "203.0.113.8")).toBe(false);
    expect(covers("2001:db8:1:2::/64", "2001:db8:1:2:aaaa:bbbb:cccc:dddd")).toBe(true);
    expect(covers("2001:db8:1:2::/64", "2001:db8:1:3::1")).toBe(false);
    expect(covers("203.0.113.0/24", "2001:db8::1")).toBe(false);
    expect(covers("0.0.0.0/0", "203.0.113.7")).toBe(false); // no /0, as the server
  });

  it("splits the list by lines, spaces or commas", () => {
    const list = entries(" 203.0.113.7\n\n2001:db8:1:2::/64, 198.51.100.0/24 ");
    expect(list).toEqual(["203.0.113.7", "2001:db8:1:2::/64", "198.51.100.0/24"]);
    expect(holds(list, "198.51.100.9")).toBe(true);
    expect(holds(list, "192.0.2.1")).toBe(false);
  });
});
