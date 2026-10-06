import { dec, formatDecimal, formatPercent, i18n } from "@exchange/core";
import { Badge, FormField, Input } from "@exchange/ui";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { registerMarginMessages } from "./messages";

// What the margin pages share (design 2026-10-06 §8, E5): their strings,
// what a change came to, margin levels toned by their thresholds, hourly
// rates with their yearly equivalent.

// The pages' strings come with their chunks, before any of them renders.
registerMarginMessages();

/** outcome says what a change of terms came to: applied at once (200), or a request waiting for a second ADMIN (202). */
export function outcome(res: unknown): string {
  return i18n.t((res as { approval?: unknown } | null)?.approval ? "admin.margin.requested" : "admin.margin.applied");
}

/** lineText is a margin level or threshold as the pages show it: two decimals (1.10), "—" for none. */
export const lineText = (v: string | null) => formatDecimal(v, { decimals: 2 });

/** Level is a margin level toned by its account's thresholds; ∞ without liabilities. */
export function Level({ level, warning, liquidation }: { level: string | null; warning: string; liquidation: string }) {
  if (level === null) return <span className="font-mono text-fg-3">∞</span>;
  const tone = dec.lte(level, liquidation) ? "danger" : dec.lte(level, warning) ? "warn" : "success";
  return (
    <Badge tone={tone} className="font-mono">
      {formatDecimal(level, { decimals: 2 })}
    </Badge>
  );
}

/** HOURS_A_YEAR turns an hourly rate into a yearly one (simple, interest is not compounded, §4.3). */
const HOURS_A_YEAR = "8760";

/** Rate is an hourly rate as a percentage, with its yearly equivalent under it. */
export function Rate({ hourly }: { hourly: string }) {
  const { t } = useTranslation();
  return (
    <span className="inline-flex flex-col items-end font-mono tabular-nums">
      <span>{t("admin.margin.perHour", { rate: formatPercent(hourly, 4, false) })}</span>
      <span className="text-xs text-fg-3">{t("admin.margin.perYear", { rate: formatPercent(dec.mul(hourly, HOURS_A_YEAR), 2, false) })}</span>
    </span>
  );
}

/** toPercent and fromPercent turn a fraction into the percentage a form shows and back, exactly. */
export const toPercent = (fraction: string) => dec.normalize(dec.mul(fraction, "100"));
export const fromPercent = (percent: string) => dec.div(percent.trim(), "100", 12);

/** isNumber is a non-negative decimal as typed. */
export const isNumber = (v: string) => /^\d+(\.\d+)?$/.test(v.trim());

/** Field is a labelled text input with its error and unit. */
export function Field({
  label, value, onChange, error, hint, unit, disabled,
}: {
  label: ReactNode;
  value: string;
  onChange: (v: string) => void;
  error?: string;
  hint?: ReactNode;
  unit?: string;
  disabled?: boolean;
}) {
  return (
    <FormField label={label} error={error} hint={hint}>
      <Input value={value} onValueChange={onChange} unit={unit} disabled={disabled} inputMode="decimal" className="font-mono" />
    </FormField>
  );
}

/** Change is one field's value before and after, for a confirmation. */
export type Change = { label: string; from: string; to: string };

/** Changes lists what a confirmation changes, before and after. */
export function Changes({ changes }: { changes: Change[] }) {
  const { t } = useTranslation();
  if (changes.length === 0) return <span className="text-sm text-fg-3">{t("admin.margin.noChange")}</span>;
  return (
    <span className="flex flex-col gap-0.5 text-xs">
      {changes.map((c) => (
        <span key={c.label}>
          <span className="text-fg-3">{c.label}</span> <span className="font-mono">{c.from}</span> → <span className="font-mono text-fg-1">{c.to}</span>
        </span>
      ))}
    </span>
  );
}
