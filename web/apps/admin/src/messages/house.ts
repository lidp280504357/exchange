// The strings of the HOUSE page as C6 redid it (its results over 30 days,
// its exposure, the filters); merged into the console's messages in
// i18n.ts beside the page's earlier ones (admin.house in zh.ts and en.ts).
export const houseZh = {
  admin: {
    house: {
      results: "近 30 日盈亏",
      resultsHint: "按日：现货盈亏（按当日价）、合约已实现盈亏减手续费、资金费；折线为 30 日累计。报表「HOUSE 盈亏」可换区间与粒度。",
      exposure: "敞口",
      exposureHint: "库存按现价折成 USDT，绝对值最大的 10 个：向右为持有，向左为卖出后为负的站内资产。",
      noExposure: "没有可估值的库存",
      assetsCount: "{{n}} 个资产",
      pairsCount: "{{n}} 个交易对有成交",
      positionsCount: "{{open}} / {{all}} 个合约有仓位",
      filter: { all: "全部", backed: "可充提", internal: "站内" },
      search: "搜索",
      side: "方向",
      long: "多",
      short: "空",
      flat: "无仓位",
      noPrice: "无价格",
      unpriced: "缺价格、未计入现货盈亏的交易对：{{pairs}}",
      spot: "现货",
      contractsPnl: "合约",
      funding: "资金费",
      cumulative: "累计",
    },
  },
};

export const houseEn = {
  admin: {
    house: {
      results: "Results over 30 days",
      resultsHint:
        "Per day: spot results (at the day's prices), contracts' realized results less fees, funding; the line is the 30 days' running total. The HOUSE results report takes other periods and buckets.",
      exposure: "Exposure",
      exposureHint: "The inventory at the last prices in USDT, the 10 largest either way: to the right what it holds, to the left the internal assets it sold below zero.",
      noExposure: "No inventory with a price",
      assetsCount: "{{n}} assets",
      pairsCount: "{{n}} pairs traded",
      positionsCount: "Positions on {{open}} of {{all}} contracts",
      filter: { all: "All", backed: "Withdrawable", internal: "Internal" },
      search: "Search",
      side: "Side",
      long: "Long",
      short: "Short",
      flat: "Flat",
      noPrice: "No price",
      unpriced: "Pairs without a price, left out of the spot results: {{pairs}}",
      spot: "Spot",
      contractsPnl: "Contracts",
      funding: "Funding",
      cumulative: "Running total",
    },
  },
};
