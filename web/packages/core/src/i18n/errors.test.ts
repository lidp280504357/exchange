import { describe, expect, it } from "vitest";
import { ApiError } from "../api/errors";
import { errorText, i18n, initI18n } from "./index";

describe("error messages", () => {
  initI18n();
  void i18n.changeLanguage("zh-CN");

  it("names the amounts an error's details carry", () => {
    const err = new ApiError(422, "DERIV_RISK_LIMIT_EXCEEDED", "", { max_notional: "50000", leverage: 30, notional: "84196.1" });
    expect(errorText(err)).toBe("超出 30x 的风险限额：该方向最多 50,000 USDT，现为 84,196.10 USDT，请降低杠杆或数量");
  });

  it("keeps the plain message when the details did not come", () => {
    const err = new ApiError(422, "DERIV_RISK_LIMIT_EXCEEDED", "", { max_notional: "50000", leverage: 30 });
    expect(errorText(err)).toBe("仓位超出该杠杆的风险限额，请降低杠杆或数量");
    expect(errorText(new ApiError(500, "NO_SUCH_CODE", ""))).toBe("出错了（NO_SUCH_CODE）");
  });
});
