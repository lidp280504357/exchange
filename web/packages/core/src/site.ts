// The three sites (ADR-0012): astras.vip (PC), m.astras.vip (mobile) and
// admin.astras.vip (console). nginx sends phones that open the PC site to
// the mobile site and desktops that open the mobile site back, unless the
// site_pref cookie says otherwise; the footer's switch sets it.

export type Site = "pc" | "m" | "admin";

const HOSTS: Record<Site, string> = { pc: "astras.vip", m: "m.astras.vip", admin: "admin.astras.vip" };

/** Development servers (design §4.2): 5173 PC, 5174 mobile, 5180 console. */
const DEV_PORTS: Record<Site, string> = { pc: "5173", m: "5174", admin: "5180" };

/** siteURL returns the origin of a site next to the current one. */
export function siteURL(site: Site): string {
  const loc = globalThis.location;
  if (loc && (loc.hostname === "localhost" || loc.hostname === "127.0.0.1")) {
    return `${loc.protocol}//${loc.hostname}:${DEV_PORTS[site]}`;
  }
  return `https://${HOSTS[site]}`;
}

/**
 * switchSite remembers the choice (site_pref, a year, for every
 * astras.vip host) and opens the same path on the other site.
 */
export function switchSite(to: "pc" | "m"): void {
  const loc = globalThis.location;
  if (!loc) return;
  const domain = loc.hostname.endsWith("astras.vip") ? "; domain=.astras.vip" : "";
  document.cookie = `site_pref=${to}; path=/; max-age=31536000; samesite=lax${domain}`;
  loc.assign(siteURL(to) + loc.pathname + loc.search);
}
