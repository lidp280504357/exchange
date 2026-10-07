import { useEffect, useMemo, useState } from "react";
import { ApiError } from "../api/errors";
import { availableOf, useBalances } from "../assets/hooks";
import { checkTransfer, type TransferIssue } from "../assets/transfer";
import * as dec from "../format/decimal";
import { useOpenProducts } from "../platform/products";
import { assetDecimals, useAssets } from "../trading/pairs";
import { newIdempotencyKey } from "../trading/orders";
import { useMarginAccounts, useMarginActions, useMarginAssets, useMarginPairs, useMaxBorrowable } from "./hooks";
import { balanceOf, hasDebt, owed, repayMax, type MarginAccount, type MarginAccountType, type MarginPair } from "./math";

// The transfer, borrow and repay forms of both sites (margin design §7):
// which account (cross, or a pair's isolated one), which asset, how much
// at most, the checks of a typed amount and the call, with one
// Idempotency-Key per intended action. The sites draw the form.

export type MarginActionKind = "transfer" | "borrow" | "repay";

// Keys of actions whose outcome is unknown (a 5xx or a lost response),
// by the intended action: trying the same action again, even from a dialog
// opened anew, reuses the key, so it acts once. An answer forgets it; so
// does the margin of ten minutes (margin-service keeps keys far longer).
const unsettled = new Map<string, { key: string; at: number }>();
const UNSETTLED_FOR = 10 * 60_000;

/** keyFor is the Idempotency-Key of an intended action: a fresh one, or the unsettled one of the same action. */
export function keyFor(action: string, now = Date.now()): string {
  const prior = unsettled.get(action);
  if (prior && now - prior.at < UNSETTLED_FOR) return prior.key;
  const key = newIdempotencyKey();
  unsettled.set(action, { key, at: now });
  return key;
}

/** settle forgets an action's key once it is answered (done or refused). */
export function settle(action: string): void {
  unsettled.delete(action);
}

/**
 * transferOutMax is what may leave a margin account of an asset at most
 * as the page knows it: what is free, and no more than the asset's net
 * (what its own debt does not hold); margin-service also keeps the margin
 * level at the warning level.
 */
export function transferOutMax(b: { free: string; net: string }): string {
  const max = dec.min(b.free, b.net);
  return dec.sign(max) > 0 ? dec.normalize(max) : "0";
}
export type Direction = "IN" | "OUT";

export type MarginFormInit = { account?: MarginAccountType; symbol?: string; asset?: string; direction?: Direction };

/** The outcome the sites announce: what moved, where. */
export type MarginDone = { kind: MarginActionKind; direction: Direction; amount: string; asset: string; account: MarginAccountType; symbol: string };

/** accountOf finds an account among the caller's: the cross one, or the isolated one of symbol. */
export function accountOf(
  data: { cross: MarginAccount; isolated: MarginAccount[] } | undefined,
  account: MarginAccountType,
  symbol: string,
): MarginAccount | undefined {
  if (!data) return undefined;
  return account === "MARGIN_CROSS" ? data.cross : data.isolated.find((a) => a.symbol === symbol);
}

/**
 * assetChoices lists the assets a form offers: an isolated account its
 * pair's two; the cross account the margin assets that count as margin
 * (transfers) or may be borrowed (borrowing); repaying offers what the
 * account owes, or everything it could owe when it owes nothing. While
 * spot is closed (repayOnly) a transfer in takes only what the account
 * owes, to repay it: none when it owes nothing (design 2026-10-07,
 * product line switches §1 #7; F20).
 */
export function assetChoices(
  kind: MarginActionKind,
  account: MarginAccountType,
  pair: Pick<MarginPair, "base" | "quote"> | undefined,
  terms: { asset: string; borrowable: boolean; collateral: boolean }[] | undefined,
  owner: MarginAccount | undefined,
  inward?: { direction: "IN" | "OUT"; repayOnly: boolean },
): string[] {
  if (kind === "transfer" && inward?.direction === "IN" && inward.repayOnly) return (owner?.balances ?? []).filter(hasDebt).map((b) => b.asset);
  const all =
    account === "MARGIN_ISOLATED"
      ? pair
        ? [pair.base, pair.quote]
        : []
      : (terms ?? []).filter((a) => (kind === "borrow" ? a.borrowable : kind === "transfer" ? a.collateral : a.borrowable)).map((a) => a.asset);
  if (kind !== "repay") return all;
  const owing = (owner?.balances ?? []).filter(hasDebt).map((b) => b.asset);
  return owing.length > 0 ? owing : all;
}

/**
 * useMarginForm holds one transfer, borrow or repay form: its account,
 * asset and amount, what may move at most (transfers in: the SPOT
 * balance; out: the account's free balance, the server also keeping the
 * margin level at the warning level; borrowing: max-borrowable; repaying:
 * the debt as far as the free balance goes), the amount's issue and the
 * call. Repaying "all" sends ALL, so the interest of the hour that ends
 * meanwhile is repaid too.
 */
export function useMarginForm(kind: MarginActionKind, init: MarginFormInit = {}) {
  const accounts = useMarginAccounts();
  const terms = useMarginAssets();
  const pairs = useMarginPairs();
  const balances = useBalances();
  const assets = useAssets();
  const actions = useMarginActions();

  const [account, setAccountState] = useState<MarginAccountType>(init.account ?? "MARGIN_CROSS");
  const [symbol, setSymbolState] = useState(init.symbol ?? "");
  const [direction, setDirectionState] = useState<Direction>(init.direction ?? "IN");
  const [asset, setAssetState] = useState((init.asset ?? "USDT").toUpperCase());
  const [amount, setAmountState] = useState("");
  const [all, setAll] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const isolatedPairs = useMemo(() => (pairs.data?.items ?? []).filter((p) => p.isolated), [pairs.data]);
  const pair = isolatedPairs.find((p) => p.symbol === symbol);
  const owner = accountOf(accounts.data, account, symbol);
  // Margin trades on the spot books: while spot is closed the server takes
  // transfers in only of what the account owes, to repay it (F20).
  const spotOpen = useOpenProducts().spot;
  const repayOnly = kind === "transfer" && !spotOpen;
  const owing = useMemo(() => (owner?.balances ?? []).filter(hasDebt).map((b) => b.asset), [owner]);
  const inward = !repayOnly || owing.length > 0;
  const choices = useMemo(
    () => assetChoices(kind, account, pair, terms.data, owner, { direction, repayOnly }),
    [kind, account, pair, terms.data, owner, direction, repayOnly],
  );
  // No transfer in to offer (the account owes nothing while spot is closed): out only; and a coin it does not owe gives way to one it does.
  useEffect(() => {
    if (!repayOnly || accounts.isPending) return;
    if (!inward && direction === "IN") setDirectionState("OUT");
    else if (direction === "IN" && owing.length > 0 && !owing.includes(asset)) setAssetState(owing[0]!);
  }, [repayOnly, accounts.isPending, inward, direction, owing, asset]);
  const row = balanceOf(owner, asset);
  const decimals = assetDecimals(assets.data?.assets, asset, 8);
  const borrowable = useMaxBorrowable(account, symbol, asset, kind === "borrow");
  const term = terms.data?.find((a) => a.asset === asset);

  let max: string;
  if (kind === "transfer") max = direction === "IN" ? availableOf(balances.data?.balances, "SPOT", asset) : transferOutMax(row);
  else if (kind === "borrow") max = borrowable.data?.amount ?? "0";
  else max = repayMax(row);
  max = dec.sign(max) > 0 ? dec.round(max, decimals, "down") : "0";

  const issue: TransferIssue | "account" | null = account === "MARGIN_ISOLATED" && !pair ? "account" : all ? null : checkTransfer(amount, max, decimals);
  const loading = accounts.isPending || terms.isPending || (account === "MARGIN_ISOLATED" && pairs.isPending);
  const ready = !busy && !loading && issue === null && (all || amount.trim() !== "") && (!all || dec.sign(max) > 0);

  const clear = () => {
    setError(null);
    setAll(false);
  };
  const setAccount = (a: MarginAccountType) => {
    setAccountState(a);
    clear();
  };
  const setSymbol = (s: string) => {
    setSymbolState(s);
    const p = isolatedPairs.find((x) => x.symbol === s);
    if (p && asset !== p.base && asset !== p.quote) setAssetState(p.quote);
    clear();
  };
  const setDirection = (d: Direction) => {
    setDirectionState(d);
    setAmountState("");
    clear();
  };
  const setAsset = (a: string) => {
    setAssetState(a);
    setAmountState("");
    clear();
  };
  const setAmount = (v: string) => {
    setAmountState(v);
    clear();
  };
  /** fillMax fills in the maximum; repaying it all sends ALL. */
  const fillMax = () => {
    setAmountState(max === "0" ? "" : dec.normalize(max));
    setError(null);
    setAll(kind === "repay" && dec.sign(max) > 0 && dec.gte(max, owed(row)));
  };

  const submit = async (): Promise<MarginDone | null> => {
    if (!ready) return null;
    const value = all ? "ALL" : dec.normalize(amount.trim());
    const body = { account, ...(account === "MARGIN_ISOLATED" ? { symbol } : {}), asset, amount: value };
    // One key per intended action: a retry after a lost response acts once;
    // a refusal replays under its key, so the next try gets a new one.
    const action = `${kind}:${direction}:${JSON.stringify(body)}`;
    const key = keyFor(action);
    setBusy(true);
    setError(null);
    try {
      let moved = value;
      if (kind === "transfer") await actions.transfer({ ...body, direction }, key);
      else if (kind === "borrow") await actions.borrow(body, key);
      else {
        const r = await actions.repay(body, key);
        moved = dec.normalize(dec.add(r.interest_repaid, r.principal_repaid));
      }
      settle(action);
      setAmountState("");
      setAll(false);
      return { kind, direction, amount: moved, asset, account, symbol };
    } catch (err) {
      if (err instanceof ApiError && err.status < 500) settle(action);
      setError(err);
      return null;
    } finally {
      setBusy(false);
    }
  };

  return {
    kind,
    account, setAccount,
    symbol, setSymbol,
    direction, setDirection,
    asset, setAsset,
    amount, setAmount,
    all,
    fillMax,
    choices,
    /** Whether a transfer in is offered; false while spot is closed and the account owes nothing. */
    inward,
    /** A transfer in only repays (spot closed): the form says so. */
    repayOnly,
    pairs: isolatedPairs,
    pair,
    owner,
    row,
    term,
    decimals,
    max,
    limitedBy: kind === "borrow" ? borrowable.data?.limited_by : undefined,
    maxPending: kind === "borrow" ? borrowable.isPending && borrowable.fetchStatus !== "idle" : kind === "transfer" && direction === "IN" ? balances.isPending : accounts.isPending,
    issue,
    loading,
    ready,
    busy,
    error,
    submit,
  };
}

export type MarginForm = ReturnType<typeof useMarginForm>;
