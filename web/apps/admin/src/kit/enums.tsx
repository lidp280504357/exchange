import { Badge, type BadgeTone } from "@exchange/ui";
import { useTranslation } from "react-i18next";

// Every enum the console shows gets a label (design §10.2, A4) with its
// code on hover; the groups and labels live in i18n (admin.enum.*).

export type EnumGroup =
  | "userStatus"
  | "orderStatus"
  | "side"
  | "orderType"
  | "tif"
  | "depositStatus"
  | "withdrawalStatus"
  | "pairStatus"
  | "approvalStatus"
  | "approvalKind"
  | "accountType"
  | "check"
  | "liquidationKind"
  | "reasonCode"
  | "riskReason"
  | "feed"
  | "role";

/** useEnum returns a function that labels a code of a group (the code itself when unknown). */
export function useEnum() {
  const { t } = useTranslation();
  return (group: EnumGroup, code: string | null | undefined) => (code ? t(`admin.enum.${group}.${code}`, { defaultValue: code }) : "—");
}

const tones: Partial<Record<EnumGroup, Record<string, BadgeTone>>> = {
  userStatus: { ACTIVE: "success", RISK_REVIEW: "warn", FROZEN: "danger", CLOSED: "neutral" },
  orderStatus: { NEW: "info", OPEN: "info", PARTIALLY_FILLED: "brand", FILLED: "success", CANCELED: "neutral", REJECTED: "danger", EXPIRED: "neutral" },
  side: { BUY: "up", SELL: "down" },
  depositStatus: { DETECTED: "info", CONFIRMING: "info", CONFIRMED: "brand", CREDITED: "success", ORPHANED: "warn", REJECTED: "danger" },
  withdrawalStatus: {
    REQUESTED: "info", RISK_SCORING: "info", PENDING_REVIEW: "warn", APPROVED: "brand", SIGNING: "brand", BROADCAST: "brand",
    CONFIRMING: "brand", CONFIRMED: "success", INTERNAL_TRANSFER: "success", REJECTED: "danger", CANCELED: "neutral", FAILED: "danger",
  },
  pairStatus: { PREPARE: "neutral", TRADING: "success", HALT: "warn", CANCEL_ONLY: "warn", DELISTED: "danger" },
  approvalStatus: { PENDING: "warn", EXECUTED: "success", REJECTED: "neutral", FAILED: "danger" },
  feed: { OK: "success", DELAYED: "warn", DOWN: "danger", OFF: "neutral" },
  liquidationKind: { WARNING: "warn", STARTED: "danger", FILLED: "danger", ADL: "danger", ENDED: "neutral" },
};

/** EnumBadge labels a code as a badge toned by its meaning. */
export function EnumBadge({ group, code }: { group: EnumGroup; code: string | null | undefined }) {
  const label = useEnum();
  if (!code) return <span className="text-fg-3">—</span>;
  return (
    <Badge tone={tones[group]?.[code] ?? "neutral"} title={code}>
      {label(group, code)}
    </Badge>
  );
}

/** EnumText labels a code as plain text with the code on hover. */
export function EnumText({ group, code }: { group: EnumGroup; code: string | null | undefined }) {
  const label = useEnum();
  return <span title={code ?? undefined}>{label(group, code)}</span>;
}
