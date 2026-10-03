// The strings of the trading and market pages added by the 2026-10-02
// rebuild (C3: every user's positions, the liquidation log), merged into
// the console's messages in i18n.ts.
export const tradingZh = {
  admin: {
    nav: { positions: "仓位", liquidations: "强平记录" },
    positions: {
      help: "全部用户的合约持仓，按保证金率（维持保证金 ÷ 保证金余额）从高到低，每 5 秒刷新；全仓仓位单独计算，强平价见用户页，全仓账户被预警时它的全仓仓位都算预警。HOUSE 的仓位是用户的对手方，排在最后，不进风险仓位。",
      all: "全部",
      watch: "风险仓位",
      noneAtRisk: "没有被预警、被接管或保证金率 ≥ 50% 的仓位",
      truncated: "只显示风险最高的 {{n}} 个，可按合约或用户筛选",
      markStale: "标记价过期",
      markStaleHint: "标记价太旧，风控暂停测量这个合约；这一行的盈亏与保证金率停在旧价上",
    },
    liquidations: {
      help: "强平引擎的每一步：预警、接管、强平成交、自动减仓（读模型，比服务晚几秒）。",
      period: "时间",
    },
  },
};

export const tradingEn = {
  admin: {
    nav: { positions: "Positions", liquidations: "Liquidations" },
    positions: {
      help: "Every user's contract positions, highest margin ratio (maintenance margin ÷ margin balance) first, refreshed every 5 seconds; cross positions are measured on their own, their liquidation price is on the user's page, and they count as warned when their cross account is. HOUSE's positions are the users' counterparty: last, never at risk.",
      all: "All",
      watch: "At risk",
      noneAtRisk: "No position is warned, taken over or at a margin ratio of 50% or more",
      truncated: "Only the {{n}} riskiest are shown; filter by contract or user",
      markStale: "Stale mark",
      markStaleHint: "The mark price is too old: the margin monitor does not measure this contract, and the row's figures stand at the old price",
    },
    liquidations: {
      help: "Every step of the liquidation engine: warnings, take-overs, liquidation fills, auto-deleveraging (the read model, seconds behind).",
      period: "Period",
    },
  },
};
