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
  | "role"
  | "providerStatus"
  | "callbackResult"
  | "callbackKind"
  | "custodyStatus"
  | "identityKind"
  | "identityRequestStatus"
  | "totpStatus"
  | "riskAction"
  | "consentDocument"
  | "loginMethod"
  | "depositReason"
  | "depositSource"
  | "depositResolution"
  | "adminStatus"
  | "feeStatus"
  | "feeUnit"
  | "marginStatus"
  | "marginType"
  | "marginTrigger"
  | "marginLiquidationStatus"
  | "rateModel"
  | "loanKind"
  | "loanStatus"
  | "loanReason"
  | "userKind";

/** useEnum returns a function that labels a code of a group (the code itself when unknown). */
export function useEnum() {
  const { t } = useTranslation();
  return (group: EnumGroup, code: string | null | undefined) => (code ? t(`admin.enum.${group}.${code}`, { defaultValue: code }) : "—");
}

const tones: Partial<Record<EnumGroup, Record<string, BadgeTone>>> = {
  userStatus: { ACTIVE: "success", RISK_REVIEW: "warn", FROZEN: "danger", CLOSED: "neutral" },
  userKind: { HUMAN: "neutral", BOT: "info", TEST: "warn", SYSTEM: "brand" },
  orderStatus: { NEW: "info", OPEN: "info", PARTIALLY_FILLED: "brand", FILLED: "success", CANCELED: "neutral", REJECTED: "danger", EXPIRED: "neutral" },
  side: { BUY: "up", SELL: "down" },
  depositStatus: { DETECTED: "info", CONFIRMING: "info", CONFIRMED: "brand", CREDITED: "success", ORPHANED: "warn", REJECTED: "danger" },
  withdrawalStatus: {
    REQUESTED: "info", RISK_SCORING: "info", PENDING_REVIEW: "warn", APPROVED: "brand", SIGNING: "brand", BROADCAST: "brand",
    CONFIRMING: "brand", SUBMITTED: "brand", CONFIRMED: "success", INTERNAL_TRANSFER: "success", REJECTED: "danger", CANCELED: "neutral",
    FAILED: "danger",
  },
  providerStatus: {
    SUBMITTED: "warn", ACCEPTED: "info", REVIEW: "info", APPROVED: "brand", REJECTED: "danger", SUCCESS: "success", FAILED: "danger",
    // A re-hand-over refused, or 30 minutes without an answer: a person
    // resolves it (exchangectl wallet custody-resolve).
    UNCERTAIN: "danger",
  },
  callbackResult: {
    RECEIVED: "info", APPLIED: "success", IGNORED: "neutral", UNMATCHED: "warn", REJECTED: "danger", FAILED: "danger", DISCREPANCY: "danger",
  },
  depositReason: { BELOW_MINIMUM: "warn", ACCOUNT_CLOSED: "warn", NOT_ELIGIBLE: "warn", UNSUPPORTED_TOKEN: "danger" },
  depositSource: { AUTO: "neutral", MANUAL: "info" },
  depositResolution: { CREDITED: "success", DISMISSED: "neutral" },
  adminStatus: { ACTIVE: "success", DISABLED: "neutral" },
  custodyStatus: { "0": "info", "1": "brand", "2": "danger", "3": "success", "4": "danger" },
  pairStatus: { PREPARE: "neutral", TRADING: "success", HALT: "warn", CANCEL_ONLY: "warn", DELISTED: "danger" },
  approvalStatus: { PENDING: "warn", EXECUTED: "success", REJECTED: "neutral", FAILED: "danger" },
  feed: { OK: "success", DELAYED: "warn", DOWN: "danger", OFF: "neutral" },
  liquidationKind: { WARNING: "warn", STARTED: "danger", FILLED: "danger", ADL: "danger", ENDED: "neutral" },
  identityRequestStatus: { PENDING_REVIEW: "warn", APPROVED: "success", REJECTED: "neutral" },
  totpStatus: { ACTIVE: "success", PENDING: "warn", NONE: "neutral" },
  riskAction: { NONE: "neutral", STEP_UP: "info", REVIEW: "warn", REJECT: "danger" },
  feeStatus: { HELD: "warn", BOOKABLE: "success", WRITTEN_OFF: "neutral" },
  marginStatus: { NORMAL: "success", WARNED: "warn", LIQUIDATING: "danger", FROZEN: "danger" },
  marginType: { MARGIN_CROSS: "brand", MARGIN_ISOLATED: "info" },
  marginTrigger: { AUTO: "neutral", MANUAL: "warn" },
  marginLiquidationStatus: { STARTED: "danger", SHORTFALL: "danger", COMPLETED: "neutral" },
  rateModel: { FIXED: "neutral", FLOATING: "info" },
  loanKind: { BORROW: "warn", REPAY: "success", INTEREST: "info" },
  loanStatus: { PENDING: "warn", DONE: "success", FAILED: "danger" },
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
