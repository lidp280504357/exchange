import { useTranslation } from "react-i18next";
import { useEnum } from "./enums";
import { ALL, type FilterDef } from "./filters";

// The kinds of account (L1): HUMAN, a person; BOT, the simulated market's
// bots; TEST, the end-to-end scripts' accounts; SYSTEM, HOUSE and the
// like. Every user-dimension list filters by them, the humans by default:
// no kind in the address is the humans, as the server reads it.

export const KINDS = ["HUMAN", "BOT", "TEST", "SYSTEM"] as const;

/** useKindFilter is a list's 类型 filter: 真人 (the default, nothing in the address), each other kind, or 全部 (ALL). */
export function useKindFilter(): FilterDef {
  const { t } = useTranslation();
  const label = useEnum();
  return {
    key: "kind",
    label: t("admin.kinds.label"),
    kind: "select",
    width: 110,
    options: [
      { value: ALL, label: label("userKind", "HUMAN") },
      ...KINDS.filter((k) => k !== "HUMAN").map((k) => ({ value: k, label: label("userKind", k) })),
      { value: "ALL", label: t("admin.kinds.all") },
    ],
  };
}

/** kindParam is a list's kind as the API takes it: undefined (the humans) when none is chosen. */
export const kindParam = (v: string | undefined) => (v ? [v] : undefined);
