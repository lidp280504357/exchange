// The admin console's strings (Chinese first; operators read Chinese).
export const adminMessages = {
  "zh-CN": {
    admin: {
      title: "Astras 管理后台",
      env: "测试环境",
      login: "登录",
      loggingIn: "登录中…",
      email: "邮箱",
      password: "密码",
      totp: "身份验证器 6 位验证码",
      loginHint: "账号由运维用 exchangectl admin create 创建；连续 5 次失败锁定 15 分钟。",
      logout: "退出",
      nav: {
        overview: "概览", users: "用户", orders: "订单与成交", wallet: "钱包", deposits: "充值", withdrawals: "提现审批",
        custody: "托管方", instruments: "资产与交易对", derivatives: "合约", risk: "风控与开关", ledger: "账本", audit: "审计",
        reports: "报表",
      },
      stats: {
        users: "注册用户", new24h: "24 小时新增", trades24h: "24 小时成交笔数", traders24h: "24 小时活跃交易用户",
        turnover24h: "24 小时成交额", pendingWithdrawals: "待审提现", pendingDeposits: "待确认充值", riskEvents: "24 小时风控事件",
        feed: "行情连接", halted: "因断流暂停的交易对",
      },
      feed: { OK: "正常", DELAYED: "延迟", DOWN: "中断", OFF: "关闭" },
      partial: "部分数据暂时读不到：{{parts}}",
      soon: "该页面在阶段 4 B5 重做；在此之前请用旧后台",
      legacy: "打开旧后台",
      roles: { ADMIN: "管理员", OPERATOR: "运营", FINANCE: "财务", AUDITOR: "审计" },
    },
  },
  en: {
    admin: {
      title: "Astras admin console",
      env: "Test environment",
      login: "Sign in",
      loggingIn: "Signing in…",
      email: "Email",
      password: "Password",
      totp: "6-digit authenticator code",
      loginHint: "Accounts come from exchangectl admin create; five failures lock the account for 15 minutes.",
      logout: "Sign out",
      nav: {
        overview: "Overview", users: "Users", orders: "Orders & trades", wallet: "Wallet", deposits: "Deposits",
        withdrawals: "Withdrawal reviews", custody: "Custodian", instruments: "Assets & pairs", derivatives: "Futures",
        risk: "Risk & flags", ledger: "Ledger", audit: "Audit", reports: "Reports",
      },
      stats: {
        users: "Accounts", new24h: "New in 24 h", trades24h: "Trades in 24 h", traders24h: "Traders in 24 h",
        turnover24h: "Turnover in 24 h", pendingWithdrawals: "Withdrawals to review", pendingDeposits: "Deposits confirming",
        riskEvents: "Risk events in 24 h", feed: "Market feed", halted: "Pairs halted for the feed",
      },
      feed: { OK: "OK", DELAYED: "Delayed", DOWN: "Down", OFF: "Off" },
      partial: "Some figures are unavailable: {{parts}}",
      soon: "This page is rebuilt in phase 4 B5; until then use the previous console",
      legacy: "Open the previous console",
      roles: { ADMIN: "Administrator", OPERATOR: "Operator", FINANCE: "Finance", AUDITOR: "Auditor" },
    },
  },
};
