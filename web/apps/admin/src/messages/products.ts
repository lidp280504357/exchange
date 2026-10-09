// The strings of the product lines (design 2026-10-07, product switches,
// batch K3): the card on 资产与交易对 and the checklist's item, merged into
// the console's messages by pageMessages.ts.
export const productsZh = {
  admin: {
    launch: {
      items: {
        products: {
          name: "产品线",
          source: "资产与交易对 → 产品线（开关 product.spot、product.usdt_m、product.coin_m）",
          required: "仅供参考：列出开着的产品线，关着的也不算未就绪",
        },
      },
    },
    products: {
      title: "产品线",
      hint: "币币交易、U 本位合约、币本位合约各一个开关，只能开或关、不能删除。关闭后两站一分钟内看不到它、新单一律拒绝（只放行撤单与只减仓的平仓单），系统撤销它的全部挂单（合约含条件单）；HOUSE 与做市账户照常报价，持仓可以平仓，资金费、强平与对账照常，仍有仓位或挂单的用户可在资产页处置。重新打开即恢复，撤掉的挂单不回来。",
      lines: { spot: "币币交易", usdt_m: "U 本位合约", coin_m: "币本位合约" },
      open: "开放中", closed: "已关闭",
      since: "{{who}} · ",
      never: "未切换过",
      orders: "挂单 {{n}}", positions: "持仓 {{n}}", debts: "欠款的杠杆账户 {{n}}", unknown: "未知",
      ordersHint: "运行这条线的服务数出的挂单与持仓，不含 HOUSE 与做市账户：合约的挂单含止盈止损；现货的挂单含杠杆账户的单（含关闭后保留的还款单，不含强平单），持仓是欠款的杠杆账户",
      close: "关闭", reopen: "打开", cancelLeft: "撤销剩余挂单",
      closeTitle: "关闭「{{line}}」",
      closeHint: "关闭后：两站一分钟内隐藏它；新单一律拒绝，只放行撤单与只减仓的平仓单；系统撤销它的全部挂单（约 {{orders}} 笔，合约含条件单）。{{positions}} 个持仓保留，只能减仓或平仓（HOUSE 照常报价作对手方），资金费与强平照常。确认词为该产品线的代号。",
      closeSpotHint: "关闭后：两站一分钟内隐藏它；新单一律拒绝，只放行撤单与杠杆账户的还款单（强平照常）；系统撤销现货与杠杆账户的挂单（现有约 {{orders}} 笔，其中的还款单保留）；做市账户照常报价。{{positions}} 个欠款的杠杆账户照常计息、可以还款。确认词为 spot。",
      closeSpotAstra: "关闭币币交易也会让平台币 ASTRA 现货停牌，以它为指数的 ASTRA 永续按指数规则进入只减仓。",
      openTitle: "打开「{{line}}」",
      openHint: "打开后两站一分钟内重新显示、可以下单，HOUSE 恢复报价；关闭期间撤掉的挂单不会恢复。确认词为该产品线的代号。",
      cancelLeftTitle: "撤销「{{line}}」剩余的挂单",
      cancelLeftHint: "产品线已关闭，但还有 {{orders}} 笔挂单（上次撤单失败或超时、撤单接口那时还没上线，或是现货杠杆账户的还款单——还款单不撤）：再撤一次，开关不变。",
      closedDone: "已关闭，撤销挂单 {{n}} 笔",
      openedDone: "已打开",
      leftDone: "已撤销挂单 {{n}} 笔",
      cancelFailed: "开关已关，但撤单没有完成（{{why}}）：已撤 {{n}} 笔。卡片上的剩余挂单数稍后更新，点「撤销剩余挂单」再撤一次。",
      cancelUnavailable: "开关已关，但这条线的服务还不能撤单，挂单没有撤：服务支持后点「撤销剩余挂单」。",
      cancelLeftUnknown: "产品线已关闭，但挂单数暂时读不到：再撤一次，开关不变（现货杠杆账户的还款单不撤）。",
      why: {
        TIMEOUT: "15 秒内没答完，服务可能还在撤",
        UNREACHABLE: "连不上该服务",
        PARTIAL: "{{users}} 个用户的挂单没撤成",
        REFUSED: "服务答：{{error}}",
      },
      partial: "部分产品线的挂单或持仓数暂时读不到",
      flagsHint: "产品线在「资产与交易对 → 产品线」切换（会撤销挂单），不在这里",
    },
  },
};

export const productsEn = {
  admin: {
    launch: {
      items: {
        products: {
          name: "Product lines",
          source: "Assets and pairs → Product lines (flags product.spot, product.usdt_m, product.coin_m)",
          required: "For information: lists the lines open; one closed does not make the platform unready",
        },
      },
    },
    products: {
      title: "Product lines",
      hint: "Spot trading, USDT-margined and coin-margined contracts each have a switch: open or closed, never deleted. Closed, a line leaves the sites within a minute, refuses new orders but cancels and reduce-only closes, has all its open orders canceled (conditional ones too); HOUSE and the market makers keep quoting so positions can close, and funding, liquidations and the reconciliation go on, and users with positions or orders in it handle them from their assets page. Opening it again restores it; canceled orders stay canceled.",
      lines: { spot: "Spot trading", usdt_m: "USDT-margined contracts", coin_m: "Coin-margined contracts" },
      open: "Open", closed: "Closed",
      since: "{{who}} · ",
      never: "never switched",
      orders: "{{n}} orders", positions: "{{n}} positions", debts: "{{n}} margin accounts owing", unknown: "unknown",
      ordersHint: "The open orders and positions as the line's service counts them, HOUSE's and the market makers' left out: a contract's orders with its take-profits and stop-losses; spot's with the margin accounts' (repayments, which a closed line keeps, in; liquidations out), its positions the margin accounts owing",
      close: "Close", reopen: "Open", cancelLeft: "Cancel the orders left",
      closeTitle: "Close “{{line}}”",
      closeHint: "Closed: it leaves the sites within a minute; new orders are refused but cancels and reduce-only closes; all its open orders are canceled (about {{orders}}, conditional ones too). Its {{positions}} positions stay, to be reduced or closed only (HOUSE keeps quoting as the counterparty), with funding and liquidations as before. Type the line's code to confirm.",
      closeSpotHint: "Closed: it leaves the sites within a minute; new orders are refused but cancels and the margin accounts' repayments (liquidations go on); the open orders of spot and margin accounts are canceled (about {{orders}} now, the repayments among them kept); the market makers keep quoting. Its {{positions}} margin accounts owing go on accruing and can repay. Type spot to confirm.",
      closeSpotAstra: "Closing spot also halts the platform coin's ASTRA spot pair; the ASTRA perpetuals indexed on it go reduce-only by their index rules.",
      openTitle: "Open “{{line}}”",
      openHint: "Open, it shows on the sites again within a minute, takes orders and HOUSE quotes it again; the orders canceled while it was closed do not come back. Type the line's code to confirm.",
      cancelLeftTitle: "Cancel the orders left on “{{line}}”",
      cancelLeftHint: "The line is closed but {{orders}} orders are still open (the last cancel failed or ran out of time, the service could not cancel then, or they are spot margin repayments, which stay): cancel them again; the switch stays.",
      closedDone: "Closed, {{n}} orders canceled",
      openedDone: "Opened",
      leftDone: "{{n}} orders canceled",
      cancelFailed: "Closed, but the cancel did not finish ({{why}}): {{n}} orders canceled. The card's count of the orders left catches up shortly; cancel them again with “Cancel the orders left”.",
      cancelUnavailable: "Closed, but the line's service cannot cancel yet: no order was canceled. Once it can, use “Cancel the orders left”.",
      cancelLeftUnknown: "The line is closed but its orders cannot be counted now: cancel them again; the switch stays (spot margin repayments stay).",
      why: {
        TIMEOUT: "no answer within 15 seconds; the service may still be canceling",
        UNREACHABLE: "the service could not be reached",
        PARTIAL: "{{users}} users' orders were not canceled",
        REFUSED: "the service answered {{error}}",
      },
      partial: "Some lines' orders or positions cannot be counted now",
      flagsHint: "Product lines are switched on Assets and pairs → Product lines (which cancels their orders), not here",
    },
  },
};
