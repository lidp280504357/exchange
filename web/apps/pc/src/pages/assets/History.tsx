import { dec, routes, timeZoneOf, useSettings } from "@exchange/core";
import { useLedger, type LedgerEntry } from "@exchange/core/assets/hooks";
import { dayRange, inRange, LEDGER_ENTRY_TYPES, ledgerRelation, pastRange, presetRange, type RangePreset } from "@exchange/core/assets/ledger";
import { sortAssets } from "@exchange/core/wallet/networks";
import {
  AmountText,
  Badge,
  Button,
  CoinIcon,
  Combobox,
  DataTable,
  EmptyArt,
  EmptyState,
  FormField,
  IconButton,
  Input,
  KeyValue,
  Segmented,
  Select,
  Spinner,
  TimeText,
  coinItems,
  type ColumnDef,
  type ComboboxItem,
  type DataColumnMeta,
} from "@exchange/ui";
import { ArrowRight, ChevronDown, Info, RotateCcw, X } from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useSearchParams } from "react-router";
import { AssetsLayout, Card } from "./parts/AssetsLayout";
import { codeLabel, shownDecimals, useAssetMeta, useTradeLinks, withQuery, type AssetMeta } from "./parts/meta";
import { MoreError } from "./parts/Notice";

type Preset = RangePreset | "custom";

const ALL_TYPES = "__all";
/** Pages fetched on their own while the time range still shows almost nothing (entries newer than it). */
const AUTO_PAGES = 20;

/**
 * History (design §6.2): the fund flow from the ledger, newest first, in a
 * virtual table that loads the next cursor page as it scrolls. Coin and
 * type filter on the server; the time range filters the pages here (the
 * API has none) and stops paging once past it. A row opens its entry's
 * details and where it comes from.
 */
export default function History() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const zone = useSettings((s) => timeZoneOf(s));
  const meta = useAssetMeta();
  const asset = (params.get("asset") ?? "").toUpperCase();
  const type = params.get("type") ?? "";
  const [preset, setPreset] = useState<Preset>("all");
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");
  const [selected, setSelected] = useState<string | null>(null);

  const range = useMemo(
    () => (preset === "custom" ? dayRange(fromDate, toDate, zone) : presetRange(preset)),
    [preset, fromDate, toDate, zone],
  );
  const ledger = useLedger({ asset, type });
  const pages = ledger.data?.pages ?? [];
  const loaded = useMemo(() => pages.flatMap((p) => p.items), [pages]);
  const rows = useMemo(() => loaded.filter((e) => inRange(e.posted_at, range)), [loaded, range]);
  const oldest = loaded[loaded.length - 1];
  const beyond = oldest ? pastRange(oldest.posted_at, range) : false;
  const hasMore = Boolean(ledger.hasNextPage) && !beyond;
  const { fetchNextPage, isFetchingNextPage } = ledger;

  // A range further back shows nothing on the first pages: fetch on until
  // it fills a screen (the table asks for more only when its rows grow).
  const scanning = hasMore && rows.length < 20 && pages.length < AUTO_PAGES;
  useEffect(() => {
    if (scanning && !isFetchingNextPage && ledger.isSuccess) void fetchNextPage();
  }, [scanning, isFetchingNextPage, ledger.isSuccess, fetchNextPage]);

  const setFilter = (key: "asset" | "type", value: string) =>
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev);
        if (value) p.set(key, value);
        else p.delete(key);
        return p;
      },
      { replace: true },
    );
  const reset = () => {
    setParams({}, { replace: true });
    setPreset("all");
    setFromDate("");
    setToDate("");
  };
  const filtered = asset !== "" || type !== "" || preset !== "all";
  const entry = rows.find((e) => e.id === selected) ?? null;

  const columns = useColumns(meta);

  return (
    <AssetsLayout title={t("pcAssets.history.title")} subtitle={t("pcAssets.history.subtitle")}>
      <Filters
        meta={meta}
        asset={asset}
        type={type}
        preset={preset}
        fromDate={fromDate}
        toDate={toDate}
        onAsset={(v) => setFilter("asset", v)}
        onType={(v) => setFilter("type", v)}
        onPreset={setPreset}
        onFromDate={setFromDate}
        onToDate={setToDate}
        onReset={filtered ? reset : undefined}
      />
      <div className="grid grid-cols-[minmax(0,1fr)_280px] items-start gap-4 xl:grid-cols-[minmax(0,1fr)_340px]">
        <Card bodyClassName="p-0">
          <DataTable
            aria-label={t("pcAssets.history.title")}
            columns={columns}
            data={rows}
            getRowId={(e) => e.id}
            loading={ledger.isPending}
            loadingRows={10}
            error={ledger.isError ? ledger.error : undefined}
            onRetry={() => void ledger.refetch()}
            virtual
            height="max(420px, calc(100dvh - 300px))"
            onEndReached={scanning ? undefined : () => void fetchNextPage()}
            loadingMore={isFetchingNextPage}
            hasMore={hasMore}
            onRowClick={(e) => setSelected((cur) => (cur === e.id ? null : e.id))}
            isRowActive={(e) => e.id === selected}
            empty={
              scanning || isFetchingNextPage ? (
                <p className="flex items-center justify-center gap-2 py-10 text-sm text-fg-3">
                  <Spinner size={14} /> {t("pcAssets.history.scanning")}
                </p>
              ) : filtered ? (
                <EmptyState
                  compact
                  title={t("pcAssets.history.emptyFiltered")}
                  description={t("pcAssets.history.emptyFilteredHint")}
                  action={
                    <Button size="sm" variant="secondary" onClick={reset}>
                      {t("pcAssets.history.reset")}
                    </Button>
                  }
                />
              ) : (
                <EmptyState
                  compact
                  title={t("pcAssets.history.empty")}
                  description={t("pcAssets.history.emptyHint")}
                  action={
                    <Button asChild size="sm">
                      <Link to={routes.deposit}>{t("nav.deposit")}</Link>
                    </Button>
                  }
                />
              )
            }
          />
          {ledger.isError && loaded.length > 0 && <MoreError error={ledger.error} onRetry={() => void fetchNextPage()} />}
          {hasMore && !ledger.isError && !isFetchingNextPage && !scanning && rows.length < 20 && (
            <div className="border-t border-line-1 p-2">
              <Button variant="ghost" block onClick={() => void fetchNextPage()}>
                {t("pcAssets.common.loadMore")}
              </Button>
            </div>
          )}
        </Card>
        <div className="sticky top-20">
          {entry ? (
            <EntryDetail entry={entry} loaded={loaded} meta={meta} onClose={() => setSelected(null)} />
          ) : (
            <Summary rows={rows} asset={asset} meta={meta} />
          )}
        </div>
      </div>
    </AssetsLayout>
  );
}

function useColumns(meta: AssetMeta): ColumnDef<LedgerEntry, any>[] {
  const { t } = useTranslation();
  return useMemo<ColumnDef<LedgerEntry, any>[]>(
    () => [
      {
        id: "time",
        header: t("common.time"),
        enableSorting: false,
        cell: ({ row }) => <TimeText value={row.original.posted_at} format="datetimeSeconds" className="text-fg-2" />,
        meta: { width: 150 } satisfies DataColumnMeta,
      },
      {
        id: "type",
        header: t("pcAssets.history.type"),
        enableSorting: false,
        cell: ({ row }) => (
          <span className="flex min-w-0 items-center gap-1.5">
            <span className="truncate">{codeLabel(row.original.entry_type, "entry")}</span>
            {row.original.account_type === "FUTURES" && <Badge tone="info">{codeLabel("FUTURES", "account")}</Badge>}
            {row.original.balance_kind === "FROZEN" && <Badge>{codeLabel("FROZEN")}</Badge>}
          </span>
        ),
      },
      {
        id: "asset",
        header: t("pcAssets.history.asset"),
        enableSorting: false,
        cell: ({ row }) => (
          <span className="flex items-center gap-2">
            <CoinIcon symbol={row.original.asset} size={18} />
            {row.original.asset}
          </span>
        ),
        meta: { width: 88 } satisfies DataColumnMeta,
      },
      {
        id: "amount",
        header: t("pcAssets.history.change"),
        enableSorting: false,
        cell: ({ row }) => (
          <AmountText value={row.original.amount} decimals={shownDecimals(meta.decimals(row.original.asset))} sign tone="auto" className="font-medium" />
        ),
        meta: { align: "right", width: 136 } satisfies DataColumnMeta,
      },
      {
        id: "available",
        header: t("pcAssets.history.availableAfter"),
        enableSorting: false,
        cell: ({ row }) => <AmountText value={row.original.available_after} decimals={shownDecimals(meta.decimals(row.original.asset))} className="text-fg-2" />,
        meta: { align: "right", width: 136 } satisfies DataColumnMeta,
      },
    ],
    [t, meta],
  );
}

function Filters({
  meta, asset, type, preset, fromDate, toDate, onAsset, onType, onPreset, onFromDate, onToDate, onReset,
}: {
  meta: AssetMeta;
  asset: string;
  type: string;
  preset: Preset;
  fromDate: string;
  toDate: string;
  onAsset: (v: string) => void;
  onType: (v: string) => void;
  onPreset: (v: Preset) => void;
  onFromDate: (v: string) => void;
  onToDate: (v: string) => void;
  onReset?: () => void;
}) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const coins: ComboboxItem[] = [
    { value: "", label: t("pcAssets.history.allAssets") },
    ...coinItems(sortAssets(meta.list.map((a) => a.asset_code)), locale).map((it) => ({ ...it, description: meta.name(it.value) })),
  ];
  return (
    <div className="mb-4 flex flex-wrap items-end gap-4 rounded-3 border border-line-1 bg-bg-1 p-4">
      <FormField label={t("pcAssets.history.asset")} className="w-44">
        {(control) => (
          <Combobox
            items={coins}
            value={asset}
            onValueChange={onAsset}
            searchPlaceholder={t("pcAssets.common.searchCoin")}
            aria-label={t("pcAssets.history.asset")}
            trigger={
              <button
                type="button"
                id={control.id}
                className="flex h-8 w-full items-center gap-2 rounded-1 border border-line-1 bg-bg-2 px-2.5 text-left text-sm transition-colors hover:border-line-2 focus-visible:ring-1 focus-visible:ring-brand"
              >
                {asset ? <CoinIcon symbol={asset} size={16} /> : null}
                <span className="truncate text-fg-1">{asset || t("pcAssets.history.allAssets")}</span>
                <ChevronDown size={14} className="ml-auto shrink-0 text-fg-3" />
              </button>
            }
          />
        )}
      </FormField>
      <FormField label={t("pcAssets.history.type")} className="w-48">
        {(control) => (
          <Select
            size="sm"
            id={control.id}
            value={type || ALL_TYPES}
            onValueChange={(v) => onType(v === ALL_TYPES ? "" : v)}
            aria-label={t("pcAssets.history.type")}
            className="w-full"
            options={[
              { value: ALL_TYPES, label: t("pcAssets.history.allTypes") },
              ...LEDGER_ENTRY_TYPES.map((c) => ({ value: c, label: codeLabel(c, "entry") })),
            ]}
          />
        )}
      </FormField>
      <FormField label={t("pcAssets.history.range")}>
        <Segmented
          value={preset}
          onValueChange={(v) => onPreset(v as Preset)}
          aria-label={t("pcAssets.history.range")}
          items={(["all", "7d", "30d", "90d", "custom"] as const).map((p) => ({ value: p, label: t(`pcAssets.history.ranges.${p}`) }))}
        />
      </FormField>
      {preset === "custom" && (
        <div className="flex items-end gap-2 animate-fade-in">
          <FormField label={t("pcAssets.history.from")} className="w-40">
            <Input size="sm" type="date" value={fromDate} max={toDate || undefined} onValueChange={onFromDate} />
          </FormField>
          <ArrowRight size={14} className="mb-2.5 text-fg-3" />
          <FormField label={t("pcAssets.history.to")} className="w-40">
            <Input size="sm" type="date" value={toDate} min={fromDate || undefined} onValueChange={onToDate} />
          </FormField>
        </div>
      )}
      {onReset && (
        <Button size="sm" variant="ghost" icon={<RotateCcw size={14} />} onClick={onReset} className="ml-auto">
          {t("pcAssets.history.reset")}
        </Button>
      )}
    </div>
  );
}

function Summary({ rows, asset, meta }: { rows: LedgerEntry[]; asset: string; meta: AssetMeta }) {
  const { t } = useTranslation();
  const totals = useMemo(() => {
    let inflow = "0";
    let outflow = "0";
    for (const e of rows) {
      if (e.balance_kind !== "AVAILABLE") continue;
      if (dec.sign(e.amount) > 0) inflow = dec.add(inflow, e.amount);
      else outflow = dec.add(outflow, e.amount);
    }
    return { inflow, outflow };
  }, [rows]);
  const places = shownDecimals(meta.decimals(asset));
  return (
    <Card title={t("pcAssets.history.detail")}>
      <div className="flex flex-col items-center gap-2 py-4 text-center">
        <EmptyArt size={72} />
        <p className="max-w-56 text-sm text-fg-3">{t("pcAssets.history.detailHint")}</p>
      </div>
      <div className="mt-2 border-t border-line-1 pt-4">
        <p className="text-xs text-fg-3">{t("pcAssets.history.loaded", { count: rows.length })}</p>
        {asset && (
          <KeyValue
            className="mt-3"
            density="compact"
            items={[
              { key: "in", label: t("pcAssets.history.inflow"), value: <AmountText value={totals.inflow} decimals={places} sign tone="auto" asset={asset} /> },
              { key: "out", label: t("pcAssets.history.outflow"), value: <AmountText value={totals.outflow} decimals={places} tone="auto" asset={asset} /> },
            ]}
          />
        )}
      </div>
    </Card>
  );
}

function EntryDetail({ entry, loaded, meta, onClose }: { entry: LedgerEntry; loaded: LedgerEntry[]; meta: AssetMeta; onClose: () => void }) {
  const { t } = useTranslation();
  const links = useTradeLinks();
  const places = shownDecimals(meta.decimals(entry.asset));
  const siblings = loaded.filter((e) => e.journal_id === entry.journal_id && e.id !== entry.id);
  const relation = ledgerRelation(entry.entry_type, entry.account_type);
  const related: { to: string; label: string } | null =
    relation === "deposit"
      ? { to: withQuery(routes.deposit, { asset: entry.asset }), label: t("pcAssets.history.open.deposit") }
      : relation === "withdraw"
        ? { to: `${withQuery(routes.withdraw, { asset: entry.asset })}#records`, label: t("pcAssets.history.open.withdraw") }
        : relation === "transfer"
          ? { to: withQuery(routes.transfer, { asset: entry.asset, from: entry.account_type }), label: t("pcAssets.history.open.transfer") }
          : relation === "spot"
            ? (() => {
                const to = links.spot(entry.asset);
                return to ? { to, label: t("pcAssets.history.open.spot") } : null;
              })()
            : relation === "futures"
              ? { to: links.futures(entry.asset), label: t("pcAssets.history.open.futures") }
              : relation === "margin"
                ? { to: routes.margin, label: t("pcAssets.history.open.margin") }
                : null;

  return (
    <Card
      title={t("pcAssets.history.detail")}
      extra={<IconButton icon={<X />} label={t("pcAssets.history.close")} size="xs" onClick={onClose} />}
      className="animate-fade-in"
    >
      <div className="flex items-center gap-3">
        <CoinIcon symbol={entry.asset} size={36} />
        <div className="min-w-0">
          <div className="text-lg font-semibold">
            <AmountText value={entry.amount} decimals={places} sign tone="auto" asset={entry.asset} />
          </div>
          <div className="text-xs text-fg-3">
            {codeLabel(entry.entry_type, "entry")} · {codeLabel(entry.account_type, "account")}
          </div>
        </div>
      </div>
      <KeyValue
        className="mt-4"
        density="compact"
        items={[
          { key: "time", label: t("common.time"), value: <TimeText value={entry.posted_at} format="datetimeSeconds" /> },
          { key: "kind", label: t("pcAssets.history.balanceKind"), value: codeLabel(entry.balance_kind) },
          { key: "available", label: t("pcAssets.history.availableAfter"), value: <AmountText value={entry.available_after} decimals={places} asset={entry.asset} /> },
          { key: "frozen", label: t("pcAssets.history.frozenAfter"), value: <AmountText value={entry.frozen_after} decimals={places} asset={entry.asset} /> },
          { key: "id", label: t("pcAssets.history.entryId"), value: entry.id, copy: true },
          { key: "journal", label: t("pcAssets.history.journal"), value: <span className="font-mono text-xs">{entry.journal_id}</span>, copy: entry.journal_id },
        ]}
      />
      {siblings.length > 0 && (
        <Block title={t("pcAssets.history.sameJournal")}>
          <ul className="flex flex-col gap-1.5">
            {siblings.map((s) => (
              <li key={s.id} className="flex items-center justify-between gap-2 text-xs">
                <span className="truncate text-fg-2">
                  {codeLabel(s.entry_type, "entry")} · {codeLabel(s.account_type, "account")}
                  {s.balance_kind === "FROZEN" && ` · ${codeLabel("FROZEN")}`}
                </span>
                <AmountText value={s.amount} decimals={shownDecimals(meta.decimals(s.asset))} sign tone="auto" asset={s.asset} />
              </li>
            ))}
          </ul>
        </Block>
      )}
      <Block title={t("pcAssets.history.related")}>
        {relation === "adjustment" ? (
          <p className="text-xs text-fg-3">{t("pcAssets.history.adjustment")}</p>
        ) : related ? (
          <>
            <Button asChild size="sm" variant="secondary" icon={<ArrowRight size={14} />}>
              <Link to={related.to}>{related.label}</Link>
            </Button>
            <p className="mt-2 flex items-start gap-1.5 text-xs text-fg-3">
              <Info size={12} className="mt-0.5 shrink-0" />
              {t("pcAssets.history.noRef")}
            </p>
          </>
        ) : (
          <p className="text-xs text-fg-3">—</p>
        )}
      </Block>
    </Card>
  );
}

function Block({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="mt-4 border-t border-line-1 pt-3">
      <div className="mb-2 text-xs font-medium text-fg-3">{title}</div>
      {children}
    </div>
  );
}
