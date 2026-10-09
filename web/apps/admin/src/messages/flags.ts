// The feature flags page's strings (user 2026-10-10: the descriptions were
// English): each known flag's description in Chinese by its key (the
// backend's English one stays the English, and any flag not listed here
// shows the backend's), and the rules' words; merged with the pages'
// (pageMessages.ts).
export const flagsZh = {
  admin: {
    flagDesc: {
      account: { transfer: "现货与合约账户之间的划转" },
      admin: {
        login_without_totp: "管理后台只凭密码登录：不询问也不校验身份验证器验证码（仅测试环境）",
        two_person_approval: "管理后台：手工调账、保险基金注资与需双人复核的提现，须由第二位管理员批准；关闭时一位管理员在单人限额内即可完成",
      },
      auth: { sms: "短信作为注册与登录渠道（高风险地区仍只用邮箱）" },
      derivatives: {
        trading: "永续合约交易（阶段 3）",
        coin_m: "币本位永续合约（BTC-USD-PERP 等，以标的币结算）：按用户或地区开放下单、持仓以及 BTC、ETH、ASTRA 的合约账户（资格 COIN_M_TRADE；设计 2026-10-06 §2）",
      },
      ledger: {
        manual_adjustment: "运营手工发放模拟资金（MANUAL_ADJUSTMENT）",
        welcome_credit: "新注册用户获得模拟体验资金（仅测试环境）",
      },
      margin: {
        enabled: "杠杆交易：划入杠杆账户、向 HOUSE 借币与杠杆账户下单；关闭时这些请求答 MARGIN_DISABLED，还款与划回现货照常（设计 2026-10-06）",
        liquidation: "杠杆强平：到达强平线的账户被冻结、与 HOUSE 平仓并偿还负债；关闭时只预警（设计 2026-10-06 §4.5）",
        auto_borrow: "杠杆账户里 side_effect 为 AUTO_BORROW 的订单自动借入可用余额不足的部分（设计 2026-10-06 §5.1）",
      },
      market: {
        reference_kline: "按交易对改用参考市场的 K 线，不用平台自己的（ADR-0010）",
        reference_feed: "接入币安公开数据作为外部参考价；取得数据授权之前只用于测试环境（§11.9）",
        maker: "已随 ADR-0015 退役：§11.10 的挂单做市机器人，由 HOUSE 虚拟流动性（market.house_liquidity）取代",
        reference_ticker: "行情（最新价、24 小时统计、买一卖一）改用参考市场的，不用平台自己的（ADR-0010）",
        halt_on_feed_loss: "跟随参考市场的交易对连续 5 分钟收不到参考数据时暂停，数据恢复后自动恢复（ADR-0010）",
        reference_depth: "按交易对改用参考市场的盘口与公开成交，不用平台自己的（ADR-0010）",
        house_liquidity: "按交易对由 HOUSE 以参考市场的盘口与用户订单成交（虚拟流动性）；关闭时只用平台自己的盘口（ADR-0015）",
        internal_matching: "在有 HOUSE 流动性的交易对上，用户订单之间也可互相成交；关闭时每笔成交的对手方都是 HOUSE（ADR-0015）",
        flat_minutes: "没有参考市场跟随的交易对（平台币及其永续）在无成交的每一分钟也记一根平的一分钟 K 线（沿用上一收盘价、成交量为 0），供图表与 ClickHouse 使用",
        reference_mark: "按合约改用所跟随的币安合约的标记价、指数价与资金费率，平台自算的作为数据流中断时的后备（设计 2026-10-06 §3.1）",
        futures_data: "从币安读取合约统计（持仓量、多空比、主动买卖量、基差、资金费率历史）与爆仓数据流；关闭后已存的数据照常提供（设计 2026-10-06 §3.3）",
        overlay: "跟随币安的交易对的价格事件：market-data 按 market-sim 每秒推送的乘数平移该交易对的参考盘口、成交、行情与 K 线（勾选 risk 时连同其永续的指数价与标记价）；关闭时所有乘数立即回到 1（设计 2026-10-07 通用价格控制）",
      },
      product: {
        spot: "币币交易产品线：关闭后两站隐藏，新单一律拒绝（PRODUCT_CLOSED；杠杆账户只放行还款），撤单与杠杆强平照常，系统撤销其挂单，模拟市场的现货机器人暂停；HOUSE 与做市账户继续报价；默认开、不可删除（设计 2026-10-07 产品线开关）",
        usdt_m: "U 本位合约产品线：关闭后两站隐藏，只接受只减仓的平仓单与撤单（新条件单也拒绝），系统撤销挂单与条件单、拒绝划入；HOUSE 与做市账户继续报价，持仓可以平掉，资金费与强平照常；默认开、不可删除（设计 2026-10-07 产品线开关）",
        coin_m: "币本位合约产品线，关闭时与 U 本位相同；默认开、不可删除（设计 2026-10-07 产品线开关）",
      },
      risk: { enforce: "执行风控规则的处置（评分需要复核的账户转为 RISK_REVIEW）；关闭时只记录评分" },
      sim: {
        enabled: "平台币模拟市场（market-sim）：机器人围绕模型价格在 ASTRA-USDT 上报价与成交；关闭时撤销它们的挂单",
        events: "运营在模拟市场发起的价格事件（跳变、目标价、趋势、暂停）",
        perp: "模拟市场的机器人同时为平台币永续合约做市",
        halt_on_loss: "模拟市场心跳停止 1 分钟后暂停该交易对（连同其永续），心跳恢复后自动恢复",
      },
      wallet: {
        withdraw: "提现总闸（阶段 2）",
        test_assets: "允许规则内的用户充提隐藏的测试资产（ADR-0017）：即端到端测试的账户（测试服为地区 AQ）；关闭时谁都不能",
      },
    },
    flagRules: {
      allow: "允许",
      deny: "排除",
      dims: { regions: "地区", statuses: "账户状态", assets: "资产", symbols: "交易对", users: "用户" },
    },
  },
};

export const flagsEn = {
  admin: {
    flagRules: {
      allow: "allow",
      deny: "deny",
      dims: { regions: "Regions", statuses: "Account statuses", assets: "Assets", symbols: "Pairs", users: "Users" },
    },
  },
};
