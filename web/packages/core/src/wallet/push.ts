import type { Deposit, Withdrawal, WithdrawAddress } from "./networks";

// Deposit and withdrawal pushes (private channels "deposits" and
// "withdrawals", internal/gateway/wsevents.go) patched into the cached
// lists at once, so a confirmation count moves the moment it arrives. The
// private sync (query/private.ts) also reloads the lists 500 ms later for
// the fields a push does not carry.

/** A deposit change on "deposits". */
export type DepositPush = {
  deposit_id: string;
  asset: string | null;
  network: string;
  tx_hash: string;
  amount: string;
  status: string;
  confirmations: number;
  required_confirmations: number;
  unclaimed: boolean;
  reason: string | null;
};

/** A withdrawal change on "withdrawals". */
export type WithdrawalPush = {
  withdrawal_id: string;
  asset: string;
  amount: string;
  status: string;
  tx_hash: string | null;
  confirmations: number;
  required_confirmations: number;
};

export type Page<T> = { items: T[]; next_cursor: string | null };
/** The cached pages of an infinite list (TanStack Query's InfiniteData). */
export type Pages<T> = { pages: Page<T>[]; pageParams: unknown[] };

function updateWhere<T>(data: Pages<T>, match: (t: T) => boolean, patch: (t: T) => T): Pages<T> | null {
  for (let i = 0; i < data.pages.length; i++) {
    const page = data.pages[i]!;
    const j = page.items.findIndex(match);
    if (j < 0) continue;
    const items = [...page.items];
    items[j] = patch(items[j]!);
    const pages = [...data.pages];
    pages[i] = { ...page, items };
    return { ...data, pages };
  }
  return null;
}

/** prependItem puts a new record at the top of the first page (a known one is replaced in place). */
export function prependItem<T>(data: Pages<T> | undefined, item: T, idOf: (t: T) => string): Pages<T> | undefined {
  if (!data || data.pages.length === 0) return data;
  const id = idOf(item);
  const replaced = updateWhere(data, (t) => idOf(t) === id, () => item);
  if (replaced) return replaced;
  const pages = [...data.pages];
  pages[0] = { ...pages[0]!, items: [item, ...pages[0]!.items] };
  return { ...data, pages };
}

/**
 * applyDepositPush updates a cached deposit, or shows a new one (just
 * detected) at the top with what the push says; `now` stands for its
 * detection time until the list reloads.
 */
export function applyDepositPush(data: Pages<Deposit> | undefined, p: DepositPush, now: string = new Date().toISOString()): Pages<Deposit> | undefined {
  if (!data) return data;
  const updated = updateWhere(
    data,
    (d) => d.id === p.deposit_id,
    (d) => ({
      ...d,
      status: p.status as Deposit["status"],
      confirmations: p.confirmations,
      required_confirmations: p.required_confirmations,
      unclaimed: p.unclaimed,
      reason: p.reason as Deposit["reason"],
    }),
  );
  if (updated) return updated;
  const fresh: Deposit = {
    id: p.deposit_id, kind: "CHAIN", asset: p.asset, network: p.network, address: "", contract: null, tx_hash: p.tx_hash, log_index: -1,
    block_number: 0, amount: p.amount, raw_amount: "", confirmations: p.confirmations, required_confirmations: p.required_confirmations,
    status: p.status as Deposit["status"], unclaimed: p.unclaimed, reason: p.reason as Deposit["reason"], detected_at: now, confirmed_at: null,
    credited_at: null,
  };
  return prependItem(data, fresh, (d) => d.id);
}

/** applyWithdrawalPush updates a cached withdrawal's status, hash and confirmations. */
export function applyWithdrawalPush(data: Pages<Withdrawal> | undefined, p: WithdrawalPush): Pages<Withdrawal> | undefined {
  if (!data) return data;
  return (
    updateWhere(
      data,
      (w) => w.id === p.withdrawal_id,
      (w) => ({
        ...w,
        status: p.status as Withdrawal["status"],
        tx_hash: p.tx_hash ?? w.tx_hash,
        confirmations: p.confirmations,
        required_confirmations: p.required_confirmations,
      }),
    ) ?? data
  );
}

type Book = { items: WithdrawAddress[] };

/** addToBook puts a saved address at the top of the cached address book (once). */
export function addToBook(book: Book | undefined, entry: WithdrawAddress): Book | undefined {
  if (!book) return book;
  return { ...book, items: [entry, ...book.items.filter((e) => e.id !== entry.id)] };
}

/** removeFromBook drops an entry from the cached address book. */
export function removeFromBook(book: Book | undefined, id: string): Book | undefined {
  if (!book) return book;
  return { ...book, items: book.items.filter((e) => e.id !== id) };
}
