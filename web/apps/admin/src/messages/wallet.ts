// The strings of the deposit and withdrawal pages added by the 2026-10-02
// rebuild (batch review, holds, deposits that need a person), merged into
// the console's messages in i18n.ts.
export const walletZh = {
  admin: {
    batch: {
      selected: "已选 {{n}} 笔，折合 {{value}} USDT",
      selectLowRisk: "选中低风险（风控分 < {{max}}）",
      clear: "清除选择",
      approve: "批量批准",
      reject: "批量拒绝",
      approveTitle: "批量批准 {{n}} 笔提现",
      rejectTitle: "批量拒绝 {{n}} 笔提现",
      hint: "每笔单独审核并各自记入审计；某一笔失败不影响其它。单人模式下，不超过单人可批准上限的提现一人批准即完成。",
      done: "已处理 {{ok}} 笔",
      partly: "已处理 {{ok}} 笔，{{failed}} 笔未处理",
      failedLine: "…{{id}}：{{message}}",
    },
    withdrawalDetail: {
      recent: "该用户最近的提现",
      noRecent: "没有别的提现",
      callbacks: "托管方回调",
      noCallbacks: "还没有回调",
    },
  },
};

export const walletEn = {
  admin: {
    batch: {
      selected: "{{n}} selected, worth {{value}} USDT",
      selectLowRisk: "Select the low-risk ones (score < {{max}})",
      clear: "Clear the selection",
      approve: "Approve all",
      reject: "Reject all",
      approveTitle: "Approve {{n}} withdrawals",
      rejectTitle: "Reject {{n}} withdrawals",
      hint: "Each is reviewed on its own and audited; one that fails leaves the others. In single-person mode, one approval completes a withdrawal up to the single-person limit.",
      done: "{{ok}} done",
      partly: "{{ok}} done, {{failed}} not",
      failedLine: "…{{id}}: {{message}}",
    },
    withdrawalDetail: {
      recent: "The user's recent withdrawals",
      noRecent: "No other withdrawals",
      callbacks: "Custodian callbacks",
      noCallbacks: "No callbacks yet",
    },
  },
};
