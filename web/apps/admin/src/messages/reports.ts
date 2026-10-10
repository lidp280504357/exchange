// The strings of the reports added by the 2026-10-02 rebuild (C4c: periods
// and buckets, the users' activity, HOUSE's result), merged into the
// console's messages in i18n.ts.
export const reportsZh = {
  admin: {
    reports: {
      help: "读模型的数据，比各服务晚几秒；时间按 UTC。可选近 7/30/90 天或自定日期，按日、周（从周一起）或月汇总。默认只算真人的账户（现货成交有一方是真人即算），「类型」可换成机器人、测试、系统或全部；HOUSE 盈亏不分类型。",
      tabs: { users: "用户增长", housePnl: "HOUSE 盈亏" },
      custom: "自定日期", period: "时间范围", bucketLabel: "汇总方式", bucket: { day: "按日", week: "按周", month: "按月" },
      registered: "注册", signedIn: "登录", traders: "交易", depositors: "充值到账", totalUsers: "累计用户",
      spotPnl: "现货", contractsPnl: "合约", funding: "资金费", totalPnl: "合计", cumulative: "期间累计", spotResult: "现货累计结果",
      unpriced: "这些交易对没有可用价格（或计价资产没有 USDT 价格），不计入现货结果：{{pairs}}",
    },
  },
};

export const reportsEn = {
  admin: {
    reports: {
      help: "From the read models, a few seconds behind the services; days in UTC. The last 7, 30 or 90 days or two dates, per day, week (from Monday) or month. The humans' accounts by default (a spot trade with a human on one side counts); Kind takes the bots, test or system accounts, or all; HOUSE's result has no kinds.",
      tabs: { users: "Users", housePnl: "HOUSE result" },
      custom: "Dates", period: "Period", bucketLabel: "Per", bucket: { day: "Day", week: "Week", month: "Month" },
      registered: "Registered", signedIn: "Signed in", traders: "Traded", depositors: "Deposited", totalUsers: "All accounts",
      spotPnl: "Spot", contractsPnl: "Contracts", funding: "Funding", totalPnl: "Total", cumulative: "Over the period", spotResult: "Spot result to date",
      unpriced: "No price for these pairs (or for their quote asset in USDT): left out of the spot result: {{pairs}}",
    },
  },
};
