import { formatAmount } from "@exchange/core";
import { Select } from "@exchange/ui";
import { Info } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useEnum } from "./enums";
import { ALL, type FilterDef } from "./filters";

// The kinds of account (L1): HUMAN, a person; BOT, the simulated market's
// bots; TEST, the end-to-end scripts' accounts; SYSTEM, HOUSE and the
// like. Every user-dimension list filters by them, the humans by default:
// no kind in the address is the humans, as the server reads it.

export const KINDS = ["HUMAN", "BOT", "TEST", "SYSTEM"] as const;

/** MAX_KIND_IDS is how many accounts a kind filter carries to a list a service serves (the server's MaxKindIDs). */
export const MAX_KIND_IDS = 5000;

/** useKindOptions are the 类型 choices: 真人 (the default, the ALL sentinel: nothing in the address), each other kind, 全部 (ALL). */
function useKindOptions() {
  const { t } = useTranslation();
  const label = useEnum();
  return [
    { value: ALL, label: label("userKind", "HUMAN") },
    ...KINDS.filter((k) => k !== "HUMAN").map((k) => ({ value: k, label: label("userKind", k) })),
    { value: "ALL", label: t("admin.kinds.all") },
  ];
}

/** useKindFilter is a list's 类型 filter in its FilterBar. */
export function useKindFilter(): FilterDef {
  const { t } = useTranslation();
  const options = useKindOptions();
  return { key: "kind", label: t("admin.kinds.label"), kind: "select", width: 110, options };
}

/** KindSelect is the 类型 filter of a list without a FilterBar: "" is the humans. */
export function KindSelect({ value, onValueChange }: { value: string; onValueChange: (v: string) => void }) {
  const { t } = useTranslation();
  const options = useKindOptions();
  return (
    <Select
      size="sm"
      className="w-28"
      value={value || ALL}
      onValueChange={(v) => onValueChange(v === ALL ? "" : v)}
      options={options}
      aria-label={t("admin.kinds.label")}
    />
  );
}

/** kindParam is a list's kind as the API takes it: undefined (the humans) when none is chosen. */
export const kindParam = (v: string | undefined) => (v ? [v] : undefined);

/**
 * KindsNarrowed says a list's kind filter was cut short (kinds_narrowed):
 * past MAX_KIND_IDS accounts the humans' list leaves out the bots and
 * system accounts only, another kind's lists the first of them.
 */
export function KindsNarrowed({ narrowed, kind }: { narrowed: boolean | undefined; kind: string | undefined }) {
  const { t } = useTranslation();
  if (!narrowed) return null;
  return (
    <p role="note" data-testid="kinds-narrowed" className="flex items-center gap-1.5 text-xs text-warn-strong">
      <Info size={14} className="shrink-0" />
      {t(kind ? "admin.kinds.narrowed.kept" : "admin.kinds.narrowed.humans", { max: formatAmount(String(MAX_KIND_IDS), 0) })}
    </p>
  );
}
