import { create } from "zustand";
import type { Locale } from "../settings/store";

// Asset profiles from the API (ASTRA design §5.3): the display names,
// introductions, links and logos operators set. The pairs and assets
// queries fill this store; coinProfile (../coins) lays it over the
// repository's static profiles, so every name, introduction and icon on
// the sites follows a change within the queries' minute.

/** ApiProfile is what the API says of an asset; unset parts fall back to the static profile. */
export type ApiProfile = {
  name?: string;
  intro?: Partial<Record<Locale, string>>;
  links?: Record<string, string>;
  logo?: string;
};

type State = { profiles: Record<string, ApiProfile> };

/** useApiProfiles is the store; read a field with a selector so that changes re-render. */
export const useApiProfiles = create<State>(() => ({ profiles: {} }));

/** The fields of a pair that carry its base asset's profile. */
export type PairProfileFields = { base_asset: string; base_display_name?: string | null; base_logo_url?: string | null };

/** The fields of an asset that carry its profile. */
export type AssetProfileFields = {
  asset_code: string;
  display_name?: string | null;
  description?: Record<string, string>;
  links?: Record<string, string>;
  logo_url?: string | null;
};

function merge(updates: Record<string, ApiProfile>): void {
  const cur = useApiProfiles.getState().profiles;
  let changed = false;
  const next = { ...cur };
  for (const [code, u] of Object.entries(updates)) {
    const merged = { ...cur[code], ...u };
    if (JSON.stringify(merged) !== JSON.stringify(cur[code])) {
      next[code] = merged;
      changed = true;
    }
  }
  if (changed) useApiProfiles.setState({ profiles: next });
}

/** rememberPairs records the base assets' display names and logos from a pairs response. */
export function rememberPairs(pairs: readonly PairProfileFields[]): void {
  const updates: Record<string, ApiProfile> = {};
  for (const p of pairs) {
    updates[p.base_asset] = { name: p.base_display_name || undefined, logo: p.base_logo_url || undefined };
  }
  merge(updates);
}

/** rememberAssets records the profiles from an assets response. */
export function rememberAssets(assets: readonly AssetProfileFields[]): void {
  const updates: Record<string, ApiProfile> = {};
  for (const a of assets) {
    const intro: Partial<Record<Locale, string>> = {};
    for (const locale of ["zh-CN", "en"] as const) {
      const text = a.description?.[locale];
      if (text) intro[locale] = text;
    }
    updates[a.asset_code] = {
      name: a.display_name || undefined,
      intro: Object.keys(intro).length > 0 ? intro : undefined,
      links: a.links && Object.keys(a.links).length > 0 ? a.links : undefined,
      logo: a.logo_url || undefined,
    };
  }
  merge(updates);
}

/** apiProfile returns what the API says of an asset, if anything. */
export function apiProfile(code: string): ApiProfile | undefined {
  return useApiProfiles.getState().profiles[code];
}
