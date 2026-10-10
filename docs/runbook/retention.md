# 历史数据保留（M1，ADR-0022）

用户 2026-10-10 决定：Postgres 只保留最近 15 天的数据，超过的删除。只删历史、不删状态；幂等键比历史留得久；账本按检查点与键表删，对账照样全 0。决策与理由见 [ADR-0022](../adr/0022-history-retention.md)。

## 怎么跑

```bash
scripts/ops/retention.sh --dry-run            # 只数不删（不拿运维锁）：每表将删的行数与估算大小
scripts/ops/retention.sh                      # 真删：在运维锁下
scripts/ops/retention.sh --only trading,auth  # 只跑这几个库
```

即 user-service 容器里的 `exchangectl retention run [--dry-run] [--days 15] [--key-days 90] [--batch 5000] [--pause 100ms] [--only SCHEMA,...]`：按库打开连接、逐条规则分批删除（每批 5,000 行、各自短事务、批间暂停 100 ms，大表不长时间锁），打印每表的行数、估算腾出的大小与表现有大小；真删后对删过行的表 ANALYZE。`--key-days` 不得小于 `--days`。

每日定时：systemd 的 `exchange-retention.timer`，每日 04:10 UTC（03:30 的备份之后；错过的一次开机后补跑）触发 `exchange-retention.service`，即 `infra/retention/retention.sh`。部署（`server-update.sh`）同步脚本并安装两个单元文件，**只装不启用**；首轮在测试服用 `--dry-run` 证明工具与策略之后（协调会话 2026-10-10 的决定）启用一次：

```bash
ssh exchange sudo systemctl enable --now exchange-retention.timer
```

看状态与下次触发：`ssh exchange systemctl list-timers exchange-retention.timer`。脚本拿服务器上的同一把运维锁（与部署、端到端、演练轮流，最多等一小时），跑完再跑一次 `exchangectl ledger reconcile`，输出追加到 `/opt/exchange/logs/retention.log`。

## 删什么、留什么

窗口：历史 15 天（`--days`）；给 30 天内可能重投的 topic（trade.events、order.commands、derivatives.trade.events、derivatives.order.commands）去重的记录 35 天；幂等键与"重放时要原结果"的记录 90 天（`--key-days`），只有订单冻结与释放的键 35 天（它们只在订单自己的重试里再出现；协调会话 2026-10-10 的决定）。

| 库 | 删（早于窗口） | 留 |
|---|---|---|
| ledger | 日记账与其行、行类型（键进 `journal_keys`、合计进 `checkpoints`）；已终态转账、已释放冻结单、对账运行记录；已结算成交 35 天（合计进检查点）；期货结算与杠杆记账的幂等记录 90 天；已删日记账的键：订单冻结与释放的 35 天，其余 90 天 | 账户、设置；资金费、杠杆利息、托管重置（`custody-reset:`）、无主充值放行（`deposit-release:`）的日记账永久；仍被转账或冻结单引用的日记账 |
| trading | 已终态且已释放的订单；不再保留的订单的成交 | 未终态或未释放的订单及其成交 |
| auth | 登录记录、已吊销会话及其刷新令牌、已决换绑申请 | 身份、密码、验证器、已知设备；过期的验证码与令牌由服务自己清理 |
| users | 状态与类型变更记录 | 用户（含已清理的测试账户行）、同意记录、自选 |
| notify | 通知、已结束的投递与广播、开发收件箱 | 文章；排队或重试中的投递 |
| wallet | 已终态提现及其广播尝试（有托管费待人处理的除外）、已处理的托管回调、链上核对（每网络每资产留最新一条）、已入账或核销的链上手续费、已完成的命令与归集、每日价格；充值 90 天（它的交易是区分迟到回调与新充值的依据；有异议或无主未决的不删） | 地址、退役地址的归属、地址簿、nonce、游标、托管基线与费用单位、停提开关、平台注资记录 |
| admin | 已决审批、结束的后台会话、已结的交易对变更 | 管理员、用户备注与标签、设置、上传 |
| risk | 评估记录、频次事件 | 用户设备 |
| instrument | 参考数据变更记录 | 资产、网络、交易对、合约、费率、平台资料 |
| config | 开关变更（每个开关保留最新 50 条，后台的只减仓窗口读它） | 开关 |
| marketsim | 价格采样、已结束的价格事件、参数变更（留最新一条） | 机器人、设置、模型状态 |
| marketmaker | HOUSE 上限变更（留最新一条） | 生效的上限 |
| signer | 签名与拒签记录 | — |

outbox、inbox、HTTP 幂等键不在这里：各服务的 janitor 已按 7 天、35 天与幂等键的 TTL 清理（`internal/platform/bootstrap`）。matching 的 WAL 由引擎在快照后自己删。derivatives、margin、market 三个库的规则由合约后端会话维护（各自 postgres 适配器的 `Retention`）。

## 账本怎么删

- **只有保留工具能删**：账本的只追加触发器（ledger 00012）只在会话设置 `ledger.retention = on` 时放行 DELETE；工具在自己的每个批次事务里 `SET LOCAL`。UPDATE、TRUNCATE 与其他任何路径的 DELETE 仍报 `ledger entries are append-only (ADR-0001)`。
- **检查点**（`ledger.checkpoints`，与删除同一事务累加）：`ACCOUNT_AVAILABLE`/`ACCOUNT_FROZEN` 按账户（被删行的金额合计）、`TRADE_SETTLE_BOOKED`/`TRADE_FEE_BOOKED` 按资产（被删的现货成交入账与手续费收入）、`TRADE_SETTLE_EXPECTED`/`TRADE_FEE_EXPECTED` 按资产与 `TRADES` 按交易对（被删成交的金额与笔数）。对账的 ACCOUNT_MATCHES_LINES、TRADES_NUMBERED、TRADE_SETTLE_MATCHES_TRADES、TRADE_FEE_MATCHES_TRADES 把它们加进来。
- **键表**（`ledger.journal_keys`）：被删日记账的键、请求摘要、ID 与序号。入账查重先查 journals、再查键表：同键同内容仍当重放返回原日记账 ID，内容不同仍是幂等冲突。
- 删完必须：`exchangectl ledger reconcile` 13 项全 0；合约与杠杆的对账（`exchangectl derivatives reconcile`、`exchangectl margin reconcile`）照常。

## 空间

删除腾出的页由 autovacuum 复用，库文件不收缩；稳态（15 天）与眼下同一量级，不上 pg_repack。首轮大删之后若要把空间还给磁盘，在部署窗口里对大表做一次 `VACUUM FULL`（锁表；journals 约 1 GB 时一分钟量级），之后不必再做。

## ClickHouse

读模型同样只留 15 天（clickhouse 00013 的 TTL，按时间自动删除）：events、event_ingest_log、ledger_entries、orders、order_updates、trades、derivatives_fills、derivatives_funding、derivatives_liquidations、margin_interest、audit_logs。不设 TTL：orders_state（订单当前状态，后台按交易对数挂单读它；它由 orders 与 order_updates 经物化视图写入，原表过期不影响它）、按 ID 的持仓/充值/提现/杠杆强平状态表、K 线（candles_1m，图表要历史）；futures_liquidations 保持 7 天。后台报表选超过 15 天的区间只看得到最近 15 天。

## 排查

- 某表删到一半出错：已删的批次已提交（检查点与键随批次一起提交），重跑即可，规则是幂等的。
- 对账出现差异：先看 `checkpoints` 是否随删除累加（`SELECT name, count(*), sum(count) FROM ledger.checkpoints GROUP BY 1`），再按差异的账户或资产对照剩余流水。
- 重放被当成新入账：查 `journal_keys` 是否还有该键（`--key-days` 之外的键已删）。
