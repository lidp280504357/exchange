# 历史数据保留（M1，ADR-0022）

用户 2026-10-10 决定：Postgres 只保留最近 15 天的数据，超过的删除。只删历史、不删状态；幂等键比历史留得久；账本按检查点与键表删，对账照样全 0。决策与理由见 [ADR-0022](../adr/0022-history-retention.md)。

## 怎么跑

```bash
scripts/ops/retention.sh --dry-run            # 只数不删（不拿运维锁）：每表将删的行数与估算大小
scripts/ops/retention.sh                      # 真删：在运维锁下
scripts/ops/retention.sh --only trading,auth  # 只跑这几个库（clickhouse 指读模型）
```

即 `exchangectl retention run [--dry-run] [--days 15] [--key-days 90] [--batch 5000] [--pause 100ms] [--only SCHEMA,...] [--force]`，在服务镜像的一次性容器里（`docker compose run --rm --no-deps user-service`，与服务同样的设置，不占 user-service 自己的容器）：按库打开连接（`application_name` 为 `exchangectl-retention`）、逐条规则分批删除（每批 5,000 行、各自短事务、批间暂停 100 ms，按条件生成的批次 `FOR UPDATE SKIP LOCKED`，被别的事务锁着的行留到下次），打印每表的行数、估算腾出的大小（行数占表的份额，估算）与表现有大小；真删后对删过行的表 ANALYZE。一个库失败不挡后面的库：失败的列在最后，整次运行以非零退出。ClickHouse 那一步在各库之后、自己连接（30 分钟读超时，等轻量删除的变更做完）：ClickHouse 不可用只让这一步失败；没有 `CLICKHOUSE_ADDR` 时这一步写 `SKIPPED`、不算失败（B201）。被停止（SIGTERM）的一次运行做完手头那个库就结束、以非零退出。`--days` 少于 7 要加 `--force`；`--key-days` 不得小于 `--days`。

容器有名字（定时任务的叫 `exchange-retention`，`scripts/ops/retention.sh` 的每次一个 `exchange-retention-ops-<时间>`）：`docker compose run` 自己被停时只断开、容器照跑（B201），所以停止单元（`sudo systemctl stop exchange-retention.service`，`KillMode=mixed`）时脚本先 `docker stop` 这个容器（exchangectl 收到 SIGTERM，取消手头的语句；已提交的批次与它们的检查点、快照、键都留着，下次接着删）、删掉它再退出——每一步都在后台跑、脚本等它，信号随到随处理（2026-10-10 测试服实测：删除中途停止 1 秒结束、不留容器）；停在之后的对账时，对账在 ledger-service 里自己跑完（只读）；`scripts/ops/retention.sh` 里 Ctrl-C 同样经 ssh 停掉服务器上的容器。

每日定时：systemd 的 `exchange-retention.timer`，每日 04:10 UTC（03:30 的备份之后；错过的一次开机后补跑）触发 `exchange-retention.service`（`TimeoutStartSec=3h`），即 `infra/retention/retention.sh`。部署（`server-update.sh`）同步脚本并安装两个单元文件，**只装不启用**；启用（协调会话 2026-10-10 的顺序：测试服 `--dry-run` 证明工具与策略、审查 LK/LL/LM 的待修上线之后）：

```bash
ssh exchange sudo systemctl enable --now exchange-retention.timer
```

看状态与下次触发：`ssh exchange systemctl list-timers exchange-retention.timer`；输出：`ssh exchange journalctl -u exchange-retention`，同时追加在 `/opt/exchange/logs/retention.log`（超过 10 MB 轮转一份；日志目录与下面的指标目录由部署建好，脚本也会自建，`/opt/exchange` 属 ubuntu）。脚本拿服务器上的同一把运维锁（与部署、端到端、演练轮流，最多等一小时），跑完再跑一次 `exchangectl ledger reconcile`；**保留与对账都成功**（对账 13 项全 0）才把完成时间写进 `infra/metrics/exchange_retention.prom`——对账有差异时这次不算成功，两天后同样告警，差异本身另有 `ledger_reconcile_mismatches` 的告警；`--dry-run` 不删，不写这个时间（不算当天那次）。这个目录是 node exporter 的 textfile 目录：node exporter 要带 `--collector.textfile.directory=/opt/exchange/infra/metrics`（采集端与 node exporter 都还没部署，参数写在 `deploy/observability/prometheus.yml` 的 node 任务旁，见 [observability.md](observability.md)）。两天没成功告警 `RetentionRunStale`；完全没有这个指标 26 小时告警 `RetentionMetricAbsent`（node exporter 没读这个目录，或定时器没启用、从没跑成过）。测试服眼下没有采集端，这两条告警不会响：每天看 `journalctl -u exchange-retention` 或 `retention.log` 的最后一次（`== … done` 一行）。

## 删什么、留什么

窗口：历史 15 天（`--days`）；给 30 天内可能重投的 topic（trade.events、order.commands、derivatives.trade.events、derivatives.order.commands）去重的记录 35 天；幂等键与"重放时要原结果"的记录 90 天（`--key-days`），只有订单冻结与释放的键 35 天（它们只在订单自己的重试里再出现；协调会话 2026-10-10 的决定）。

| 库 | 删（早于窗口） | 留 |
|---|---|---|
| ledger | 日记账与其行、行类型（键进 `journal_keys`、合计进 `checkpoints`、每账户最新被删的一行进 `account_snapshots`）；已终态转账、已释放冻结单、对账运行记录；已结算成交 35 天（合计进检查点）；期货结算与杠杆记账的幂等记录 90 天；已删日记账的键：订单冻结与释放的 35 天，其余 90 天 | 账户、设置；资金费、杠杆利息、托管重置（`custody-reset:`）、无主充值放行（`deposit-release:`）的日记账永久；仍被转账或冻结单引用的日记账 |
| trading | 已终态且已释放（或从未冻结、被账本拒绝）的订单；不再保留的订单的成交（按 trading 00010 的 `orders_finished`、`fills_executed` 索引分批） | 未终态或未释放的订单及其成交 |
| auth | 登录记录、已吊销会话及其刷新令牌、闲置早于窗口且刷新令牌都已过期（被服务清掉）的会话、已决换绑申请 | 身份、密码、验证器、已知设备；过期的验证码与令牌由服务自己清理 |
| users | 状态与类型变更记录 | 用户（含已清理的测试账户行）、同意记录、自选 |
| notify | 通知、已结束的投递与广播、开发收件箱 | 文章；排队或重试中的投递 |
| wallet | 已处理且已入账、忽略或拒绝的托管回调、链上核对（每网络每资产留最新一条）、已入账或核销的链上手续费、已完成的命令与归集、每日价格；90 天：已终态提现及其广播尝试（用户的月度提现限额按当月提现求和；有托管费待人处理的除外）、失败/未匹配/有差异的托管回调（可由人重放）、充值（它的交易是区分迟到回调与新充值的依据；有异议或无主未决的不删） | 地址、退役地址的归属、地址簿、nonce、游标、托管基线与费用单位、停提开关、平台注资记录；扫描过的区块由扫描器自己按重组窗口删（`Blocks.Prune`） |
| admin | 已决审批、结束的后台会话、已结的交易对变更 | 管理员、用户备注与标签、设置、上传 |
| risk | 评估记录 | 用户设备；频次事件由服务自己按规则窗口（最长 30 天）清理 |
| instrument | 参考数据变更记录，但每个对象每个键留最新一条与最新一次编辑（文件或后台的，`LastEdit` 读它，之后的状态或资料变更不顶掉它；部署时 `instruments apply` 靠它保留后台改过的费率与限额，B201） | 资产、网络、交易对、合约、费率、平台资料 |
| config | 开关变更（每个开关保留最新 50 条，后台的只减仓窗口读它） | 开关 |
| marketsim | 价格采样、已结束（按结束时间）的价格事件、参数变更（留最新一条） | 机器人、设置、模型状态 |
| marketmaker | HOUSE 上限变更（留最新一条） | 生效的上限 |
| signer | 拒签记录；签名记录 90 天（重复的签名请求按它作答） | — |

outbox、inbox、HTTP 幂等键不在这里：各服务的 janitor 已按 7 天、35 天与幂等键的 TTL 清理（`internal/platform/bootstrap`）。matching 的 WAL 由引擎在快照后自己删。derivatives、margin、market 三个库的规则由合约后端会话维护（各自 postgres 适配器的 `Retention`）。

## 账本怎么删

- **只有保留工具会删**：账本的只追加触发器（ledger 00012）只在会话设置 `ledger.retention = on` 时放行 DELETE；工具在自己的每个批次事务里 `SET LOCAL`。UPDATE、TRUNCATE 与其他任何路径的 DELETE 仍报 `ledger entries are append-only (ADR-0001)`。这个设置是普通的会话变量，只防误操作、不是权限边界（各服务共用一个数据库角色，见 ADR-0022）。00012 的 Down 在删过以后拒绝回退（键与检查点代表已删的那部分）。
- **检查点**（`ledger.checkpoints`，与删除同一事务累加）：`ACCOUNT_AVAILABLE`/`ACCOUNT_FROZEN` 按账户（被删行的金额合计）、`TRADE_SETTLE_BOOKED`/`TRADE_FEE_BOOKED` 按资产（被删的现货成交入账与手续费收入）、`TRADE_SETTLE_EXPECTED`/`TRADE_FEE_EXPECTED` 按资产与 `TRADES` 按交易对（被删成交的金额与笔数）。对账的 ACCOUNT_MATCHES_LINES、TRADES_NUMBERED、TRADE_SETTLE_MATCHES_TRADES、TRADE_FEE_MATCHES_TRADES 把它们加进来。
- **账户快照**（`ledger.account_snapshots`，ledger 00013）：每个账户最新被删的那一行删后的余额与版本。SNAPSHOT_MATCHES_ACCOUNT 拿账户与 {剩余最新一行, 快照} 中版本较高的比：闲置账户的新行删光后，剩下的最新一行可能是更早的永久日记账（资金费、托管重置）。
- **键表**（`ledger.journal_keys`）：被删日记账的键、请求摘要、ID 与序号。入账查重先查 journals、再查键表：同键同内容仍当重放返回原日记账 ID，内容不同仍是幂等冲突。键也只留它的窗口：后台手工调整、`fund:` 这类键 90 天后不再拦重复，同一个键再提交会再入一次账（那时原日记账早已不在）。
- 删完必须：`exchangectl ledger reconcile` 13 项全 0；合约与杠杆的对账（`exchangectl derivatives reconcile`、`exchangectl margin reconcile`）照常。

## 空间

删除腾出的页由 autovacuum 复用，库文件不收缩；稳态（15 天）与眼下同一量级，不上 pg_repack。首轮大删之后若要把空间还给磁盘，在部署窗口里对大表做一次 `VACUUM FULL`（锁表；journals 约 1 GB 时一分钟量级），之后不必再做。备份（`pg-backup.sh`，7 天）只管 Postgres。

## ClickHouse

读模型同样只留 15 天（clickhouse 00013 的 TTL，按时间自动删除）：events、event_ingest_log、ledger_entries、orders、order_updates、derivatives_fills、derivatives_funding、derivatives_liquidations、margin_interest。后台报表要加总"本期之前"的，不删：注册事件（用户报表的"此前注册"）、账本里的杠杆利息行（利息报表结转的"欠息"）与现货成交表 trades（HOUSE 报表本期之前的持仓与资金，约 1 MB/天）；审计日志 audit_logs 是后台唯一的审计记录、量小，不删（clickhouse 00014，协调会话的决定）。不设 TTL：orders_state（订单当前状态，后台按交易对数挂单读它；按状态设 TTL 会删掉一个订单的最新版本、让旧版本回来）——由保留工具（`--only clickhouse`，或整次运行）每天按 order_id 轻量删除 15 天前已终态、且最后一次变更也在 15 天前的订单的全部行（挂了很久、最近才撤的单照样留 15 天，B201；dry-run 数的是这些行）；按 ID 的持仓/充值/提现/杠杆强平状态表（同理）、K 线（candles_1m，图表要历史）；futures_liquidations 保持 7 天。后台报表选超过 15 天的区间只看得到最近 15 天（上面几项"本期之前"的合计除外）；15 天前的状态读模型也不能再从 events 重建（它们已过期）。

后台「谁持有」卡（各资产的持有人与系统账户）不再从 ledger_entries 加总：ledger-service 的 `ListHolders` 读账户当前余额（B199，后台会话改读，A123）——列出最大的 `limit` 个持有人（最多 1,000；0 或不传为全部，与 B201 之前一样，B202），另给 `apart_user_ids`（后台按类型传机器人、系统与测试账户，最多 1,000 个）与其余用户各自的合计与持有人数（B201）；前 N 与两个合计出自同一条语句、同一时刻（B202）。系统账户照旧读 `GetSystemBalances`。

## 排查

- 某表删到一半出错：已删的批次已提交（检查点、快照与键随批次一起提交），重跑即可，规则是幂等的；其余库照跑，失败的在输出末尾。
- 对账出现差异：先看 `checkpoints` 是否随删除累加（`SELECT name, count(*), sum(count) FROM ledger.checkpoints GROUP BY 1`），再按差异的账户或资产对照剩余流水与 `account_snapshots`。
- 重放被当成新入账：查 `journal_keys` 是否还有该键（窗口之外的键已删）。
- `RetentionRunStale`：`journalctl -u exchange-retention` 看最后一次的输出（保留失败，还是之后的对账有差异），修好后 `sudo systemctl start exchange-retention.service` 补跑一次。`RetentionMetricAbsent`：先看 `systemctl list-timers exchange-retention.timer` 与 `ls /opt/exchange/infra/metrics`，再看 node exporter 的参数。
- 停下正在跑的一次：`sudo systemctl stop exchange-retention.service`（脚本停掉 `exchange-retention` 容器再退出；`sudo docker ps --filter name=exchange-retention` 应为空）。名字被一个仍在运行的旧容器占着时，新的一次以失败结束、不去停它：先确认它是什么再 `sudo docker stop`。
