import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo } from "react";
import { create } from "zustand";
import { unwrap, userApi } from "../api/client";
import { qk } from "../query/keys";
import { selectSignedIn, useSession } from "../session/store";

// Favourite markets (design §6.2 /markets): visitors keep them on the
// device; a signed-in user's live on the server (GET/PUT /v1/user/favorites).
// On sign-in the visitor's picks are merged into the account's list once
// and leave the device, so they never flow into another account signed in
// on the same browser later. While signed in, a toggle updates the cache
// at once and the PUTs go out in order.

/** The server keeps at most this many (user.yaml maxItems). */
export const FAVORITES_MAX = 100;

const SYMBOL = /^[A-Z0-9]{2,10}-[A-Z0-9]{2,10}(-PERP)?$/;

/**
 * normalizeFavorites does what the server does to a list: upper-case,
 * drop malformed symbols and repeats (keeping the first) and cap it.
 */
export function normalizeFavorites(list: readonly string[]): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const raw of list) {
    const s = String(raw).trim().toUpperCase();
    if (!SYMBOL.test(s) || seen.has(s)) continue;
    seen.add(s);
    out.push(s);
    if (out.length === FAVORITES_MAX) break;
  }
  return out;
}

/** mergeFavorites keeps the account's list first and appends the local picks it lacks. */
export function mergeFavorites(server: readonly string[], local: readonly string[]): string[] {
  return normalizeFavorites([...server, ...local]);
}

/** sameFavorites compares two lists, order included. */
export function sameFavorites(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((s, i) => s === b[i]);
}

/**
 * planSignInMerge decides the one merge after sign-in: the list to keep,
 * and whether the server needs it (nothing to send when the local picks
 * are already there).
 */
export function planSignInMerge(server: readonly string[], local: readonly string[]): { merged: string[]; put: boolean } {
  const base = normalizeFavorites(server);
  const merged = mergeFavorites(base, local);
  return { merged, put: !sameFavorites(merged, base) };
}

/** toggleFavorite adds a symbol at the end, or removes it. */
export function toggleFavorite(list: readonly string[], symbol: string): string[] {
  const s = symbol.trim().toUpperCase();
  return list.includes(s) ? list.filter((x) => x !== s) : normalizeFavorites([...list, s]);
}

/** The device's key for a visitor's favourites. */
export const FAVORITES_KEY = "exchange.favorites";

function storage(): Storage | null {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
}

/** readLocalFavorites reads a visitor's favourites; anything unreadable is an empty list. */
export function readLocalFavorites(store: Storage | null = storage()): string[] {
  try {
    const parsed: unknown = JSON.parse(store?.getItem(FAVORITES_KEY) ?? "[]");
    return Array.isArray(parsed) ? normalizeFavorites(parsed.filter((s): s is string => typeof s === "string")) : [];
  } catch {
    return [];
  }
}

/** writeLocalFavorites keeps a visitor's favourites (an empty list removes the key). */
export function writeLocalFavorites(list: readonly string[], store: Storage | null = storage()): void {
  try {
    if (list.length === 0) store?.removeItem(FAVORITES_KEY);
    else store?.setItem(FAVORITES_KEY, JSON.stringify(list));
  } catch {
    // storage full or blocked: the list lives for this page only
  }
}

type LocalFavorites = { symbols: string[]; set: (symbols: string[]) => void };

/** The visitor's favourites (mirrored to localStorage). */
export const useLocalFavorites = create<LocalFavorites>((set) => ({
  symbols: readLocalFavorites(),
  set: (symbols) => {
    writeLocalFavorites(symbols);
    set({ symbols });
  },
}));

// Other tabs change the list too: follow the storage event while any
// component shows favourites.
let storageReaders = 0;
function onStorage(e: StorageEvent) {
  if (e.key === FAVORITES_KEY || e.key === null) useLocalFavorites.setState({ symbols: readLocalFavorites() });
}
function followStorage(): () => void {
  if (typeof window === "undefined") return () => {};
  if (storageReaders++ === 0) window.addEventListener("storage", onStorage);
  return () => {
    if (--storageReaders === 0) window.removeEventListener("storage", onStorage);
  };
}

type Favorites = { symbols: string[]; updated_at: string | null };

// PUTs leave one at a time, in the order the toggles happened.
let queue: Promise<unknown> = Promise.resolve();

/**
 * saveFavorites writes a list to the cache at once and to the server in
 * order; the server's answer replaces the cache unless a newer list is
 * already there. A failure restores `before` (when still current) and
 * rejects.
 */
export function saveFavorites(qc: QueryClient, next: string[], before: string[]): Promise<void> {
  const current = () => qc.getQueryData<Favorites>(qk.favorites)?.symbols ?? [];
  qc.setQueryData<Favorites>(qk.favorites, (d) => ({ symbols: next, updated_at: d?.updated_at ?? null }));
  const run = queue.then(() => unwrap(userApi.PUT("/v1/user/favorites", { body: { symbols: next } })));
  queue = run.catch(() => undefined);
  return run.then(
    (stored) => {
      if (sameFavorites(current(), next)) qc.setQueryData<Favorites>(qk.favorites, stored);
    },
    (err: unknown) => {
      if (sameFavorites(current(), next)) qc.setQueryData<Favorites>(qk.favorites, (d) => ({ symbols: before, updated_at: d?.updated_at ?? null }));
      throw err;
    },
  );
}

const EMPTY: string[] = [];

export type FavoritesApi = {
  /** The favourite symbols in the user's order. */
  symbols: string[];
  has: (symbol: string) => boolean;
  /** Adds or removes a symbol; rejects when the server refused (the change is undone). */
  toggle: (symbol: string) => Promise<void>;
  /** False while a signed-in user's list is loading. */
  ready: boolean;
  error: unknown;
};

/**
 * useFavorites returns the favourites of the visitor (device) or of the
 * signed-in user (server), merging the first into the second once after
 * sign-in.
 */
export function useFavorites(): FavoritesApi {
  const signedIn = useSession(selectSignedIn);
  const local = useLocalFavorites((s) => s.symbols);
  const qc = useQueryClient();
  const server = useQuery({
    queryKey: qk.favorites,
    queryFn: () => unwrap(userApi.GET("/v1/user/favorites")),
    enabled: signedIn,
    staleTime: 5 * 60_000,
  });

  useEffect(() => followStorage(), []);

  // The one merge after sign-in: the device's picks move into the account.
  const data = server.data;
  useEffect(() => {
    if (!signedIn || !data) return;
    const pending = useLocalFavorites.getState().symbols;
    if (pending.length === 0) return;
    // Emptied first, so a second reader of the hook does not merge again.
    useLocalFavorites.getState().set([]);
    const plan = planSignInMerge(data.symbols, pending);
    if (!plan.put) return;
    saveFavorites(qc, plan.merged, data.symbols).catch(() => {
      // Keep the picks on the device for the next sign-in.
      useLocalFavorites.getState().set(mergeFavorites(useLocalFavorites.getState().symbols, pending));
    });
  }, [signedIn, data, qc]);

  const symbols = signedIn ? (data?.symbols ?? EMPTY) : local;
  const set = useMemo(() => new Set(symbols), [symbols]);
  const has = useCallback((s: string) => set.has(s.toUpperCase()), [set]);

  const toggle = useCallback(
    async (symbol: string) => {
      if (!signedIn) {
        const cur = useLocalFavorites.getState().symbols;
        useLocalFavorites.getState().set(toggleFavorite(cur, symbol));
        return;
      }
      const cur = qc.getQueryData<Favorites>(qk.favorites)?.symbols;
      if (!cur) throw new Error("favorites are still loading");
      await saveFavorites(qc, toggleFavorite(cur, symbol), cur);
    },
    [signedIn, qc],
  );

  return { symbols, has, toggle, ready: !signedIn || server.isSuccess, error: server.error };
}
