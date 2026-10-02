import { en } from "./messages/en";
import { instrumentsEn, instrumentsZh } from "./messages/instruments";
import { moneyEn, moneyZh } from "./messages/money";
import { tradingEn, tradingZh } from "./messages/trading";
import { usersEn, usersZh } from "./messages/users";
import { walletEn, walletZh } from "./messages/wallet";
import { zh } from "./messages/zh";

// The console's strings (Chinese first; operators read Chinese), merged
// over the shared ones; `errors` adds the console's error codes. The
// strings of newer pages live in their own files and merge in here.

type Tree = Record<string, unknown>;

function merge(base: Tree, ...extras: Tree[]): Tree {
  const out: Tree = { ...base };
  for (const extra of extras) {
    for (const [k, v] of Object.entries(extra)) {
      const b = out[k];
      out[k] = isTree(b) && isTree(v) ? merge(b, v) : v;
    }
  }
  return out;
}

const isTree = (v: unknown): v is Tree => typeof v === "object" && v !== null && !Array.isArray(v);

export const adminMessages = {
  "zh-CN": merge(zh, usersZh, walletZh, moneyZh, tradingZh, instrumentsZh),
  en: merge(en, usersEn, walletEn, moneyEn, tradingEn, instrumentsEn),
};
