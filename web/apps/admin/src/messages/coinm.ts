// The coin-margined contracts' strings (design 2026-10-06 §2.7, G5): the
// contract tables' margin type, settlement asset and face value, the unit
// of a coin-margined contract's quantities, the insurance fund of every
// settlement asset, a coin's contracts closed or reopened at once (§3.5),
// and the launch checklist's two items; merged with the pages'
// (pageMessages.ts).
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
      contracts: "张",
      coins: {
        coin: "币",
        contracts: "合约（U 本位与币本位）",
        close: "关闭",
        reopen: "重新开放",
        help: "按币关闭：该币未下线的 U 本位与币本位合约（交易中或暂停的）一起改为只可撤单——只能减仓，HOUSE 继续报价，持仓可以平掉；重新开放：只可撤单的回到交易中。同一个修改、同样的护栏（延时生效，双人模式下要另一位管理员批准）；生效时逐个改、逐个审计。下线仍在交易品种页逐个合约操作。",
        closeTitle: "关闭 {{coin}} 的合约",
        reopenTitle: "重新开放 {{coin}} 的合约",
        closeNote: "这些合约改为只可撤单：只能减仓，HOUSE 继续报价。",
        reopenNote: "这些合约回到交易中。",
        staying: "不变：{{list}}",
      },
    },
    derivatives: { tabs: { coins: "按币" } },
    changes: { kind: { COIN_CONTRACTS_STATUS: "按币改合约状态" } },
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
      contractsOpen: "开放中 U 本位 {{usdt}} 个、币本位 {{coin}} 个",
      noOpenContracts: "没有开放中的合约",
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
      contracts: "cont.",
      coins: {
        coin: "Coin",
        contracts: "Contracts (USDⓈ-M and COIN-M)",
        close: "Close",
        reopen: "Reopen",
        help: "Close by coin: the coin's USDⓈ-M and COIN-M contracts that are not delisted (trading or halted) go to cancel only together — reduce only, HOUSE keeps quoting, positions can close; reopen: those in cancel only go back to trading. One change under the same guard (it takes effect after the delay and, in two-person mode, once a second administrator approves); each contract moves and is audited on its own. Delisting stays per contract on the instruments page.",
        closeTitle: "Close {{coin}}'s contracts",
        reopenTitle: "Reopen {{coin}}'s contracts",
        closeNote: "These contracts go to cancel only: reduce only, HOUSE keeps quoting. ",
        reopenNote: "These contracts go back to trading. ",
        staying: "Unchanged: {{list}}",
      },
    },
    derivatives: { tabs: { coins: "By coin" } },
    changes: { kind: { COIN_CONTRACTS_STATUS: "Contracts by coin" } },
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
      contractsOpen: "open: {{usdt}} USDⓈ-M, {{coin}} COIN-M",
      noOpenContracts: "no contract open",
      coinMGlobal: "for everyone",
    },
  },
};
