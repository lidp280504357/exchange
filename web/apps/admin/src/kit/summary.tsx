import { dec, formatDecimal } from "@exchange/core";
import { Badge, cn, KeyTag } from "@exchange/ui";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

// The console's item values in words (A94, the user's 2026-10-09 23:3x
// screenshot): a switch as 开/关 with whom it is on for, in grey - red is
// the status badge's, for a real problem; an amount grouped by thousands
// with its asset in grey; numbers in the text face, monospace only for keys
// and IDs (KeyTag).

/** Translate is a page's t, for the helpers that put values in words. */
export type Translate = ReturnType<typeof useTranslation>["t"];

/** OnOff is a switch's state as a small badge. */
export function OnOff({ on }: { on: unknown }) {
  const { t } = useTranslation();
  return <Badge tone={on ? "success" : "neutral"}>{t(on ? "admin.summary.on" : "admin.summary.off")}</Badge>;
}

/**
 * FlagState is a switch: its key (when asked for), 开/关, and whom it is on
 * for - everyone or by rules - in grey (scope false: a switch for all alike,
 * nothing said).
 */
export function FlagState({ flag, on, rules, scope = true }: { flag?: string; on: unknown; rules?: unknown; scope?: boolean }) {
  const { t } = useTranslation();
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5">
      {flag && <KeyTag>{flag}</KeyTag>}
      <OnOff on={on} />
      {on && scope ? <span className="text-xs text-fg-3">{t(rules ? "admin.summary.withRules" : "admin.summary.forEveryone")}</span> : null}
    </span>
  );
}

/** Amount is a decimal grouped by thousands, its asset in grey. */
export function Amount({ value, asset, decimals, className }: { value: string | null | undefined; asset?: string; decimals?: number; className?: string }) {
  return (
    <span className={cn("whitespace-nowrap tabular-nums", className)}>
      {formatDecimal(value ?? "", { decimals })}
      {asset && <span className="ml-1 text-fg-3">{asset}</span>}
    </span>
  );
}

/** AmountGrid lists amounts by asset (a row's details), right-aligned; problem ones in red. */
export function AmountGrid({ rows, problem }: { rows: [string, string][]; problem?: (asset: string, value: string) => boolean }) {
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-x-6">
      {rows.map(([asset, value]) => (
        <div key={asset} className="flex items-baseline justify-between gap-3 border-b border-line-1 py-1.5">
          <span className="text-fg-3">{asset}</span>
          <span className={cn("tabular-nums", problem?.(asset, value) ? "font-medium text-danger-strong" : "text-fg-1")}>{formatDecimal(value, {})}</span>
        </div>
      ))}
    </div>
  );
}

/** Lines are a row's details: labelled values, one a line. */
export function Lines({ items }: { items: [ReactNode, ReactNode][] }) {
  return (
    <dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-6 gap-y-1.5">
      {items.map(([label, value], i) => (
        <div key={i} className="contents">
          <dt className="text-fg-3">{label}</dt>
          <dd className="min-w-0 break-words text-fg-1">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

/** lowest is the smallest of amounts by asset, with its asset (undefined for none). */
export function lowest(rows: [string, string][]): [string, string] | undefined {
  let min: [string, string] | undefined;
  for (const r of rows) if (dec.isDecimal(r[1]) && (!min || dec.lt(r[1], min[1]))) min = r;
  return min;
}

/** positive is an amount above zero. */
export const positive = (v: string) => dec.isDecimal(v) && dec.gt(v, "0");

/** few names a list's first max items in a sentence (USDT、BTC、ETH +20). */
export function few(items: string[], t: Translate, max = 3): string {
  const shown = items.slice(0, max).join(t("admin.summary.sep"));
  return items.length > max ? `${shown} ${t("ui.summary.more", { n: items.length - max })}` : shown;
}
