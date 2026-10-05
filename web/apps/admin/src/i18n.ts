import { accessEn, accessZh } from "./messages/access";
import { accountEn, accountZh } from "./messages/account";
import { en } from "./messages/en";
import { shellEn, shellZh } from "./messages/shell";
import { zh } from "./messages/zh";

// The console's strings (Chinese first; operators read Chinese), merged
// over the shared ones; `errors` adds the console's error codes. Here are
// what the sign-in, the setup page and the shell's own parts say; the
// pages' strings are in pageMessages.ts, registered by the signed-in
// console when its chunk loads (A40: the sign-in page does not download
// every page's text).

type Tree = Record<string, unknown>;

/** merge deep-merges trees of strings, the later winning. */
export function merge(base: Tree, ...extras: Tree[]): Tree {
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

/** entryMessages are the strings of the sign-in, the setup page and the shell, given to initI18n. */
export const entryMessages = {
  "zh-CN": merge(zh, accountZh, accessZh, shellZh),
  en: merge(en, accountEn, accessEn, shellEn),
};
