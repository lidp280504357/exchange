# ADR-0018：杠杆负债记成用户名下的负数行（三行记法）

- 状态：已接受（2026-10-06）
- 关联：设计文档 `docs/设计-杠杆交易-2026-10-06.md` §3（协调会话 2026-10-06 01:10–01:23 的决定）、E0 契约 §7；[ADR-0001](0001-ledger-double-entry.md)（余额只能经账本分录改变）、[ADR-0008](0008-money-and-ids.md)（金额十进制）、[ADR-0015](0015-house-liquidity.md)（HOUSE 是对手方）

## 背景

杠杆交易要在账本里同时表达三样东西：用户杠杆账户里的资产、用户欠 HOUSE 的本金、用户欠 HOUSE 的利息。账本是复式记账，每条 journal 按资产之和为 0，余额由表约束兜底（用户账户不为负，不变量 3）。

设计初稿把"借币"写成"用户负债与 HOUSE 借出同增"：用户多一笔负债、HOUSE 的借出科目也增加。两边同号，这条分录记不平。把 HOUSE 的借出记成付出方（HOUSE 余额减少）则要求 HOUSE 真的有这笔钱，而站内资产（ADR-0013）HOUSE 并没有实物库存；利息的应收、实收也要再多一组科目。

## 决策

1. **每个杠杆账户每种资产三行，都记在用户名下**（`owner_type = USER`）：
   - 资产行 `MARGIN_CROSS` / `MARGIN_ISOLATED`：可用 + 订单冻结，≥ 0；
   - 本金行 `MARGIN_CROSS_DEBT` / `MARGIN_ISOLATED_DEBT`：−未还本金，≤ 0；
   - 利息行 `MARGIN_CROSS_INTEREST` / `MARGIN_ISOLATED_INTEREST`：−未还利息，≤ 0。

   负债行不能冻结；逐仓账户三行的 `scope` 列存交易对（如 `BTC-USDT`），其余账户 `scope` 为空，账户唯一键因此加上 `scope`（迁移 ledger 00008）。符号由按类型的 CHECK 约束与对账检查 `MARGIN_ROWS_SIGNED` 双重保证。
2. **借币在用户自己名下平衡**：资产行 +A / 本金行 −A，HOUSE 不出现。HOUSE 借出了多少不是账户余额，而是查询值 −Σ 本金行（按资产）；借贷池上限是配置，可借余量 = 上限 + Σ 本金行。
3. **利息在计息时确认收入**：利息行 −i / 系统科目 `MARGIN_INTEREST_INCOME` +i（整点按资产一笔汇总，每个账户一行）。不设"应收利息"科目：用户没还的利息就是他的利息行。
4. **还币也在用户名下平衡，先息后本**：资产行 −(I+P) / 利息行 +I / 本金行 +P，HOUSE 不动（收入在计息时已确认）。账本强制 I = min(还款额, 所欠利息)（`LEDGER_INTEREST_FIRST`），负债行不能被还成正数（`LEDGER_DEBT_OVERPAID`）。
5. **成交与强平**：订单在杠杆账户上的一方在其资产行结算（`MARGIN_TRADE_SETTLE`，行与现货结算相同）；强平（`MARGIN_LIQUIDATE`）卖出资产还清负债，穿仓的部分由保险基金补足。
6. **对外只暴露两种账户**：接口与事件只有 `MARGIN_CROSS`、`MARGIN_ISOLATED` + `symbol`；`GetBalances`、`BalanceChanged` 仍只有 SPOT/FUTURES，杠杆账户由 margin-service 汇总三行后提供。
7. **不变量**：7 账本的本金行与利息行 = margin-service 借款表（margin-service 对账）；8 `MARGIN_INTEREST_INCOME` = 历次计息之和（`MARGIN_INTEREST_CONSERVED`）；9 三行的符号（`MARGIN_ROWS_SIGNED`）。

## 理由

- 每条分录在一个用户名下就能平衡，复式记账不需要例外，也不要求 HOUSE 持有实物库存。
- 负债是账本里的余额，ADR-0001 的"余额只能经分录改变"同样约束负债：借、计息、还、强平都留下分录，可以逐笔复算，也能直接与 margin-service 的借款表对账。
- 少一个应收科目，每笔还款少两条腿；收入在计息时确认，与按小时计息的业务含义一致。
- 用户的杠杆净资产 = Σ 三行，无需跨所有者汇总。

## 后果

- 账本第一次有"用户名下允许为负"的行：不变量 3 加上例外（负债行只能 ≤ 0），对账与表约束都要按类型区分符号。
- 两站与报表不能再把"非 FUTURES 的行"都当现货累加：杠杆行（含负数行）只经 margin-service 的接口与 `margin` 频道展示，`ledger.events` 的 `EntryPosted` 行带 `scope`。
- 利息收入在计息时已确认，用户穿仓时未还的利息不是 HOUSE 的现金：强平按负债（本金 + 利息）还款，不足由保险基金补。
- 若以后要把 `MARGIN_INTEREST_INCOME` 划到别的科目，不变量 8 的检查要同时修改。

## 备选

- **HOUSE 借出科目 + 用户负债同增**：两边同号，分录记不平（初稿的写法，01:10 否决）。
- **HOUSE 付出、用户收入，负债只记在 margin-service**：负债不在账本里，ADR-0001 管不到；站内资产 HOUSE 没有库存，付出会让系统账户为负。
- **另设应收利息 `MARGIN_INTEREST_RECEIVABLE`，还息时才转收入**：01:10 曾采用，01:23 改为计息时直接记收入——多一个科目、每笔还款多两条腿，信息与用户利息行重复。
