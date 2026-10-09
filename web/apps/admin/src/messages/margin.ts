// The margin strings the rest of the console shows (design 2026-10-06
// §8, A55, A56, E5): the sidebar's, the approvals' (and their payloads),
// the launch checklist's, the ledger's account types and reconciliation
// checks, merged with the pages' (pageMessages.ts). The margin pages' own
// come with their chunks (pages/margin/messages.ts).
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
        // B145: every journal line listed by type (the users' ledger filtered by type reads that list).
        LINE_TYPES_LISTED: "分录类型索引完整",
      },
    },
    funds: {
      escalation: { MARGIN_RISK: "杠杆的计息方式与利率、倍数、阈值、强平费、折扣、保证金资格、放宽借贷与手工强平一律需要另一位管理员批准" },
      escalationShort: { MARGIN_RISK: "杠杆双人" },
    },
    launch: {
      items: {
        margin: {
          name: "杠杆交易", source: "开关 margin.enabled、margin.liquidation、margin.auto_borrow",
          required: "不开放；或已开启强平，且杠杆与自动借款都按用户/地区规则开放（不对所有人全局打开）",
        },
      },
    },
    marginApproval: {
      asset: "资产 {{code}}",
      pair: "交易对 {{symbol}}",
      cross: "全仓条款",
      level: "风险率 {{level}}",
      owes: "负债 {{usdt}} USDT",
      fields: {
        borrowable: "可借", collateral: "保证金", haircut: "折扣", pool_cap: "池上限", user_cap: "单用户上限", interest_model: "计息方式",
        fixed_rate: "固定利率", float_base: "基准利率", float_kink: "拐点", float_kink_rate: "拐点利率", float_max_rate: "满池利率",
        isolated: "逐仓", leverage: "倍数", warn_level: "预警线", liquidation_level: "强平线", liquidation_fee: "强平费",
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
        LINE_TYPES_LISTED: "Every journal line listed by type",
      },
    },
    funds: {
      escalation: { MARGIN_RISK: "Margin interest and rates, leverage, thresholds, fees, haircuts, collateral, more to lend and liquidations by hand always take a second administrator" },
      escalationShort: { MARGIN_RISK: "Margin, two" },
    },
    launch: {
      items: {
        margin: {
          name: "Margin trading", source: "Flags margin.enabled, margin.liquidation and margin.auto_borrow",
          required: "Off; or on with liquidations on, margin and auto-borrowing open by user or region rules (not for everyone)",
        },
      },
    },
    marginApproval: {
      asset: "Asset {{code}}",
      pair: "Pair {{symbol}}",
      cross: "Cross terms",
      level: "Margin level {{level}}",
      owes: "Owes {{usdt}} USDT",
      fields: {
        borrowable: "Borrowable", collateral: "Collateral", haircut: "Haircut", pool_cap: "Pool cap", user_cap: "User cap",
        interest_model: "Interest model", fixed_rate: "Fixed rate", float_base: "Base rate", float_kink: "Kink", float_kink_rate: "Kink rate",
        float_max_rate: "Full pool rate", isolated: "Isolated", leverage: "Leverage", warn_level: "Warning level",
        liquidation_level: "Liquidation level", liquidation_fee: "Liquidation fee",
      },
    },
  },
};
