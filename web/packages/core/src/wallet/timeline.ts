import type { Deposit, Withdrawal } from "./networks";

// Status timelines of deposits and withdrawals (design §6.2): which steps
// are done, which one is in progress and where a failed one stopped. The
// pages draw them with the Stepper; the step names are the pages' words.
//
//   deposit:    detected → confirming n/m → credited
//   withdrawal: risk → review → sign → broadcast → confirm n/m → done
//               (to another user's deposit address: risk → review → done)

export type StepState = "done" | "current" | "upcoming" | "error";

export type TimelineStep<K extends string> = {
  key: K;
  state: StepState;
  /** When the step happened, when the record says. */
  at: string | null;
};

export type Timeline<K extends string> = {
  steps: TimelineStep<K>[];
  outcome: "pending" | "done" | "failed";
  /** Confirmations so far and needed, while they are being counted. */
  confirmations: { n: number; of: number } | null;
};

const step = <K extends string>(key: K, state: StepState, at: string | null = null): TimelineStep<K> => ({ key, state, at });

// ---- deposits ----

export type DepositStep = "detected" | "confirming" | "credited" | "failed";

/** What a deposit's status means for the user. */
export type DepositPhase = "confirming" | "crediting" | "credited" | "failed";

/**
 * depositPhase groups the statuses: DETECTED and CONFIRMING count
 * confirmations, CONFIRMED waits for the ledger, CREDITED is done, and
 * ORPHANED (dropped by a reorganization) or REJECTED (not credited) failed.
 */
export function depositPhase(status: string): DepositPhase {
  switch (status) {
    case "CREDITED":
      return "credited";
    case "CONFIRMED":
      return "crediting";
    case "ORPHANED":
    case "REJECTED":
      return "failed";
    default:
      return "confirming";
  }
}

type DepositLike = Pick<Deposit, "status" | "confirmations" | "required_confirmations" | "detected_at" | "confirmed_at" | "credited_at">;

/** depositTimeline lays a deposit out as detected → confirming n/m → credited. */
export function depositTimeline(d: DepositLike): Timeline<DepositStep> {
  const of = Math.max(1, d.required_confirmations);
  const counted = { n: Math.min(Math.max(0, d.confirmations), of), of };
  switch (depositPhase(d.status)) {
    case "confirming":
      return {
        steps: [step("detected", "done", d.detected_at), step("confirming", "current"), step("credited", "upcoming")],
        outcome: "pending",
        confirmations: counted,
      };
    case "crediting":
      return {
        steps: [step("detected", "done", d.detected_at), step("confirming", "done", d.confirmed_at), step("credited", "current")],
        outcome: "pending",
        confirmations: null,
      };
    case "credited":
      return {
        steps: [step("detected", "done", d.detected_at), step("confirming", "done", d.confirmed_at), step("credited", "done", d.credited_at)],
        outcome: "done",
        confirmations: null,
      };
    default: {
      // Rejected after its confirmations (below the minimum, closed account):
      // the confirmations were counted; a reorganization dropped it before.
      const confirmed = d.status === "REJECTED" && d.confirmed_at !== null;
      return {
        steps: [
          step("detected", "done", d.detected_at),
          ...(confirmed ? [step<DepositStep>("confirming", "done", d.confirmed_at)] : []),
          step("failed", "error"),
        ],
        outcome: "failed",
        confirmations: null,
      };
    }
  }
}

// ---- withdrawals ----

export type WithdrawalStep = "risk" | "review" | "sign" | "broadcast" | "confirm" | "custody" | "done" | "failed";

const CHAIN_STEPS: WithdrawalStep[] = ["risk", "review", "sign", "broadcast", "confirm", "done"];
// The custodian signs, sends and confirms on its side (ADR-0011).
const CUSTODY_STEPS: WithdrawalStep[] = ["risk", "review", "custody", "done"];
const INTERNAL_STEPS: WithdrawalStep[] = ["risk", "review", "done"];

const FAILED = new Set(["REJECTED", "CANCELED", "FAILED"]);
const CANCELABLE = new Set(["REQUESTED", "PENDING_REVIEW", "APPROVED"]);

/** withdrawalCancelable reports whether the caller may still cancel: until it is being signed or is with the custodian. */
export function withdrawalCancelable(status: string): boolean {
  return CANCELABLE.has(status);
}

type WithdrawalLike = Pick<
  Withdrawal,
  | "status" | "internal" | "custody" | "risk_reasons" | "approvals_required" | "tx_hash" | "confirmations" | "required_confirmations"
  | "created_at" | "approved_at" | "submitted_at" | "broadcast_at" | "confirmed_at"
>;

// progressIndex is the step in progress; steps.length once completed.
function progressIndex(w: WithdrawalLike, count: number): number {
  switch (w.status) {
    case "REQUESTED":
      return 0;
    case "PENDING_REVIEW":
      return 1;
    case "APPROVED":
    case "SIGNING":
    case "SUBMITTED":
      return 2;
    case "BROADCAST":
      return 3;
    case "CONFIRMING":
      return 4;
    case "CONFIRMED":
    case "INTERNAL_TRANSFER":
      return count;
    default:
      return 0;
  }
}

// failureIndex is the step a refused, canceled or failed withdrawal stopped at.
function failureIndex(w: WithdrawalLike): number {
  if (w.status === "FAILED" && w.custody) return w.approved_at ? 2 : 0;
  if (w.status === "FAILED") {
    if (w.broadcast_at) return 4;
    if (w.tx_hash) return 3;
    return w.approved_at ? 2 : 0;
  }
  if (w.approved_at) return 2;
  return w.approvals_required > 0 || w.risk_reasons.length > 0 ? 1 : 0;
}

function stepTime(key: WithdrawalStep, w: WithdrawalLike): string | null {
  switch (key) {
    case "risk":
      return w.created_at;
    case "review":
      return w.approved_at;
    case "broadcast":
      return w.broadcast_at;
    case "custody":
      return w.submitted_at;
    case "confirm":
      return w.confirmed_at;
    case "done":
      return w.confirmed_at ?? (w.status === "INTERNAL_TRANSFER" ? w.approved_at : null);
    default:
      return null;
  }
}

/**
 * withdrawalTimeline lays a withdrawal out as risk → review → sign →
 * broadcast → confirm → done (risk → review → custody → done with the
 * custodian, risk → review → done inside the platform). A refused,
 * canceled or failed one ends in an error step where it stopped.
 */
export function withdrawalTimeline(w: WithdrawalLike): Timeline<WithdrawalStep> {
  const keys = w.internal ? INTERNAL_STEPS : w.custody ? CUSTODY_STEPS : CHAIN_STEPS;
  if (FAILED.has(w.status)) {
    const at = Math.min(failureIndex(w), keys.length - 1);
    return {
      steps: [...keys.slice(0, at).map((k) => step(k, "done", stepTime(k, w))), step<WithdrawalStep>("failed", "error")],
      outcome: "failed",
      confirmations: null,
    };
  }
  const p = Math.min(progressIndex(w, keys.length), keys.length);
  const counting = !w.internal && !w.custody && (w.status === "BROADCAST" || w.status === "CONFIRMING");
  const of = Math.max(1, w.required_confirmations);
  return {
    steps: keys.map((k, i) => step(k, i < p ? "done" : i === p ? "current" : "upcoming", i < p ? stepTime(k, w) : null)),
    outcome: p >= keys.length ? "done" : "pending",
    confirmations: counting ? { n: Math.min(Math.max(0, w.confirmations), of), of } : null,
  };
}
