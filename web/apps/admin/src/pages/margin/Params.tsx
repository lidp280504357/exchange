import { dec, formatDecimal, formatPercent } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import {
  Badge, Button, DataTable, Drawer, ErrorState, Progress, Segmented, Skeleton, Switch, Tabs, type ColumnDef, type DataColumnMeta,
} from "@exchange/ui";
import { Pencil } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { DangerAction } from "../../kit/actions";
import { EnumBadge } from "../../kit/enums";
import { useFilters } from "../../kit/filters";
import { TimeText } from "../../kit/format";
import { Card, Page } from "../../kit/Page";
import { marginKey, useMarginAssets, useMarginPairs, useMarginSettings, type MarginAsset, type MarginPair, type MarginSettings } from "./api";
import { Changes, Field, fromPercent, isNumber, lineText, outcome, Rate, toPercent, type Change } from "./common";

type AssetParams = AdminSchemas["MarginAssetParams"];
type Leverage = AdminSchemas["MarginLeverage"];

const right: DataColumnMeta = { align: "right" };
const LEVERAGES: Leverage[] = [3, 5, 10];

/** A change reloads the terms and the approvals that may now list it. */
const changed = [marginKey, ["admin", "approvals"]];

/** Pending marks terms a request waits to change, linking to the approvals. */
function Pending({ id }: { id: string | null }) {
  const { t } = useTranslation();
  if (!id) return null;
  return (
    <Link to="/approvals" title={id} data-testid="margin-pending">
      <Badge tone="warn">{t("admin.margin.pending")}</Badge>
    </Link>
  );
}

/**
 * Margin parameters (design 2026-10-06 §4, §8; A55, A56): the assets that
 * can be borrowed or count as collateral (pools, caps, interest,
 * haircuts), the pairs' isolated terms and the cross account's, each with
 * its liquidation fee; margin-service keeps them (asset_terms, pair_terms,
 * cross_terms). Changing them needs instruments.trading: what only stops
 * new borrowing or new isolated accounts applies at once, everything else
 * waits for a second ADMIN (a MARGIN_PARAMS request on the approvals page).
 */
export default function Params({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const filters = useFilters(["tab"]);
  const tab = ["pairs", "settings"].includes(filters.values.tab ?? "") ? filters.values.tab! : "assets";
  const edit = can(admin, "instruments.trading");
  return (
    <Page title={t("admin.nav.marginParams")} help={t("admin.margin.params.help")}>
      <Tabs
        items={[
          { value: "assets", label: t("admin.margin.params.assets") },
          { value: "pairs", label: t("admin.margin.params.pairs") },
          { value: "settings", label: t("admin.margin.params.settings") },
        ]}
        value={tab}
        onValueChange={(v) => filters.set({ tab: v === "assets" ? "" : v })}
        aria-label={t("admin.nav.marginParams")}
      />
      {tab === "assets" && <Assets edit={edit} />}
      {tab === "pairs" && <Pairs edit={edit} />}
      {tab === "settings" && <Settings edit={edit} />}
    </Page>
  );
}

/** Updated says who last changed terms (the seed when nobody did) and when. */
function Updated({ v }: { v: { version: number; updated_by: string; updated_at: string } }) {
  const { t } = useTranslation();
  return (
    <span className="flex flex-col text-xs">
      <span>
        v{v.version} · {v.updated_by || t("admin.margin.seed")}
      </span>
      <TimeText value={v.updated_at} />
    </span>
  );
}

function Assets({ edit }: { edit: boolean }) {
  const { t } = useTranslation();
  const q = useMarginAssets();
  const [editing, setEditing] = useState<MarginAsset | null>(null);
  const columns = useMemo<ColumnDef<MarginAsset, unknown>[]>(
    () => [
      {
        id: "asset", header: t("admin.common.asset"),
        cell: ({ row: { original: a } }) => (
          <span className="flex flex-col gap-1">
            <span className="font-medium text-fg-1">{a.asset}</span>
            <span className="flex gap-1">
              <Badge tone={a.borrowable ? "success" : "neutral"} size="sm">{t(a.borrowable ? "admin.margin.borrowable" : "admin.margin.notBorrowable")}</Badge>
              <Badge tone={a.collateral ? "info" : "neutral"} size="sm">{t(a.collateral ? "admin.margin.collateral" : "admin.margin.notCollateral")}</Badge>
            </span>
          </span>
        ),
      },
      {
        id: "pool", header: t("admin.margin.fields.pool"), meta: { width: 220 },
        cell: ({ row: { original: a } }) => (
          <Progress
            value={Number(a.utilization) * 100}
            tone={dec.gte(a.utilization, a.float_kink) ? "warn" : "brand"}
            size="xs"
            label={t("admin.margin.lent", { pct: formatPercent(a.utilization, 1, false) })}
            valueText={`${formatDecimal(a.lent)} / ${formatDecimal(a.pool_cap)}`}
          />
        ),
      },
      { id: "user", header: t("admin.margin.fields.userCap"), meta: right, cell: ({ row }) => <span className="font-mono">{formatDecimal(row.original.user_cap)}</span> },
      {
        id: "rate", header: t("admin.margin.fields.rate"), meta: right,
        cell: ({ row: { original: a } }) => (
          <span className="inline-flex items-start gap-2">
            <EnumBadge group="rateModel" code={a.interest_model} />
            <Rate hourly={a.hourly_rate} />
          </span>
        ),
      },
      { id: "haircut", header: t("admin.margin.fields.haircut"), meta: right, cell: ({ row }) => <span className="font-mono">{row.original.haircut}</span> },
      {
        id: "owed", header: t("admin.margin.interestOwed"), meta: right,
        cell: ({ row }) => <span className="font-mono">{formatDecimal(row.original.interest_owed)}</span>,
      },
      { id: "borrowers", header: t("admin.margin.borrowers"), meta: right, cell: ({ row }) => row.original.borrowers },
      { id: "updated", header: t("admin.margin.updated"), cell: ({ row }) => <Updated v={row.original} /> },
      {
        id: "act", header: "", meta: right,
        cell: ({ row: { original: a } }) => (
          <span className="inline-flex items-center gap-2">
            <Pending id={a.pending_approval_id} />
            {edit && (
              <Button size="sm" variant="secondary" icon={<Pencil size={14} />} onClick={() => setEditing(a)} data-testid={`margin-edit-${a.asset}`}>
                {t("admin.margin.edit")}
              </Button>
            )}
          </span>
        ),
      },
    ],
    [t, edit],
  );
  if (q.isError) return <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />;
  return (
    <>
      <DataTable columns={columns} data={q.data ?? []} getRowId={(a) => a.asset} loading={q.isPending} density="compact" aria-label="margin-assets" />
      {editing && <AssetDrawer asset={editing} onClose={() => setEditing(null)} />}
    </>
  );
}

/** Form is an asset's terms as the form edits them: rates in percent an hour, the kink in percent. */
type Form = {
  borrowable: boolean; collateral: boolean; pool_cap: string; user_cap: string; interest_model: AssetParams["interest_model"]; fixed: string;
  base: string; kink: string; kink_rate: string; max: string; haircut: string;
};

const toForm = (a: AssetParams): Form => ({
  borrowable: a.borrowable, collateral: a.collateral, pool_cap: a.pool_cap, user_cap: a.user_cap, interest_model: a.interest_model,
  fixed: toPercent(a.fixed_rate), base: toPercent(a.float_base), kink: toPercent(a.float_kink), kink_rate: toPercent(a.float_kink_rate),
  max: toPercent(a.float_max_rate), haircut: a.haircut,
});

const fromForm = (f: Form): AssetParams => ({
  borrowable: f.borrowable, collateral: f.collateral, haircut: f.haircut.trim(), pool_cap: f.pool_cap.trim(), user_cap: f.user_cap.trim(),
  interest_model: f.interest_model, fixed_rate: fromPercent(f.fixed), float_base: fromPercent(f.base), float_kink: fromPercent(f.kink),
  float_kink_rate: fromPercent(f.kink_rate), float_max_rate: fromPercent(f.max),
});

/**
 * braking reports whether a change only stops new borrowing (borrowing
 * switched off, a lower cap): it applies at once. The interest model and
 * its rates, the haircut, collateral, borrowing switched on or a higher
 * cap waits for a second ADMIN (admin.yaml PUT /admin/v1/margin/assets/{asset}).
 */
function braking(a: AssetParams, b: AssetParams): boolean {
  const rates =
    a.interest_model === b.interest_model && dec.eq(a.fixed_rate, b.fixed_rate) && dec.eq(a.float_base, b.float_base) &&
    dec.eq(a.float_kink, b.float_kink) && dec.eq(a.float_kink_rate, b.float_kink_rate) && dec.eq(a.float_max_rate, b.float_max_rate);
  if (!rates || a.collateral !== b.collateral || !dec.eq(a.haircut, b.haircut)) return false;
  if (b.borrowable && !a.borrowable) return false;
  return dec.lte(b.pool_cap, a.pool_cap) && dec.lte(b.user_cap, a.user_cap);
}

/** differs compares a form field as typed: numbers by value ("0.950" is 0.95). */
const differs = (x: string | boolean, y: string | boolean) =>
  typeof x === "string" && typeof y === "string" && isNumber(x) && isNumber(y) ? !dec.eq(x.trim(), y.trim()) : x !== y;

function AssetDrawer({ asset: a, onClose }: { asset: MarginAsset; onClose: () => void }) {
  const { t } = useTranslation();
  const [f, setF] = useState<Form>(() => toForm(a));
  const set = (k: keyof Form) => (v: string) => setF((p) => ({ ...p, [k]: v }));
  // A field as the confirmation shows it: switches on or off, the model by name, rates with their unit.
  const shown = (k: keyof Form, v: string | boolean): string => {
    if (typeof v === "boolean") return t(v ? "admin.launch.on" : "admin.launch.off");
    if (k === "interest_model") return t(`admin.enum.rateModel.${v}`);
    if (k === "kink") return `${v}%`;
    return ["fixed", "base", "kink_rate", "max"].includes(k) ? `${v}%/h` : v;
  };
  const bad = (v: string) => (!isNumber(v) ? t("admin.margin.badNumber") : undefined);
  const errors = {
    pool: bad(f.pool_cap),
    user: bad(f.user_cap) ?? (isNumber(f.pool_cap) && dec.gt(f.user_cap, f.pool_cap) ? t("admin.margin.userOverPool") : undefined),
    fixed: bad(f.fixed),
    base: bad(f.base),
    kink: !isNumber(f.kink) || dec.lte(f.kink, "0") || dec.gte(f.kink, "100") ? t("admin.margin.badKink") : undefined,
    kinkRate: bad(f.kink_rate) ?? (isNumber(f.base) && isNumber(f.kink_rate) && dec.lt(f.kink_rate, f.base) ? t("admin.margin.notRising") : undefined),
    max: bad(f.max) ?? (isNumber(f.kink_rate) && isNumber(f.max) && dec.lt(f.max, f.kink_rate) ? t("admin.margin.notRising") : undefined),
    // asset_terms holds a haircut above 0 and at most 1.
    haircut: !isNumber(f.haircut) || dec.lte(f.haircut, "0") || dec.gt(f.haircut, "1") ? t("admin.margin.badHaircut") : undefined,
  };
  const ok = Object.values(errors).every((e) => !e);
  const before = toForm(a);
  const after = ok ? fromForm(f) : null;
  const approval = after ? !braking(a, after) : true;
  // Every field is sent (margin-service sets them all), so every one changed is shown, the other model's rates too.
  const changes: Change[] = (Object.keys(before) as (keyof Form)[])
    .filter((k) => differs(before[k], f[k]))
    .map((k) => ({ label: t(`admin.margin.form.${k}`), from: shown(k, before[k]), to: shown(k, f[k]) }));
  return (
    <Drawer open onOpenChange={(o) => !o && onClose()} title={t("admin.margin.params.editAsset", { asset: a.asset })} description={t("admin.margin.params.assetHint")} width={600}>
      <div className="flex flex-col gap-4">
        <div className="grid gap-3 sm:grid-cols-2">
          <Switch checked={f.borrowable} onCheckedChange={(v) => setF((p) => ({ ...p, borrowable: v }))} label={t("admin.margin.form.borrowable")} />
          <Switch checked={f.collateral} onCheckedChange={(v) => setF((p) => ({ ...p, collateral: v }))} label={t("admin.margin.form.collateral")} />
          <Field label={t("admin.margin.form.pool_cap")} value={f.pool_cap} onChange={set("pool_cap")} error={errors.pool} unit={a.asset} />
          <Field label={t("admin.margin.form.user_cap")} value={f.user_cap} onChange={set("user_cap")} error={errors.user} unit={a.asset} hint={t("admin.margin.userCapHint")} />
        </div>
        <Card title={t("admin.margin.form.interest_model")}>
          <div className="flex flex-col gap-3">
            <Segmented
              value={f.interest_model}
              onValueChange={(v) => setF((p) => ({ ...p, interest_model: v as Form["interest_model"] }))}
              items={[
                { value: "FIXED", label: t("admin.enum.rateModel.FIXED") },
                { value: "FLOATING", label: t("admin.enum.rateModel.FLOATING") },
              ]}
              aria-label={t("admin.margin.form.interest_model")}
            />
            {f.interest_model === "FIXED" ? (
              <Field label={t("admin.margin.form.fixed")} value={f.fixed} onChange={set("fixed")} error={errors.fixed} unit="%/h" />
            ) : (
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label={t("admin.margin.form.base")} value={f.base} onChange={set("base")} error={errors.base} unit="%/h" />
                <Field label={t("admin.margin.form.kink")} value={f.kink} onChange={set("kink")} error={errors.kink} unit="%" />
                <Field label={t("admin.margin.form.kink_rate")} value={f.kink_rate} onChange={set("kink_rate")} error={errors.kinkRate} unit="%/h" />
                <Field label={t("admin.margin.form.max")} value={f.max} onChange={set("max")} error={errors.max} unit="%/h" />
              </div>
            )}
            <p className="text-xs text-fg-3">{t("admin.margin.floatingHint")}</p>
          </div>
        </Card>
        <Field label={t("admin.margin.form.haircut")} value={f.haircut} onChange={set("haircut")} error={errors.haircut} hint={t("admin.margin.haircutHint")} />
        <div className="sticky bottom-0 -mx-6 mt-2 flex items-center justify-end gap-2 border-t border-line-1 bg-bg-1 px-6 py-3">
          <span className="mr-auto text-xs text-fg-3">{changes.length > 0 && t(approval ? "admin.margin.willAsk" : "admin.margin.willApply")}</span>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <DangerAction
            trigger={(open) => (
              <Button disabled={!ok || changes.length === 0} onClick={open} data-testid="margin-asset-save">
                {t(approval ? "admin.margin.ask" : "admin.margin.apply")}
              </Button>
            )}
            danger={approval}
            title={t(approval ? "admin.margin.askTitle" : "admin.margin.applyTitle", { target: a.asset })}
            description={approval ? t("admin.margin.askHint") : t("admin.margin.applyHint")}
            target={<Changes changes={changes} />}
            confirmWord={a.asset}
            run={async (reason) =>
              adminData(
                await adminApi.PUT("/admin/v1/margin/assets/{asset}", {
                  params: { path: { asset: a.asset } }, body: { ...fromForm(f), expected_version: a.version, reason },
                }),
              )
            }
            success={outcome}
            invalidate={changed}
            onDone={onClose}
          />
        </div>
      </div>
    </Drawer>
  );
}

function Pairs({ edit }: { edit: boolean }) {
  const { t } = useTranslation();
  const q = useMarginPairs();
  const settings = useMarginSettings();
  const [editing, setEditing] = useState<MarginPair | null>(null);
  const columns = useMemo<ColumnDef<MarginPair, unknown>[]>(
    () => [
      { accessorKey: "symbol", header: t("admin.common.symbol"), cell: ({ row }) => <span className="font-medium text-fg-1">{row.original.symbol}</span> },
      {
        id: "isolated", header: t("admin.margin.fields.isolated"),
        cell: ({ row }) => <Badge tone={row.original.isolated ? "success" : "neutral"}>{t(row.original.isolated ? "admin.launch.on" : "admin.launch.off")}</Badge>,
      },
      { id: "leverage", header: t("admin.margin.fields.isolatedLeverage"), cell: ({ row }) => <Badge tone="brand">{row.original.leverage}x</Badge> },
      {
        id: "levels", header: t("admin.margin.lines"), meta: right,
        cell: ({ row: { original: p } }) => (
          <span className="font-mono">
            {lineText(p.warn_level)} / {lineText(p.liquidation_level)}
          </span>
        ),
      },
      { id: "fee", header: t("admin.margin.fields.fee"), meta: right, cell: ({ row }) => <span className="font-mono">{formatPercent(row.original.liquidation_fee, 2, false)}</span> },
      { id: "accounts", header: t("admin.margin.isolatedAccounts"), meta: right, cell: ({ row }) => row.original.accounts },
      { id: "updated", header: t("admin.margin.updated"), cell: ({ row }) => <Updated v={row.original} /> },
      {
        id: "act", header: "", meta: right,
        cell: ({ row: { original: p } }) => (
          <span className="inline-flex items-center gap-2">
            <Pending id={p.pending_approval_id} />
            {edit && (
              <Button size="sm" variant="secondary" icon={<Pencil size={14} />} onClick={() => setEditing(p)} data-testid={`margin-pair-${p.symbol}`}>
                {t("admin.margin.edit")}
              </Button>
            )}
          </span>
        ),
      },
    ],
    [t, edit],
  );
  if (q.isError) return <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />;
  return (
    <>
      <DataTable columns={columns} data={q.data ?? []} getRowId={(p) => p.symbol} loading={q.isPending} density="compact" aria-label="margin-pairs" />
      {editing && settings.data && <PairDrawer pair={editing} defaults={settings.data.isolated_defaults} onClose={() => setEditing(null)} />}
    </>
  );
}

type Levels = MarginSettings["isolated_defaults"];

/** levelsError checks a pair of thresholds as typed: 1 < liquidation < warning (design §4.4). */
function useLevelsError() {
  const { t } = useTranslation();
  return (warn: string, liq: string) =>
    !isNumber(warn) || !isNumber(liq) ? t("admin.margin.badNumber") : dec.lte(liq, "1") || dec.gte(liq, warn) ? t("admin.margin.badThresholds") : undefined;
}

/** feeError checks a liquidation fee typed in percent: 0 to 10. */
function useFeeError() {
  const { t } = useTranslation();
  return (fee: string) => (!isNumber(fee) || dec.gt(fee, "10") ? t("admin.margin.badFee") : undefined);
}

/**
 * Suggested offers a leverage's suggested thresholds when the ones typed
 * differ from them: a new leverage never overwrites what is filled in or
 * stored (A57), the administrator takes the suggestion or not.
 */
function Suggested({ lev, levels, warn, liq, onUse }: { lev: number; levels?: { warn_level: string; liquidation_level: string }; warn: string;
  liq: string; onUse: (warn: string, liq: string) => void }) {
  const { t } = useTranslation();
  if (!levels || (isNumber(warn) && isNumber(liq) && dec.eq(warn, levels.warn_level) && dec.eq(liq, levels.liquidation_level))) return null;
  return (
    <p className="flex flex-wrap items-center gap-2 text-xs text-fg-3" data-testid="margin-suggested">
      {t("admin.margin.suggested", { lev, warn: lineText(levels.warn_level), liq: lineText(levels.liquidation_level) })}
      <Button size="sm" variant="ghost" onClick={() => onUse(levels.warn_level, levels.liquidation_level)}>
        {t("admin.margin.useSuggested")}
      </Button>
    </p>
  );
}

/**
 * PairDrawer changes a pair's isolated terms: switching isolated accounts
 * off applies at once; the leverage (with the design's thresholds for it
 * offered, not filled in), the thresholds, the fee and switching isolated
 * accounts on wait for a second ADMIN.
 */
function PairDrawer({ pair: p, defaults, onClose }: { pair: MarginPair; defaults: Levels; onClose: () => void }) {
  const { t } = useTranslation();
  const levelsError = useLevelsError();
  const feeError = useFeeError();
  const [isolated, setIsolated] = useState(p.isolated);
  const [lev, setLev] = useState<Leverage>(p.leverage);
  const [warn, setWarn] = useState(p.warn_level);
  const [liq, setLiq] = useState(p.liquidation_level);
  const [fee, setFee] = useState(toPercent(p.liquidation_fee));
  const errors = { levels: levelsError(warn, liq), fee: feeError(fee) };
  const changes: Change[] = [];
  const onOff = (b: boolean) => t(b ? "admin.launch.on" : "admin.launch.off");
  if (isolated !== p.isolated) changes.push({ label: t("admin.margin.fields.isolated"), from: onOff(p.isolated), to: onOff(isolated) });
  if (lev !== p.leverage) changes.push({ label: t("admin.margin.fields.isolatedLeverage"), from: `${p.leverage}x`, to: `${lev}x` });
  if (!errors.levels && (!dec.eq(warn, p.warn_level) || !dec.eq(liq, p.liquidation_level))) {
    changes.push({ label: t("admin.margin.lines"), from: `${lineText(p.warn_level)} / ${lineText(p.liquidation_level)}`, to: `${lineText(warn)} / ${lineText(liq)}` });
  }
  if (!errors.fee && !dec.eq(fromPercent(fee), p.liquidation_fee)) {
    changes.push({ label: t("admin.margin.fields.fee"), from: `${toPercent(p.liquidation_fee)}%`, to: `${fee.trim()}%` });
  }
  // Only isolated accounts switched off, and nothing else, applies at once.
  const approval = !(changes.length === 1 && p.isolated && !isolated);
  return (
    <Drawer open onOpenChange={(o) => !o && onClose()} title={t("admin.margin.params.editPair", { symbol: p.symbol })} description={t("admin.margin.params.pairHint")} width={560}>
      <div className="flex flex-col gap-4">
        <Switch checked={isolated} onCheckedChange={setIsolated} label={t("admin.margin.fields.isolated")} />
        <div className="flex items-center gap-3">
          <span className="text-sm text-fg-2">{t("admin.margin.fields.isolatedLeverage")}</span>
          <Segmented
            value={String(lev)}
            onValueChange={(v) => setLev(Number(v) as Leverage)}
            items={LEVERAGES.map((l) => ({ value: String(l), label: `${l}x` }))}
            aria-label={t("admin.margin.fields.isolatedLeverage")}
          />
        </div>
        <Suggested
          lev={lev}
          levels={defaults.find((x) => x.leverage === lev)}
          warn={warn}
          liq={liq}
          onUse={(w, l) => {
            setWarn(w);
            setLiq(l);
          }}
        />
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t("admin.margin.fields.warning")} value={warn} onChange={setWarn} error={errors.levels} />
          <Field label={t("admin.margin.fields.liquidation")} value={liq} onChange={setLiq} />
        </div>
        <Field label={t("admin.margin.fields.fee")} value={fee} onChange={setFee} error={errors.fee} unit="%" hint={t("admin.margin.feeHint")} />
        <div className="sticky bottom-0 -mx-6 mt-2 flex items-center justify-end gap-2 border-t border-line-1 bg-bg-1 px-6 py-3">
          <span className="mr-auto text-xs text-fg-3">{changes.length > 0 && t(approval ? "admin.margin.willAsk" : "admin.margin.willApply")}</span>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <DangerAction
            trigger={(open) => (
              <Button disabled={!!errors.levels || !!errors.fee || changes.length === 0} onClick={open} data-testid="margin-pair-save">
                {t(approval ? "admin.margin.ask" : "admin.margin.apply")}
              </Button>
            )}
            danger={approval}
            title={t(approval ? "admin.margin.askTitle" : "admin.margin.applyTitle", { target: p.symbol })}
            description={approval ? t("admin.margin.askHint") : t("admin.margin.pairOffHint")}
            target={<Changes changes={changes} />}
            confirmWord={p.base}
            run={async (reason) =>
              adminData(
                await adminApi.PUT("/admin/v1/margin/pairs/{symbol}", {
                  params: { path: { symbol: p.symbol } },
                  body: {
                    isolated, leverage: lev, warn_level: warn.trim(), liquidation_level: liq.trim(), liquidation_fee: fromPercent(fee),
                    expected_version: p.version, reason,
                  },
                }),
              )
            }
            success={outcome}
            invalidate={changed}
            onDone={onClose}
          />
        </div>
      </div>
    </Drawer>
  );
}

function Settings({ edit }: { edit: boolean }) {
  const { t } = useTranslation();
  const q = useMarginSettings();
  const [editing, setEditing] = useState(false);
  if (q.isError) return <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />;
  const s = q.data;
  if (!s) return <Skeleton className="h-40 w-full" />;
  return (
    <>
      <Card
        title={t("admin.margin.params.cross")}
        extra={
          <span className="flex items-center gap-2">
            <Pending id={s.pending_approval_id} />
            {edit && (
              <Button size="sm" variant="secondary" icon={<Pencil size={14} />} onClick={() => setEditing(true)} data-testid="margin-settings-edit">
                {t("admin.margin.edit")}
              </Button>
            )}
          </span>
        }
      >
        <div className="flex flex-col gap-3">
          <p className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-fg-2">
            <span>
              {t("admin.margin.fields.crossLeverage")} <Badge tone="brand">{s.cross.leverage}x</Badge>
            </span>
            <span>
              {t("admin.margin.lines")}{" "}
              <span className="font-mono">
                {lineText(s.cross.warn_level)} / {lineText(s.cross.liquidation_level)}
              </span>
            </span>
            <span>
              {t("admin.margin.fields.fee")} <span className="font-mono">{formatPercent(s.cross.liquidation_fee, 2, false)}</span>
            </span>
          </p>
          <span className="text-xs text-fg-3">
            <Updated v={s} />
          </span>
        </div>
      </Card>
      <Card title={t("admin.margin.params.defaults")}>
        <div className="flex flex-col gap-2">
          <table className="w-full text-sm" aria-label={t("admin.margin.params.defaults")}>
            <thead className="text-left text-xs text-fg-3">
              <tr>
                <th className="py-1 font-normal">{t("admin.margin.fields.isolatedLeverage")}</th>
                <th className="py-1 text-right font-normal">{t("admin.margin.fields.warning")}</th>
                <th className="py-1 text-right font-normal">{t("admin.margin.fields.liquidation")}</th>
              </tr>
            </thead>
            <tbody>
              {s.isolated_defaults.map((d) => (
                <tr key={d.leverage} className="border-t border-line-1">
                  <td className="py-1.5">
                    <Badge tone="brand">{d.leverage}x</Badge>
                  </td>
                  <td className="py-1.5 text-right font-mono">{lineText(d.warn_level)}</td>
                  <td className="py-1.5 text-right font-mono">{lineText(d.liquidation_level)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="text-xs text-fg-3">{t("admin.margin.params.defaultsHint")}</p>
        </div>
      </Card>
      {editing && <SettingsDrawer s={s} onClose={() => setEditing(false)} />}
    </>
  );
}

/** The cross account's suggested thresholds by leverage (design §4.4): 3x 1.30/1.10, 5x 1.20/1.10. */
const CROSS_LEVELS: Record<3 | 5, { warn_level: string; liquidation_level: string }> = {
  3: { warn_level: "1.3", liquidation_level: "1.1" },
  5: { warn_level: "1.2", liquidation_level: "1.1" },
};

function SettingsDrawer({ s, onClose }: { s: MarginSettings; onClose: () => void }) {
  const { t } = useTranslation();
  const levelsError = useLevelsError();
  const feeError = useFeeError();
  const [lev, setLev] = useState<3 | 5>(s.cross.leverage);
  const [warn, setWarn] = useState(s.cross.warn_level);
  const [liq, setLiq] = useState(s.cross.liquidation_level);
  const [fee, setFee] = useState(toPercent(s.cross.liquidation_fee));
  const errors = { levels: levelsError(warn, liq), fee: feeError(fee) };
  const changes: Change[] = [];
  if (lev !== s.cross.leverage) changes.push({ label: t("admin.margin.fields.crossLeverage"), from: `${s.cross.leverage}x`, to: `${lev}x` });
  if (!errors.levels && (!dec.eq(warn, s.cross.warn_level) || !dec.eq(liq, s.cross.liquidation_level))) {
    changes.push({
      label: t("admin.margin.lines"), from: `${lineText(s.cross.warn_level)} / ${lineText(s.cross.liquidation_level)}`, to: `${lineText(warn)} / ${lineText(liq)}`,
    });
  }
  if (!errors.fee && !dec.eq(fromPercent(fee), s.cross.liquidation_fee)) {
    changes.push({ label: t("admin.margin.fields.fee"), from: `${toPercent(s.cross.liquidation_fee)}%`, to: `${fee.trim()}%` });
  }
  return (
    <Drawer open onOpenChange={(o) => !o && onClose()} title={t("admin.margin.params.editSettings")} description={t("admin.margin.params.settingsHint")} width={560}>
      <div className="flex flex-col gap-4">
        <div className="flex items-center gap-3">
          <span className="text-sm text-fg-2">{t("admin.margin.fields.crossLeverage")}</span>
          <Segmented
            value={String(lev)}
            onValueChange={(v) => setLev(v === "5" ? 5 : 3)}
            items={[
              { value: "3", label: "3x" },
              { value: "5", label: "5x" },
            ]}
            aria-label={t("admin.margin.fields.crossLeverage")}
          />
        </div>
        <Suggested
          lev={lev}
          levels={CROSS_LEVELS[lev]}
          warn={warn}
          liq={liq}
          onUse={(w, l) => {
            setWarn(w);
            setLiq(l);
          }}
        />
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t("admin.margin.fields.warning")} value={warn} onChange={setWarn} error={errors.levels} />
          <Field label={t("admin.margin.fields.liquidation")} value={liq} onChange={setLiq} />
        </div>
        <Field label={t("admin.margin.fields.fee")} value={fee} onChange={setFee} error={errors.fee} unit="%" hint={t("admin.margin.feeHint")} />
        <div className="sticky bottom-0 -mx-6 mt-2 flex items-center justify-end gap-2 border-t border-line-1 bg-bg-1 px-6 py-3">
          <span className="mr-auto text-xs text-fg-3">{changes.length > 0 && t("admin.margin.willAsk")}</span>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <DangerAction
            trigger={(open) => (
              <Button disabled={!!errors.levels || !!errors.fee || changes.length === 0} onClick={open} data-testid="margin-settings-save">
                {t("admin.margin.ask")}
              </Button>
            )}
            title={t("admin.margin.askTitle", { target: t("admin.margin.params.cross") })}
            description={t("admin.margin.askHint")}
            target={<Changes changes={changes} />}
            confirmWord="margin"
            run={async (reason) =>
              adminData(
                await adminApi.PUT("/admin/v1/margin/settings", {
                  body: {
                    cross: { leverage: lev, warn_level: warn.trim(), liquidation_level: liq.trim(), liquidation_fee: fromPercent(fee) },
                    expected_version: s.version, reason,
                  },
                }),
              )
            }
            success={outcome}
            invalidate={changed}
            onDone={onClose}
          />
        </div>
      </div>
    </Drawer>
  );
}
