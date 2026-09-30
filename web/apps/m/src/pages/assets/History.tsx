import { enumLabel, errorText, formatTime, routes, timeZoneOf, useSettings } from "@exchange/core";
import { useLedger, type LedgerEntry } from "@exchange/core/assets/hooks";
import { dayRange, inRange, LEDGER_ENTRY_TYPES, ledgerRelation, pastRange, presetRange, type RangePreset } from "@exchange/core/assets/ledger";
import { sortAssets } from "@exchange/core/wallet/networks";
import { AmountText, Badge, Button, CoinIcon, EmptyState, ErrorState, FormField, Input, KeyValue, Sheet, Skeleton, Spinner, TimeText, cn } from "@exchange/ui";
import { ArrowRight, ChevronRight, Info, SlidersHorizontal, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useSearchParams } from "react-router";
import { usePageHeader } from "../../layout/header";
import { AddressValue, Appear, PRESS, RETRY, TextButton, useKept } from "./parts/bits";
import { CoinSheet } from "./parts/Coins";
import { filterCount, groupByDay, ledgerTotals } from "./parts/logic";
import { shownDecimals, useAssetMeta, useTradeLinks, withQuery, type AssetMeta } from "./parts/meta";
import { MoreError } from "./parts/Notice";
import { PullToRefresh } from "../../components/PullToRefresh";

type Preset = RangePreset | "custom";
type Filters = { asset: string; type: string; preset: Preset; fromDate: string; toDate: string };

const PRESETS: Preset[] = ["all", "7d", "30d", "90d", "custom"];
/** Pages fetched on their own while the time range still shows almost nothing (entries newer than it). */
const AUTO_PAGES = 20;
/** Fewer rows than this do not fill a phone screen. */
const SCREEN_ROWS = 20;

/**
 * History (design §7.2, §6.2): the fund flow from the ledger, newest first,
 * grouped by day and loaded page by page as it scrolls. Coin and type
 * filter on the server; the time range filters the pages here (the API
 * has none) and stops paging once past it. The filters sit in a sheet
 * behind the header's button; an entry opens its details in a sheet.
 */
export default function History() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const zone = useSettings((s) => timeZoneOf(s));
  const locale = useSettings((s) => s.locale);
  const meta = useAssetMeta();
  const asset = (params.get("asset") ?? "").toUpperCase();
  const type = params.get("type") ?? "";
  const [preset, setPreset] = useState<Preset>("all");
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");
  const [filtering, setFiltering] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);

  const count = filterCount({ asset, type, range: preset });
  const title = t("nav.history");
  const filterLabel = t("mAssets.history.filters");
  usePageHeader(
    { title, back: routes.assets, right: <FilterButton count={count} label={filterLabel} onClick={() => setFiltering(true)} /> },
    [title, count, filterLabel],
  );

  const range = useMemo(
    () => (preset === "custom" ? dayRange(fromDate, toDate, zone) : presetRange(preset)),
    [preset, fromDate, toDate, zone],
  );
  const ledger = useLedger({ asset, type });
  const pages = ledger.data?.pages;
  const loaded = useMemo(() => (pages ?? []).flatMap((p) => p.items), [pages]);
  const rows = useMemo(() => loaded.filter((e) => inRange(e.posted_at, range)), [loaded, range]);
  const oldest = loaded[loaded.length - 1];
  const beyond = oldest ? pastRange(oldest.posted_at, range) : false;
  const hasMore = Boolean(ledger.hasNextPage) && !beyond;
  const { fetchNextPage, isFetchingNextPage } = ledger;

  // A range further back shows nothing on the first pages: fetch on until
  // it fills a screen (scrolling asks for more only once rows show).
  const scanning = hasMore && rows.length < SCREEN_ROWS && (pages?.length ?? 0) < AUTO_PAGES;
  useEffect(() => {
    if (scanning && !isFetchingNextPage && ledger.isSuccess) void fetchNextPage();
  }, [scanning, isFetchingNextPage, ledger.isSuccess, fetchNextPage]);
  const idle = hasMore && !scanning && !isFetchingNextPage && !ledger.isError;

  const groups = useMemo(() => groupByDay(rows, (e) => formatTime(e.posted_at, "date", locale, zone)), [rows, locale, zone]);
  const totals = useMemo(() => ledgerTotals(rows), [rows]);
  const kept = useKept(selected);
  const entry = loaded.find((e) => e.id === kept) ?? null;

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
  const clearRange = () => {
    setPreset("all");
    setFromDate("");
    setToDate("");
  };
  const apply = (f: Filters) => {
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev);
        for (const [key, value] of [
          ["asset", f.asset],
          ["type", f.type],
        ] as const) {
          if (value) p.set(key, value);
          else p.delete(key);
        }
        return p;
      },
      { replace: true },
    );
    setPreset(f.preset);
    setFromDate(f.fromDate);
    setToDate(f.toDate);
  };
  const reset = () => {
    setParams({}, { replace: true });
    clearRange();
  };
  const rangeLabel = preset === "custom" ? `${fromDate || "…"} ~ ${toDate || "…"}` : t(`mAssets.history.ranges.${preset}`);
  const places = shownDecimals(meta.decimals(asset));

  let body: ReactNode;
  if (ledger.isPending) body = <RowsSkeleton />;
  else if (ledger.isError && loaded.length === 0) {
    body = <ErrorState compact message={errorText(ledger.error)} onRetry={() => void ledger.refetch()} className={RETRY} />;
  } else if (rows.length === 0) {
    body =
      scanning || isFetchingNextPage ? (
        <p className="flex items-center justify-center gap-2 py-10 text-sm text-fg-3">
          <Spinner size={14} /> {t("mAssets.history.scanning")}
        </p>
      ) : count > 0 ? (
        <EmptyState
          compact
          title={t("mAssets.history.emptyFiltered")}
          description={t("mAssets.history.emptyFilteredHint")}
          action={
            <Button variant="secondary" className="h-11" onClick={reset}>
              {t("mAssets.history.reset")}
            </Button>
          }
        />
      ) : (
        <EmptyState
          compact
          title={t("mAssets.history.empty")}
          description={t("mAssets.history.emptyHint")}
          action={
            <Button asChild className="h-11">
              <Link to={routes.deposit}>{t("nav.deposit")}</Link>
            </Button>
          }
        />
      );
  } else {
    body = <Groups groups={groups} meta={meta} onOpen={setSelected} />;
  }

  return (
    <PullToRefresh onRefresh={() => ledger.refetch()}>
      <div className="flex flex-col gap-3 px-4 pb-6 pt-2">
        {count > 0 && (
          <div className="flex flex-wrap items-center gap-x-2">
            {asset && <Chip label={asset} onRemove={() => setFilter("asset", "")} />}
            {type && <Chip label={enumLabel(type, "entry")} onRemove={() => setFilter("type", "")} />}
            {preset !== "all" && <Chip label={rangeLabel} onRemove={clearRange} />}
            <TextButton onClick={reset} className="ml-auto -mr-3">
              {t("mAssets.history.reset")}
            </TextButton>
          </div>
        )}
        {asset && rows.length > 0 && (
          <div className="grid grid-cols-2 gap-x-3 gap-y-2 rounded-3 bg-bg-1 p-3 text-xs">
            <span className="flex min-w-0 flex-col">
              <span className="text-fg-3">{t("mAssets.history.inflow")}</span>
              <AmountText value={totals.inflow} decimals={places} sign tone="auto" asset={asset} className="break-all text-sm font-medium" />
            </span>
            <span className="flex min-w-0 flex-col items-end text-right">
              <span className="text-fg-3">{t("mAssets.history.outflow")}</span>
              <AmountText value={totals.outflow} decimals={places} tone="auto" asset={asset} className="break-all text-sm font-medium" />
            </span>
            <span className="col-span-2 text-fg-3">{t("mAssets.history.loaded", { count: rows.length })}</span>
          </div>
        )}
        {body}
        {rows.length > 0 && (
          <>
            {ledger.isError && <MoreError error={ledger.error} onRetry={() => void (ledger.isFetchNextPageError ? fetchNextPage() : ledger.refetch())} />}
            <Sentinel enabled={idle && rows.length >= SCREEN_ROWS} onVisible={() => void fetchNextPage()} />
            {idle && rows.length < SCREEN_ROWS && (
              <Button variant="ghost" size="lg" block onClick={() => void fetchNextPage()}>
                {t("mAssets.common.loadMore")}
              </Button>
            )}
            {isFetchingNextPage && (
              <p className="flex items-center justify-center gap-2 py-3 text-sm text-fg-3">
                <Spinner size={14} /> {t("common.loading")}
              </p>
            )}
            {!hasMore && !ledger.isError && <p className="py-3 text-center text-xs text-fg-3">{t("mAssets.history.end")}</p>}
          </>
        )}
      </div>
      <FilterSheet
        open={filtering}
        onOpenChange={setFiltering}
        value={{ asset, type, preset, fromDate, toDate }}
        onApply={apply}
        meta={meta}
      />
      <EntrySheet open={selected !== null && entry !== null} entry={entry} loaded={loaded} meta={meta} onClose={() => setSelected(null)} />
    </PullToRefresh>
  );
}

/** FilterButton opens the filters from the header, with the number of filters on. */
function FilterButton({ count, label, onClick }: { count: number; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-haspopup="dialog"
      aria-label={count > 0 ? `${label} (${count})` : label}
      className="relative grid size-11 place-items-center text-fg-1 active:text-brand"
    >
      <SlidersHorizontal size={20} />
      {count > 0 && (
        <span aria-hidden className="absolute right-1 top-1.5 grid h-4 min-w-4 place-items-center rounded-full bg-brand px-1 text-xs font-semibold leading-none text-brand-fg">
          {count}
        </span>
      )}
    </button>
  );
}

/** Chip is a filter in force; tapping it removes the filter. */
function Chip({ label, onRemove }: { label: string; onRemove: () => void }) {
  const { t } = useTranslation();
  return (
    <button type="button" onClick={onRemove} aria-label={t("mAssets.history.removeFilter", { name: label })} className="flex h-11 items-center">
      <span className="flex h-8 items-center gap-1 rounded-full border border-line-2 bg-bg-1 pl-3 pr-2 text-xs text-fg-1">
        {label}
        <X size={12} className="text-fg-3" />
      </span>
    </button>
  );
}

/**
 * Sentinel asks for the next page when it comes within 400 px of the
 * screen (an observer is set up again every time it is enabled, so a list
 * still too short after a page keeps loading).
 */
function Sentinel({ enabled, onVisible }: { enabled: boolean; onVisible: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const callback = useRef(onVisible);
  useEffect(() => {
    callback.current = onVisible;
  }, [onVisible]);
  useEffect(() => {
    const el = ref.current;
    if (!enabled || !el || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver((seen) => {
      if (seen.some((s) => s.isIntersecting)) callback.current();
    }, { rootMargin: "400px 0px" });
    io.observe(el);
    return () => io.disconnect();
  }, [enabled]);
  return <div ref={ref} aria-hidden className="h-px" />;
}

function RowsSkeleton() {
  return (
    <div aria-hidden className="flex flex-col gap-2">
      <Skeleton className="ml-1 h-3 w-24" />
      <div className="flex flex-col divide-y divide-line-1 rounded-3 bg-bg-1">
        {Array.from({ length: 6 }, (_, i) => (
          <div key={i} className="flex items-center gap-3 px-3 py-3">
            <Skeleton round className="size-7" />
            <div className="flex flex-1 flex-col gap-1.5">
              <Skeleton className="h-3.5 w-24" />
              <Skeleton className="h-3 w-16" />
            </div>
            <div className="flex flex-col items-end gap-1.5">
              <Skeleton className="h-3.5 w-20" />
              <Skeleton className="h-3 w-14" />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function Groups({ groups, meta, onOpen }: { groups: { day: string; items: LedgerEntry[] }[]; meta: AssetMeta; onOpen: (id: string) => void }) {
  let index = 0;
  return (
    <div className="flex flex-col gap-3">
      {groups.map((g) => (
        <section key={g.day} aria-label={g.day}>
          <h3 className="px-1 pb-1.5 text-xs font-medium tabular-nums text-fg-3">{g.day}</h3>
          <ul className="divide-y divide-line-1 overflow-hidden rounded-3 bg-bg-1">
            {g.items.map((e) => (
              // Rows off screen skip layout and paint until they come near it.
              <Appear key={e.id} index={index++} className="[contain-intrinsic-size:auto_64px] [content-visibility:auto]">
                <EntryRow e={e} meta={meta} onOpen={() => onOpen(e.id)} />
              </Appear>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

function EntryRow({ e, meta, onOpen }: { e: LedgerEntry; meta: AssetMeta; onOpen: () => void }) {
  const { t } = useTranslation();
  const places = shownDecimals(meta.decimals(e.asset));
  return (
    <button type="button" onClick={onOpen} className="flex min-h-16 w-full items-center gap-3 px-3 py-2.5 text-left active:bg-bg-2">
      <CoinIcon symbol={e.asset} size={28} />
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="truncate text-sm font-medium text-fg-1">{enumLabel(e.entry_type, "entry")}</span>
          {e.account_type === "FUTURES" && <Badge tone="info">{enumLabel("FUTURES", "account")}</Badge>}
          {e.balance_kind === "FROZEN" && <Badge>{enumLabel("FROZEN")}</Badge>}
        </span>
        <TimeText value={e.posted_at} format="timeSeconds" className="block text-xs text-fg-3" />
      </span>
      <span className="flex max-w-[55%] shrink-0 flex-col items-end text-right">
        <AmountText value={e.amount} decimals={places} sign tone="auto" asset={e.asset} className="break-all text-sm font-medium" />
        <span className="break-all text-xs text-fg-3">
          {t("mAssets.history.availableShort")} <AmountText value={e.available_after} decimals={places} />
        </span>
      </span>
    </button>
  );
}

function Group({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section aria-label={title}>
      <h3 className="mb-2 text-sm font-medium text-fg-2">{title}</h3>
      {children}
    </section>
  );
}

/** Chips is a single choice among pills that wrap (the type and the time range). */
function Chips({ options, value, onChange, label }: { options: { value: string; label: string }[]; value: string; onChange: (v: string) => void; label: string }) {
  return (
    <div role="radiogroup" aria-label={label} className="flex flex-wrap gap-2">
      {options.map((o) => {
        const on = o.value === value;
        return (
          <button
            key={o.value || "__all"}
            type="button"
            role="radio"
            aria-checked={on}
            onClick={() => onChange(o.value)}
            className={cn(
              "min-h-11 rounded-full border px-4 text-sm transition-colors duration-[var(--t-fast)]",
              on ? "border-brand bg-brand-soft text-brand" : "border-line-1 bg-bg-2 text-fg-2 active:bg-bg-3",
            )}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

const NONE: Filters = { asset: "", type: "", preset: "all", fromDate: "", toDate: "" };

/**
 * FilterSheet edits a draft of the filters (coin, type, time range) and
 * applies it at once; each opening starts from the filters in force.
 */
function FilterSheet({
  open, onOpenChange, value, onApply, meta,
}: { open: boolean; onOpenChange: (open: boolean) => void; value: Filters; onApply: (f: Filters) => void; meta: AssetMeta }) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState<Filters>(value);
  const [wasOpen, setWasOpen] = useState(open);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) setDraft(value);
  }
  const [coinOpen, setCoinOpen] = useState(false);
  const coins = useMemo(() => sortAssets(meta.list.map((a) => a.asset_code)), [meta.list]);
  const patch = (p: Partial<Filters>) => setDraft((d) => ({ ...d, ...p }));
  const badRange = draft.preset === "custom" && draft.fromDate !== "" && draft.toDate !== "" && draft.fromDate > draft.toDate;

  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      title={t("mAssets.history.filters")}
      closeButton
      footer={
        <div className="grid grid-cols-2 gap-2">
          <Button size="lg" variant="secondary" onClick={() => setDraft(NONE)}>
            {t("mAssets.history.clear")}
          </Button>
          <Button
            size="lg"
            disabled={badRange}
            onClick={() => {
              onApply(draft);
              onOpenChange(false);
            }}
          >
            {t("common.apply")}
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-5 pt-1">
        <Group title={t("mAssets.history.asset")}>
          <button
            type="button"
            onClick={() => setCoinOpen(true)}
            aria-haspopup="dialog"
            className={cn("flex min-h-12 w-full items-center gap-3 rounded-2 border border-line-1 bg-bg-2 px-3 text-left", PRESS)}
          >
            {draft.asset ? (
              <>
                <CoinIcon symbol={draft.asset} size={24} />
                <span className="font-medium text-fg-1">{draft.asset}</span>
                <span className="min-w-0 truncate text-sm text-fg-3">{meta.name(draft.asset)}</span>
              </>
            ) : (
              <span className="text-fg-1">{t("mAssets.history.allAssets")}</span>
            )}
            <ChevronRight size={16} className="ml-auto shrink-0 text-fg-3" />
          </button>
        </Group>
        <Group title={t("mAssets.history.type")}>
          <Chips
            label={t("mAssets.history.type")}
            value={draft.type}
            onChange={(v) => patch({ type: v })}
            options={[{ value: "", label: t("mAssets.history.allTypes") }, ...LEDGER_ENTRY_TYPES.map((c) => ({ value: c, label: enumLabel(c, "entry") }))]}
          />
        </Group>
        <Group title={t("mAssets.history.range")}>
          <Chips
            label={t("mAssets.history.range")}
            value={draft.preset}
            onChange={(v) => patch({ preset: v as Preset })}
            options={PRESETS.map((p) => ({ value: p, label: t(`mAssets.history.ranges.${p}`) }))}
          />
          {draft.preset === "custom" && (
            <div className="mt-3 flex flex-col gap-3 animate-fade-in">
              <FormField label={t("mAssets.history.from")}>
                <Input size="lg" type="date" value={draft.fromDate} max={draft.toDate || undefined} onValueChange={(v) => patch({ fromDate: v })} />
              </FormField>
              <FormField label={t("mAssets.history.to")} error={badRange ? t("mAssets.history.badRange") : undefined}>
                <Input size="lg" type="date" value={draft.toDate} min={draft.fromDate || undefined} onValueChange={(v) => patch({ toDate: v })} />
              </FormField>
            </div>
          )}
        </Group>
      </div>
      <CoinSheet
        open={coinOpen}
        onOpenChange={setCoinOpen}
        title={t("mAssets.history.pickAsset")}
        coins={coins}
        value={draft.asset}
        onPick={(a) => patch({ asset: a })}
        name={meta.name}
        allLabel={t("mAssets.history.allAssets")}
      />
    </Sheet>
  );
}

function EntrySheet({
  open, entry, loaded, meta, onClose,
}: { open: boolean; entry: LedgerEntry | null; loaded: LedgerEntry[]; meta: AssetMeta; onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()} title={t("mAssets.history.detail")} closeButton>
      {entry && <EntryDetail entry={entry} loaded={loaded} meta={meta} />}
    </Sheet>
  );
}

function EntryDetail({ entry, loaded, meta }: { entry: LedgerEntry; loaded: LedgerEntry[]; meta: AssetMeta }) {
  const { t } = useTranslation();
  const links = useTradeLinks();
  const places = shownDecimals(meta.decimals(entry.asset));
  const siblings = loaded.filter((e) => e.journal_id === entry.journal_id && e.id !== entry.id);
  const relation = ledgerRelation(entry.entry_type, entry.account_type);
  const spot = relation === "spot" ? links.spot(entry.asset) : null;
  const related: { to: string; label: string } | null =
    relation === "deposit"
      ? { to: withQuery(routes.deposit, { asset: entry.asset }), label: t("mAssets.history.open.deposit") }
      : relation === "withdraw"
        ? { to: `${withQuery(routes.withdraw, { asset: entry.asset })}#records`, label: t("mAssets.history.open.withdraw") }
        : relation === "transfer"
          ? { to: withQuery(routes.transfer, { asset: entry.asset, from: entry.account_type }), label: t("mAssets.history.open.transfer") }
          : relation === "spot" && spot
            ? { to: spot, label: t("mAssets.history.open.spot") }
            : relation === "futures"
              ? { to: links.futures(entry.asset), label: t("mAssets.history.open.futures") }
              : null;

  return (
    <div className="flex flex-col gap-4 pt-1">
      <div className="flex items-center gap-3">
        <CoinIcon symbol={entry.asset} size={40} />
        <div className="min-w-0">
          <AmountText value={entry.amount} decimals={places} sign tone="auto" asset={entry.asset} className="block break-all text-lg font-semibold" />
          <div className="text-xs text-fg-3">
            {enumLabel(entry.entry_type, "entry")} · {enumLabel(entry.account_type, "account")}
          </div>
        </div>
      </div>
      <KeyValue
        items={[
          { key: "time", label: t("common.time"), value: <TimeText value={entry.posted_at} format="datetimeSeconds" /> },
          { key: "kind", label: t("mAssets.history.balanceKind"), value: enumLabel(entry.balance_kind) },
          { key: "available", label: t("mAssets.history.availableAfter"), value: <AmountText value={entry.available_after} decimals={places} asset={entry.asset} /> },
          { key: "frozen", label: t("mAssets.history.frozenAfter"), value: <AmountText value={entry.frozen_after} decimals={places} asset={entry.asset} /> },
          { key: "id", label: t("mAssets.history.entryId"), value: <AddressValue address={entry.id} head={8} tail={6} /> },
          { key: "journal", label: t("mAssets.history.journal"), value: <AddressValue address={entry.journal_id} head={8} tail={6} /> },
        ]}
      />
      {siblings.length > 0 && (
        <Block title={t("mAssets.history.sameJournal")}>
          <ul className="flex flex-col gap-2">
            {siblings.map((s) => (
              <li key={s.id} className="flex items-start justify-between gap-3 text-xs">
                <span className="min-w-0 text-fg-2">
                  {enumLabel(s.entry_type, "entry")} · {enumLabel(s.account_type, "account")}
                  {s.balance_kind === "FROZEN" && ` · ${enumLabel("FROZEN")}`}
                </span>
                <AmountText value={s.amount} decimals={shownDecimals(meta.decimals(s.asset))} sign tone="auto" asset={s.asset} className="shrink-0 text-right" />
              </li>
            ))}
          </ul>
        </Block>
      )}
      <Block title={t("mAssets.history.related")}>
        {relation === "adjustment" ? (
          <p className="text-xs text-fg-3">{t("mAssets.history.adjustment")}</p>
        ) : related ? (
          <>
            <Button asChild size="lg" variant="secondary" block icon={<ArrowRight size={16} />}>
              <Link to={related.to}>{related.label}</Link>
            </Button>
            <p className="mt-2 flex items-start gap-1.5 text-xs text-fg-3">
              <Info size={12} className="mt-0.5 shrink-0" />
              {t("mAssets.history.noRef")}
            </p>
          </>
        ) : (
          <p className="text-xs text-fg-3">—</p>
        )}
      </Block>
    </div>
  );
}

function Block({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="border-t border-line-1 pt-3">
      <div className="mb-2 text-xs font-medium text-fg-3">{title}</div>
      {children}
    </div>
  );
}
