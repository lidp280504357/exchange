import { describe, expect, it } from "vitest";
import { depositPhase, depositTimeline, withdrawalCancelable, withdrawalTimeline } from "./timeline";

const states = (t: { steps: { key: string; state: string }[] }) => t.steps.map((s) => `${s.key}:${s.state}`);

const deposit = {
  status: "CONFIRMING",
  confirmations: 3,
  required_confirmations: 12,
  detected_at: "2026-09-30T10:00:00Z",
  confirmed_at: null,
  credited_at: null,
} as const;

describe("depositTimeline", () => {
  it("counts confirmations while confirming", () => {
    const t = depositTimeline(deposit);
    expect(states(t)).toEqual(["detected:done", "confirming:current", "credited:upcoming"]);
    expect(t.confirmations).toEqual({ n: 3, of: 12 });
    expect(t.outcome).toBe("pending");
    expect(depositTimeline({ ...deposit, status: "DETECTED", confirmations: 0 }).confirmations).toEqual({ n: 0, of: 12 });
    expect(depositTimeline({ ...deposit, confirmations: 20 }).confirmations).toEqual({ n: 12, of: 12 });
  });

  it("waits for the ledger once confirmed, then is done", () => {
    expect(states(depositTimeline({ ...deposit, status: "CONFIRMED", confirmed_at: "2026-09-30T10:03:00Z" }))).toEqual([
      "detected:done", "confirming:done", "credited:current",
    ]);
    const done = depositTimeline({ ...deposit, status: "CREDITED", confirmed_at: "2026-09-30T10:03:00Z", credited_at: "2026-09-30T10:03:01Z" });
    expect(states(done)).toEqual(["detected:done", "confirming:done", "credited:done"]);
    expect(done.steps[2]?.at).toBe("2026-09-30T10:03:01Z");
    expect(done.outcome).toBe("done");
  });

  it("ends in an error step when dropped or refused", () => {
    expect(states(depositTimeline({ ...deposit, status: "ORPHANED" }))).toEqual(["detected:done", "failed:error"]);
    expect(states(depositTimeline({ ...deposit, status: "REJECTED", confirmed_at: "2026-09-30T10:03:00Z" }))).toEqual([
      "detected:done", "confirming:done", "failed:error",
    ]);
    expect(depositTimeline({ ...deposit, status: "REJECTED" }).outcome).toBe("failed");
  });

  it("groups statuses into phases", () => {
    expect(["DETECTED", "CONFIRMING", "CONFIRMED", "CREDITED", "ORPHANED", "REJECTED"].map(depositPhase)).toEqual([
      "confirming", "confirming", "crediting", "credited", "failed", "failed",
    ]);
  });
});

const withdrawal = {
  status: "REQUESTED",
  internal: false,
  risk_reasons: [] as ("NEW_ADDRESS" | "LARGE_AMOUNT")[],
  approvals_required: 0,
  tx_hash: null,
  confirmations: 0,
  required_confirmations: 12,
  created_at: "2026-09-30T10:00:00Z",
  approved_at: null,
  broadcast_at: null,
  confirmed_at: null,
} as const;

describe("withdrawalTimeline", () => {
  it("follows risk → review → sign → broadcast → confirm → done", () => {
    const at = (status: string, extra: object = {}) => states(withdrawalTimeline({ ...withdrawal, ...extra, status: status as never }));
    expect(at("REQUESTED")).toEqual(["risk:current", "review:upcoming", "sign:upcoming", "broadcast:upcoming", "confirm:upcoming", "done:upcoming"]);
    expect(at("PENDING_REVIEW")[1]).toBe("review:current");
    expect(at("APPROVED")[2]).toBe("sign:current");
    expect(at("SIGNING")[2]).toBe("sign:current");
    expect(at("BROADCAST")[3]).toBe("broadcast:current");
    expect(at("CONFIRMING")[4]).toBe("confirm:current");
    expect(at("CONFIRMED").every((s) => s.endsWith(":done"))).toBe(true);
  });

  it("counts confirmations once broadcast", () => {
    const t = withdrawalTimeline({ ...withdrawal, status: "CONFIRMING", tx_hash: "0xabc" as never, confirmations: 5 });
    expect(t.confirmations).toEqual({ n: 5, of: 12 });
    expect(withdrawalTimeline({ ...withdrawal, status: "PENDING_REVIEW" }).confirmations).toBeNull();
  });

  it("keeps the times of the steps done", () => {
    const t = withdrawalTimeline({
      ...withdrawal, status: "CONFIRMED", approved_at: "2026-09-30T10:01:00Z" as never, broadcast_at: "2026-09-30T10:02:00Z" as never,
      confirmed_at: "2026-09-30T10:05:00Z" as never,
    });
    expect(t.steps.map((s) => s.at)).toEqual([
      "2026-09-30T10:00:00Z", "2026-09-30T10:01:00Z", null, "2026-09-30T10:02:00Z", "2026-09-30T10:05:00Z", "2026-09-30T10:05:00Z",
    ]);
    expect(t.outcome).toBe("done");
  });

  it("is shorter inside the platform", () => {
    expect(states(withdrawalTimeline({ ...withdrawal, internal: true, status: "APPROVED" }))).toEqual(["risk:done", "review:done", "done:current"]);
    expect(withdrawalTimeline({ ...withdrawal, internal: true, status: "INTERNAL_TRANSFER" }).outcome).toBe("done");
  });

  it("stops where a withdrawal was refused, canceled or failed", () => {
    const at = (extra: object) => states(withdrawalTimeline({ ...withdrawal, ...extra } as never));
    // The ledger refused the freeze at once.
    expect(at({ status: "REJECTED" })).toEqual(["failed:error"]);
    // Refused (or canceled) during review.
    expect(at({ status: "REJECTED", approvals_required: 1, risk_reasons: ["NEW_ADDRESS"] })).toEqual(["risk:done", "failed:error"]);
    expect(at({ status: "CANCELED", approvals_required: 1 })).toEqual(["risk:done", "failed:error"]);
    // Canceled after approval, before signing.
    expect(at({ status: "CANCELED", approved_at: "2026-09-30T10:01:00Z" })).toEqual(["risk:done", "review:done", "failed:error"]);
    // Failed on the chain.
    expect(at({ status: "FAILED", approved_at: "x", tx_hash: "0xabc", broadcast_at: "y" })).toEqual([
      "risk:done", "review:done", "sign:done", "broadcast:done", "failed:error",
    ]);
    expect(withdrawalTimeline({ ...withdrawal, status: "FAILED" } as never).outcome).toBe("failed");
  });

  it("allows canceling until signing starts", () => {
    expect(["REQUESTED", "PENDING_REVIEW", "APPROVED"].every(withdrawalCancelable)).toBe(true);
    expect(["SIGNING", "BROADCAST", "CONFIRMING", "CONFIRMED", "CANCELED", "REJECTED"].some(withdrawalCancelable)).toBe(false);
  });
});
