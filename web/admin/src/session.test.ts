import { describe as group, expect, it } from "vitest";
import { ApiError, describe, errorFrom } from "./api/client";
import { can } from "./session";

group("can", () => {
  it("checks the permission list", () => {
    const finance = { permissions: ["withdrawals.read", "withdrawals.review"] as const };
    expect(can({ permissions: [...finance.permissions] }, "withdrawals.review")).toBe(true);
    expect(can({ permissions: [...finance.permissions] }, "flags.write")).toBe(false);
    expect(can(undefined, "users.read")).toBe(false);
  });
});

group("errors", () => {
  it("reads the unified body", () => {
    const err = errorFrom(403, { code: "ADMIN_FORBIDDEN", message: "no", trace_id: "0123456789abcdef0123456789abcdef" });
    expect(err).toBeInstanceOf(ApiError);
    expect(describe(err)).toBe("你的角色没有这项权限（ADMIN_FORBIDDEN，trace 01234567）");
  });

  it("falls back on the status for bodies that are not JSON", () => {
    expect(errorFrom(502, "<html>").code).toBe("COMMON_UNAVAILABLE");
    expect(errorFrom(429, undefined).code).toBe("COMMON_RATE_LIMITED");
    expect(describe(new ApiError(400, "WALLET_X", "raw message"))).toBe("raw message（WALLET_X）");
  });
});
