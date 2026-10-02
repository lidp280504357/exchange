import { Button } from "@exchange/ui";
import { ArrowRight, FileText } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Card } from "../../kit/Page";
import { isAmount, type ConfigPatch, type InstrumentConfig, type PairConfig } from "./config";
import { useReview } from "./Review";

// The listing wizard (design 2026-10-02 §4.4): pairs pasted as CSV (one
// per line under a header) or a whole config document as JSON, previewed
// item by item and applied together.

/** The CSV columns of a pair; the first three and the steps are required. */
export const COLUMNS = [
  "symbol", "base_asset", "quote_asset", "tick_size", "lot_size", "min_quantity", "max_quantity", "min_notional", "price_band", "fee_tier",
  "reference_symbol", "reference_multiplier",
] as const;

const REQUIRED = ["base_asset", "quote_asset", "tick_size", "lot_size", "min_quantity", "max_quantity", "min_notional", "price_band"];
const AMOUNTS = ["tick_size", "lot_size", "min_quantity", "max_quantity", "min_notional", "price_band", "reference_multiplier"];

const EXAMPLE = `${COLUMNS.join(",")}
LINK-BTC,LINK,BTC,0.0000001,0.01,0.01,100000,0.0001,0.1,default,LINKBTC,1`;

type Parsed = { patch: ConfigPatch | null; count: number; errors: string[] };

/** parse reads the pasted text: a JSON config document, or pairs as CSV under a header line. */
export function parse(text: string, cfg: InstrumentConfig | undefined, line: (n: number, what: string) => string): Parsed {
  const s = text.trim();
  if (!s) return { patch: null, count: 0, errors: [] };
  if (s.startsWith("{")) {
    try {
      const doc = JSON.parse(s) as ConfigPatch;
      const count = (doc.fee_schedules?.length ?? 0) + (doc.assets?.length ?? 0) + (doc.pairs?.length ?? 0) + (doc.contracts?.length ?? 0);
      return { patch: doc, count, errors: [] };
    } catch (err) {
      return { patch: null, count: 0, errors: [String(err)] };
    }
  }
  const lines = s.split(/\r?\n/).map((l) => l.trim()).filter(Boolean);
  const header = (lines[0] ?? "").split(",").map((h) => h.trim().toLowerCase());
  const errors: string[] = [];
  const unknown = header.filter((h) => !(COLUMNS as readonly string[]).includes(h));
  const missing = REQUIRED.filter((h) => !header.includes(h));
  if (unknown.length || missing.length) {
    return { patch: null, count: 0, errors: [line(1, [...unknown.map((h) => `?${h}`), ...missing.map((h) => `+${h}`)].join(" "))] };
  }
  const tiers = new Set((cfg?.fee_schedules ?? []).map((f) => f.tier));
  const assets = new Set((cfg?.assets ?? []).map((a) => a.asset_code));
  const pairs: PairConfig[] = [];
  lines.slice(1).forEach((l, i) => {
    const cells = l.split(",").map((c) => c.trim());
    const row: Record<string, string> = Object.fromEntries(header.map((h, j) => [h, cells[j] ?? ""]));
    const base = row.base_asset!.toUpperCase();
    const quote = row.quote_asset!.toUpperCase();
    const pair: PairConfig = {
      symbol: (row.symbol || `${base}-${quote}`).toUpperCase(), base_asset: base, quote_asset: quote, tick_size: row.tick_size!, lot_size: row.lot_size!,
      min_quantity: row.min_quantity!, max_quantity: row.max_quantity!, min_notional: row.min_notional!, price_band: row.price_band!,
      fee_tier: row.fee_tier || "default", status: "PREPARE", reference_symbol: (row.reference_symbol ?? "").toUpperCase(),
      reference_multiplier: row.reference_multiplier || "1",
    };
    const bad = [
      ...AMOUNTS.filter((k) => !isAmount(pair[k as keyof PairConfig] as string)),
      ...(assets.size && !assets.has(base) ? [base] : []),
      ...(assets.size && !assets.has(quote) ? [quote] : []),
      ...(tiers.size && !tiers.has(pair.fee_tier) ? [pair.fee_tier] : []),
    ];
    if (bad.length) errors.push(line(i + 2, bad.join(", ")));
    pairs.push(pair);
  });
  return { patch: errors.length ? null : { pairs }, count: pairs.length, errors };
}

/** ListingWizard previews and applies pasted pairs or a pasted config document. */
export function ListingWizard({ cfg }: { cfg: InstrumentConfig | undefined }) {
  const { t } = useTranslation();
  const [text, setText] = useState("");
  const parsed = useMemo(() => parse(text, cfg, (n, what) => t("admin.listing.lineProblem", { n, what })), [text, cfg, t]);
  const { review, busy, dialog } = useReview();
  return (
    <Card title={t("admin.listing.wizard")} extra={<Button size="sm" variant="ghost" icon={<FileText size={14} />} onClick={() => setText(EXAMPLE)}>{t("admin.listing.example")}</Button>}>
      <div className="flex flex-col gap-3">
        <p className="text-sm text-fg-3">{t("admin.listing.wizardHelp", { columns: COLUMNS.join(", ") })}</p>
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          rows={10}
          spellCheck={false}
          aria-label={t("admin.listing.wizard")}
          placeholder={EXAMPLE}
          className="w-full resize-y rounded-2 border border-line-1 bg-bg-2 px-3 py-2 font-mono text-xs text-fg-1 outline-none placeholder:text-fg-3 focus-visible:border-brand focus-visible:ring-1 focus-visible:ring-brand"
        />
        {parsed.errors.length > 0 && (
          <ul className="flex flex-col gap-1 text-sm text-danger" role="alert">
            {parsed.errors.map((e) => (
              <li key={e}>{e}</li>
            ))}
          </ul>
        )}
        <div className="flex items-center gap-3">
          <Button
            icon={<ArrowRight size={16} />}
            loading={busy}
            disabled={!parsed.patch || parsed.count === 0}
            onClick={() => parsed.patch && void review(parsed.patch, t("admin.listing.wizardTitle", { n: parsed.count }), () => setText(""))}
          >
            {t("admin.listing.preview")}
          </Button>
          {parsed.count > 0 && <span className="text-sm text-fg-3">{t("admin.listing.parsed", { n: parsed.count })}</span>}
        </div>
      </div>
      {dialog}
    </Card>
  );
}
