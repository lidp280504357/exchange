// The coin-margined contracts' strings (design 2026-10-06 §2.7, G5): the
// contract tables' margin type, settlement asset and face value, the
// insurance fund of every settlement asset, and the launch checklist's
// two items; merged with the pages' (pageMessages.ts).
export const coinmZh = {
  admin: {
    coinm: {
      marginType: { USDT: "U 本位", COIN: "币本位" },
      kind: "类型",
      settle: "结算币",
      size: "面值",
      sizeValue: "{{size}} 美元/张",
      tiersIn: "名义价值以 {{asset}} 计",
      funds: "各结算币的保险基金",
      fundsHint: "合约穿仓由该合约结算币的保险基金承担，杠杆强平的缺口也从同一行出；注资按资产分别审批。",
      asset: "资产",
      contributeAsset: "向 {{asset}} 保险基金注资",
    },
    launch: {
      items: {
        insurance: {
          name: "合约保险基金",
          source: "账本 INSURANCE_FUND（各结算币）与在交易的合约",
          required: "每个在交易合约的结算币，保险基金余额都大于 0",
        },
        coin_m: {
          name: "币本位合约",
          source: "开关 derivatives.coin_m",
          required: "不开放；或按用户/地区规则开放（不对所有人全局打开）",
        },
      },
      insuranceShort: "缺：{{assets}}",
      coinMGlobal: "对所有人",
    },
  },
};

export const coinmEn = {
  admin: {
    coinm: {
      marginType: { USDT: "USDⓈ-M", COIN: "COIN-M" },
      kind: "Type",
      settle: "Settles in",
      size: "Face value",
      sizeValue: "{{size}} USD a contract",
      tiersIn: "Notionals in {{asset}}",
      funds: "Insurance fund by settlement asset",
      fundsHint: "A contract's loss beyond its margin is paid from its settlement asset's fund, and so is a margin liquidation's shortfall; contributions are approved per asset.",
      asset: "Asset",
      contributeAsset: "Contribute to the {{asset}} insurance fund",
    },
    launch: {
      items: {
        insurance: {
          name: "Contracts' insurance fund",
          source: "Ledger INSURANCE_FUND (each settlement asset) and the contracts in trading",
          required: "Every settlement asset of a contract in trading has an insurance fund above 0",
        },
        coin_m: {
          name: "Coin-margined contracts",
          source: "Flag derivatives.coin_m",
          required: "Off; or open by user or region rules (not for everyone)",
        },
      },
      insuranceShort: "missing: {{assets}}",
      coinMGlobal: "for everyone",
    },
  },
};
