// The margin strings the rest of the console shows (design 2026-10-06
// §8, A55, A56): the sidebar's, the approvals', the launch checklist's,
// the ledger's account types and reconciliation checks, merged with the
// pages' (pageMessages.ts). The margin pages' own come with their chunks
// (pages/margin/messages.ts).
export const marginZh = {
  admin: {
    groups: { margin: "杠杆" },
    nav: { marginAccounts: "杠杆账户", marginLiquidations: "杠杆强平", marginInterest: "利息报表", marginParams: "杠杆参数" },
    enum: {
      approvalKind: { MARGIN_PARAMS: "杠杆参数", MARGIN_LIQUIDATE: "杠杆手工强平" },
      accountType: {
        MARGIN_CROSS: "全仓杠杆", MARGIN_CROSS_DEBT: "全仓负债", MARGIN_CROSS_INTEREST: "全仓利息", MARGIN_ISOLATED: "逐仓杠杆",
        MARGIN_ISOLATED_DEBT: "逐仓负债", MARGIN_ISOLATED_INTEREST: "逐仓利息", MARGIN_INTEREST_INCOME: "杠杆利息收入",
      },
      check: {
        MARGIN_ROWS_SIGNED: "杠杆行符号", MARGIN_INTEREST_CONSERVED: "利息守恒", HOUSE_BACKED_NON_NEGATIVE: "HOUSE 背书资产不为负",
      },
    },
    funds: {
      escalation: { MARGIN_RISK: "杠杆的计息方式与利率、倍数、阈值、强平费、折扣、保证金资格、放宽借贷与手工强平一律需要另一位管理员批准" },
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
      accountType: {
        MARGIN_CROSS: "Cross margin", MARGIN_CROSS_DEBT: "Cross debt", MARGIN_CROSS_INTEREST: "Cross interest", MARGIN_ISOLATED: "Isolated margin",
        MARGIN_ISOLATED_DEBT: "Isolated debt", MARGIN_ISOLATED_INTEREST: "Isolated interest", MARGIN_INTEREST_INCOME: "Margin interest income",
      },
      check: {
        MARGIN_ROWS_SIGNED: "Margin rows signed", MARGIN_INTEREST_CONSERVED: "Interest conserved", HOUSE_BACKED_NON_NEGATIVE: "HOUSE's backed assets not negative",
      },
    },
    funds: {
      escalation: { MARGIN_RISK: "Margin interest and rates, leverage, thresholds, fees, haircuts, collateral, more to lend and liquidations by hand always take a second administrator" },
      escalationShort: { MARGIN_RISK: "Margin, two" },
    },
    launch: {
      items: {
        margin: { name: "Margin parameters", source: "Flag margin.enabled and the margin parameters", required: "Every asset's pool cap and rates at their launch values" },
      },
    },
  },
};
