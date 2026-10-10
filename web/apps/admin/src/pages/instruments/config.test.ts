import { describe, expect, it } from "vitest";
import contract from "../../../../../../internal/instrument/domain/contract.go?raw";
import spec from "../../../../../../api/admin/admin.yaml?raw";
import { LEVERAGE_CAP, riskTierProblems } from "./config";

// The risk tiers' leverage cap is instrument-service's LeverageCap (B171),
// written in three places - the Go constant, the console's and the
// contract's text - kept one here (A114).
describe("the risk tiers' leverage cap", () => {
  it("is instrument-service's LeverageCap, as the contract says", () => {
    expect(Number(/const LeverageCap = (\d+)/.exec(contract)?.[1])).toBe(LEVERAGE_CAP);
    expect(spec).toContain(`(1-${LEVERAGE_CAP}, instrument-service's LeverageCap)`);
  });

  it("takes a tier up to it", () => {
    const tier = (leverage: number) => [{ max_notional: "300000", max_leverage: leverage, mmr: "0.004" }];
    expect(riskTierProblems(tier(LEVERAGE_CAP))).toEqual([]);
    expect(riskTierProblems(tier(LEVERAGE_CAP + 1))).toContain("leverage:0");
  });
});
