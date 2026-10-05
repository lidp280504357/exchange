// The margin strings the rest of the console shows (design 2026-10-06
// §8, A55): the sidebar's, the approvals' and the launch checklist's,
// merged with the pages' (pageMessages.ts). The margin pages' own come
// with their chunks (pages/margin/messages.ts).
export const marginZh = {
  admin: {
    groups: { margin: "杠杆" },
    nav: { marginAccounts: "杠杆账户", marginLiquidations: "杠杆强平", marginInterest: "利息报表", marginParams: "杠杆参数" },
    enum: {
      approvalKind: { MARGIN_PARAMS: "杠杆参数", MARGIN_LIQUIDATE: "杠杆手工强平" },
    },
    funds: {
      escalation: { MARGIN_RISK: "杠杆的利率、倍数、阈值、折扣、放宽借贷与手工强平一律需要另一位管理员批准" },
      escalationShort: { MARGIN_RISK: "杠杆双人" },
    },
    launch: {
      items: {
        margin: { name: "杠杆参数", source: "开关 margin.enabled 与杠杆参数", required: "各资产的池上限、利率已按正式值配置" },
      },
    },
  },
};

export const marginEn = {
  admin: {
    groups: { margin: "Margin" },
    nav: { marginAccounts: "Margin accounts", marginLiquidations: "Margin liquidations", marginInterest: "Interest", marginParams: "Margin parameters" },
    enum: {
      approvalKind: { MARGIN_PARAMS: "Margin parameters", MARGIN_LIQUIDATE: "Margin liquidation by hand" },
    },
    funds: {
      escalation: { MARGIN_RISK: "Margin rates, leverage, thresholds, haircuts, more to lend and liquidations by hand always take a second administrator" },
      escalationShort: { MARGIN_RISK: "Margin, two" },
    },
    launch: {
      items: {
        margin: { name: "Margin parameters", source: "Flag margin.enabled and the margin parameters", required: "Every asset's pool cap and rates at their launch values" },
      },
    },
  },
};
