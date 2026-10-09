import { Button, Dialog, DropdownMenu, Input, Select, type MenuEntry } from "@exchange/ui";
import { Bookmark, RotateCcw, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";

// A list's filters (design §10.2) live in the address, so a filtered list
// can be shared and the back button restores it; named views of filters
// are saved per page in this browser.

export type FilterDef =
  | { key: string; label: string; kind: "text"; placeholder?: string; width?: number }
  | { key: string; label: string; kind: "select"; options: { value: string; label: string }[]; width?: number }
  | { key: string; label: string; kind: "date"; width?: number };

/** ALL is the select option that clears a filter (Radix Select has no empty value). */
export const ALL = "__all";

/** useFilters reads and writes a page's filters in the address. */
export function useFilters(keys: readonly string[]) {
  const [params, setParams] = useSearchParams();
  const values: Record<string, string> = {};
  for (const k of keys) values[k] = params.get(k) ?? "";
  const set = (patch: Record<string, string>) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        for (const [k, v] of Object.entries(patch)) {
          if (v && v !== ALL) next.set(k, v);
          else next.delete(k);
        }
        return next;
      },
      { replace: true },
    );
  const reset = () => set(Object.fromEntries(keys.map((k) => [k, ""])));
  return { values, set, reset };
}

/** dayStart and dayEnd turn a yyyy-mm-dd filter into an RFC 3339 bound (UTC). */
export const dayStart = (d: string) => (d ? `${d}T00:00:00Z` : undefined);
export const dayEnd = (d: string) => {
  if (!d) return undefined;
  const t = new Date(`${d}T00:00:00Z`);
  t.setUTCDate(t.getUTCDate() + 1);
  return t.toISOString();
};

type SavedView = { name: string; values: Record<string, string> };

function loadViews(page: string): SavedView[] {
  try {
    return JSON.parse(localStorage.getItem(`admin.views.${page}`) ?? "[]") as SavedView[];
  } catch {
    return [];
  }
}

export type FilterBarProps = {
  /** The page, for its saved views. */
  page: string;
  defs: FilterDef[];
  filters: ReturnType<typeof useFilters>;
  /** Actions at the right end (export). */
  extra?: React.ReactNode;
  /**
   * Filters the page sets elsewhere (the users list's search box, A93):
   * reset, saved and restored with the bar's own.
   */
  extraKeys?: readonly string[];
};

export function FilterBar({ page, defs, filters, extra, extraKeys = [] }: FilterBarProps) {
  const { t } = useTranslation();
  const [views, setViews] = useState(() => loadViews(page));
  const [naming, setNaming] = useState(false);
  const [name, setName] = useState("");
  const store = (next: SavedView[]) => {
    setViews(next);
    localStorage.setItem(`admin.views.${page}`, JSON.stringify(next));
  };
  const keys = [...defs.map((d) => d.key), ...extraKeys];
  const active = keys.some((k) => filters.values[k]);
  const menu: MenuEntry[] = [
    ...views.map((v) => ({ key: `view-${v.name}`, label: v.name, onSelect: () => filters.set({ ...Object.fromEntries(keys.map((k) => [k, ""])), ...v.values }) })),
    ...(views.length ? [{ type: "separator" as const, key: "sep" }] : []),
    { key: "save", label: t("admin.common.saveView"), icon: <Bookmark size={14} />, disabled: !active, onSelect: () => setNaming(true) },
    ...views.map((v) => ({
      key: `del-${v.name}`,
      label: t("admin.common.deleteView", { name: v.name }),
      icon: <Trash2 size={14} />,
      danger: true,
      onSelect: () => store(views.filter((x) => x.name !== v.name)),
    })),
  ];
  return (
    <div className="flex flex-wrap items-end gap-2">
      {defs.map((d) => (
        <Field key={d.key} def={d} value={filters.values[d.key] ?? ""} onChange={(v) => filters.set({ [d.key]: v })} />
      ))}
      <Button size="sm" variant="ghost" icon={<RotateCcw size={14} />} disabled={!active} onClick={() => filters.set(Object.fromEntries(keys.map((k) => [k, ""])))}>
        {t("admin.common.reset")}
      </Button>
      <DropdownMenu
        align="start"
        trigger={
          <Button size="sm" variant="ghost" icon={<Bookmark size={14} />}>
            {t("admin.common.views")}
          </Button>
        }
        items={menu}
      />
      <div className="ml-auto flex items-center gap-2">{extra}</div>
      <Dialog
        open={naming}
        onOpenChange={setNaming}
        title={t("admin.common.saveView")}
        size="sm"
        confirmText={t("admin.common.save")}
        confirmDisabled={!name.trim()}
        onConfirm={() => {
          const values = Object.fromEntries(keys.map((k) => [k, filters.values[k] ?? ""]).filter(([, v]) => v));
          store([...views.filter((v) => v.name !== name.trim()), { name: name.trim(), values }]);
          setName("");
          setNaming(false);
        }}
      >
        <Input size="sm" autoFocus value={name} onValueChange={setName} placeholder={t("admin.common.viewName")} maxLength={40} />
      </Dialog>
    </div>
  );
}

function Field({ def, value, onChange }: { def: FilterDef; value: string; onChange: (v: string) => void }) {
  const width = def.width ?? (def.kind === "select" ? 140 : def.kind === "date" ? 150 : 220);
  return (
    <label className="flex flex-col gap-1 text-xs text-fg-3" style={{ width }}>
      {def.label}
      {def.kind === "select" ? (
        <Select size="sm" value={value || ALL} onValueChange={onChange} options={def.options} aria-label={def.label} />
      ) : def.kind === "date" ? (
        <Input size="sm" type="date" value={value} onValueChange={onChange} aria-label={def.label} />
      ) : (
        <TextFilter value={value} onChange={onChange} placeholder={def.placeholder} label={def.label} />
      )}
    </label>
  );
}

/** TextFilter commits 300 ms after typing stops, or at once on Enter, not on every key (A5, A93). */
function TextFilter({ value, onChange, placeholder, label }: { value: string; onChange: (v: string) => void; placeholder?: string; label: string }) {
  const [draft, setDraft] = useState(value);
  const commit = useRef(onChange);
  commit.current = onChange;
  useEffect(() => setDraft(value), [value]);
  useEffect(() => {
    if (draft.trim() === value) return;
    const id = setTimeout(() => commit.current(draft.trim()), 300);
    return () => clearTimeout(id);
  }, [draft, value]);
  return (
    <Input
      size="sm"
      value={draft}
      onValueChange={setDraft}
      onKeyDown={(e) => e.key === "Enter" && onChange(draft.trim())}
      placeholder={placeholder}
      clearable
      onClear={() => onChange("")}
      aria-label={label}
    />
  );
}

/** options builds select options of codes with an "all" entry first. */
export function options(all: string, codes: readonly string[], label: (code: string) => string) {
  return [{ value: ALL, label: all }, ...codes.map((c) => ({ value: c, label: label(c) }))];
}
