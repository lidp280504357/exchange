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

  it("picks the message whose details came", () => {
    const level = new ApiError(422, "MARGIN_LEVEL_TOO_LOW", "", { margin_level: "1.2468", warn_level: "1.3" });
    expect(errorText(level)).toBe("操作后风险率 1.24 会低于预警线 1.3");
    const out = new ApiError(422, "MARGIN_LEVEL_TOO_LOW", "", { max_transferable: "12.5" });
    expect(errorText(out)).toBe("划出后风险率会低于预警线：现在最多可划出 12.5");
    // A transfer out carries the levels too: the transferable amount wins.
    const outFull = new ApiError(422, "MARGIN_LEVEL_TOO_LOW", "", { margin_level: "1.28", warn_level: "1.3", max_transferable: "12.5" });
    expect(errorText(outFull)).toBe("划出后风险率会低于预警线：现在最多可划出 12.5");
    const short = new ApiError(422, "LEDGER_INSUFFICIENT_BALANCE", "", { max_transferable: "3" });
    expect(errorText(short)).toBe("可用余额不足：现在最多可划出 3");
    expect(errorText(new ApiError(422, "LEDGER_INSUFFICIENT_BALANCE", "", { order_id: "o1" }))).toBe("可用余额不足");
    expect(errorText(new ApiError(403, "MARGIN_DISABLED", "", { flag: "margin.auto_borrow" }))).toBe("自动借款暂未开放，借还方式已改为普通");
    expect(errorText(new ApiError(403, "MARGIN_DISABLED", ""))).toBe("杠杆交易暂未开放");
    // Only margin.auto_borrow is about the side effect.
    expect(errorText(new ApiError(403, "MARGIN_DISABLED", "", { flag: "margin.enabled" }))).toBe("杠杆交易暂未开放");
  });
});
