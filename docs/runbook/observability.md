# 可观测性：指标、告警、日志与追踪

实施计划 §9.2 要求每阶段交付"指标与告警清单、日志字段说明、trace 查询方法和回滚步骤"；需求 §12.2。现状：

- **指标**：每个服务在运维端口（[server-deploy.md](server-deploy.md) 的 90xx）暴露 Prometheus `/metrics`。还没有采集端（Grafana Cloud 暂缓，[grafana-cloud.md](grafana-cloud.md)）；采集配置与告警规则已写好：`deploy/observability/prometheus.yml`（每个服务一个 job，告警按 `job` 区分服务）、`alerts.yml`（46 条），CI 用 promtool 校验语法并跑规则单测 `alerts_test.yml`。
- **日志**：结构化，写 stdout，容器日志驱动 json-file（每容器 20 MB × 5 个文件）；测试服为 JSON，本机默认文本（`LOG_FORMAT` 可改）。
- **追踪**：HTTP、gRPC、事件（信封里的 `traceparent`）全链路传递 W3C trace context，span 暂不导出；trace ID 出现在响应头 `X-Trace-Id`、错误体 `trace_id`、每条日志的 `trace_id`、ClickHouse `events.correlation_id`。

## 指标清单

所有服务：`exchange_build_info{service,version}`（恒为 1，标识版本）、Go 运行时 `go_*` 与进程 `process_*`。

| 指标 | 标签 | 来源 | 含义 |
|---|---|---|---|
| `http_server_requests_total` | method, route, status | 有 REST 的服务 | 请求数；错误率 = 5xx / 全部；429 即限流 |
| `http_server_request_duration_seconds` | method, route, status | 同上 | 延迟直方图 |
| `grpc_server_handled_total`、`grpc_server_handling_seconds` | method, code | 有 gRPC 的服务 | 服务间调用 |
| `outbox_pending`、`outbox_published_total`、`outbox_publish_failures_total` | — | auth、user、notification、instrument、ledger | 待发事件积压、已发、发送失败 |
| `kafka_consumer_records_total` | group, topic, result（ok/retry/dlq/skipped） | 各消费者 | 消费结果；`result="dlq"` 即进入死信 |
| `kafka_consumer_lag` | group, topic, partition | 同上（启动即刷新，之后每 30 秒） | 消费积压 |
| `kafka_consumer_handle_seconds` | group, topic | 同上 | 处理耗时 |
| `flags_last_refresh_timestamp_seconds` | — | 使用功能开关的服务 | 开关最近一次刷新 |
| `ws_connections`、`ws_pushed_total` | channel | api-gateway | WebSocket 连接与推送 |
| `auth_otp_requests_total` | scene, channel, outcome | auth-service | `outcome="queued"` 即验证码发送量（[otp.md](otp.md)） |
| `auth_otp_verifications_total` | outcome | auth-service | 验证结果 |
| `notify_sends_total` | channel, provider, result | notification-service | 各服务商发送结果 |
| `notify_provider_circuit_open` | provider | 同上 | 1 = 熔断中 |
| `notify_deliveries_failed_total` | channel | 同上 | 所有服务商都失败的投递 |
| `ledger_reconcile_mismatches` | check | ledger-service | 最近一次对账各检查项的不一致数，>0 即事故 |
| `ledger_reconcile_runs_total` | — | 同上 | 完成的对账次数（启动 1 分钟后首次，之后每小时） |
| `wallet_scan_block`、`wallet_scan_lag_blocks`、`wallet_scan_last_success_timestamp_seconds` | network | wallet-service | 充值扫描的最高块、落后链头的块数、最近一轮完成时间（[wallet.md](wallet.md)） |
| `wallet_deposits_detected_total`、`wallet_deposits_orphaned_total`、`wallet_deposits_held` | network（detected 另有 status） | 同上 | 发现的充值、链重组丢弃的充值、因资产关闭充值而挂起的已确认充值 |
| `wallet_chain_balance`、`wallet_chain_expected`、`wallet_chain_shortfall`、`wallet_hot_wallet_balance` | network, asset | 同上 | 链上对账（不变量 4）：持有、账本预期、缺口（> 0 即少钱）；热钱包余额 |
| `wallet_chain_fees_unbooked`、`wallet_sweeps_open`、`wallet_withdrawals_waiting` | network | 同上 | 未记账的 gas（GAS_SUPPLY 不足）、等待回执的归集、因热钱包不足或费用超上限而等待的提现 |
| `analytics_ingested_rows_total`、`analytics_rejected_rows_total` | topic | analytics-consumer | 写入 ClickHouse 的行 |
| `analytics_reconcile_missing`、`analytics_reconcile_last_success_timestamp_seconds` | topic | 同上 | 最近 24 小时 outbox 已发而 ClickHouse 缺的事件数（负数表示有 outbox 不在 `RECONCILE_SCHEMAS` 里）、最近一次核对成功 |

阶段 1 验收标准 10 的对应：错误率与延迟 → `http_server_*`；OTP 发送量 → `auth_otp_requests_total`；事件积压 → `kafka_consumer_lag`、`outbox_pending`；DLQ → `kafka_consumer_records_total{result="dlq"}`。`scripts/e2e/ops.sh` 逐个服务检查健康、就绪与这些指标族。

临时查看（测试服）：

```bash
ssh exchange 'cd /opt/exchange/infra && sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T ledger-service wget -qO- http://127.0.0.1:9085/metrics' | grep ^ledger_
```

## 告警清单（`deploy/observability/alerts.yml`）

| 告警 | 级别 | 条件 | 处理 |
|---|---|---|---|
| ServiceDown | critical | 运维端口 2 分钟无响应 | `task deploy:status`、`task deploy:logs -- <服务>` |
| HighHTTPErrorRate | critical | 5 分钟 5xx 比例 > 2%（且有流量） | 按 trace ID 查日志（下文） |
| HighHTTPLatency | warning | p99 > 1 秒持续 10 分钟 | 看依赖（PostgreSQL、Redis）与 `/debug/pprof` |
| GRPCServerErrors | warning | 服务间调用 Unknown/Internal/Unavailable/DeadlineExceeded | 看被调服务 |
| EventsDeadLettered | warning | 15 分钟内有事件进入死信 | [events.md](events.md)：`exchangectl dlq list`，修复后 `replay` |
| ConsumerLagHigh | warning | 积压 > 1000 持续 10 分钟 | 消费者是否卡在重试、下游是否可用 |
| OutboxBacklog | warning | 待发事件 > 100 持续 5 分钟 | Redpanda 可用性 |
| OutboxPublishFailing | critical | 5 分钟内持续发送失败 | Redpanda 是否宕机（恢复后自动补发） |
| LedgerReconciliationMismatch | critical | 对账不一致 > 0 | [ledger.md](ledger.md)，冻结相关账户，按分录排查 |
| LedgerReconciliationStopped | warning | 3 小时无完成的对账 | ledger-service 日志 |
| WalletScanLagging | warning | 充值扫描落后链头 50 块以上持续 10 分钟 | [wallet.md](wallet.md)：节点可用性、租约、数据库 |
| WalletScanStalled | warning | 10 分钟没有完成一轮扫描 | 同上；wallet-service 日志里的 `deposit scan failed` |
| WalletDepositsHeld | warning | 已确认充值因资产关闭充值挂起超过 1 小时 | 重新开放充值后自动入账，或按 wallet.md 人工处置 |
| WalletChainShortfall | critical | 平台钱包链上持有少于账本预期持续 15 分钟 | [wallet.md](wallet.md) 链上对账；冻结相关提现，逐笔核对 |
| WalletHotWalletLow / High | warning | 热钱包 < 0.005 ETH 持续 15 分钟 / > 1 ETH 持续 1 小时 | 归集或注资 / 人工转冷 |
| WalletChainFeesUnbooked | warning | 链上 gas 1 小时未能记账 | `exchangectl wallet fund` 给 GAS_SUPPLY 注资 |
| WalletWithdrawalsWaiting | warning | 已批准的提现等待超过 30 分钟 | 热钱包余额、链上费用是否超过上限 |
| AnalyticsMissingEvents | warning | ClickHouse 缺事件持续 30 分钟 | analytics-consumer 与 ClickHouse 状态（恢复后自动补齐） |
| AnalyticsReconciliationStale | warning | 2 小时未成功核对 | 同上 |
| FeatureFlagsStale | warning | 开关 5 分钟未刷新 | PostgreSQL 连接；刷新失败时保持最后值 |
| SMSVolumeNearLimit | warning | 1 小时短信 > 150（上限 200） | 是否被刷；必要时 `exchangectl flags set auth.sms --off` |
| OTPCaptchaRejections | warning | 10 分钟人机验证失败 > 50 | 疑似攻击，看来源 IP 与限流 |
| OTPVerificationFailures | warning | 15 分钟验证失败过半（且量 > 20） | 猜码，或验证码没送达（看 notify 指标） |
| NotificationProviderCircuitOpen | warning | 服务商熔断 5 分钟 | [otp.md](otp.md) 的服务商链 |
| NotificationsFailed | warning | 有投递在全部服务商失败 | 同上，`notify.deliveries` 查失败原因 |

规则校验（CI 里自动跑）：

```bash
promtool check config deploy/observability/prometheus.yml
promtool test rules deploy/observability/alerts_test.yml
```

## 日志字段

| 字段 | 出现在 | 说明 |
|---|---|---|
| `time`、`level`、`msg` | 全部 | 时间（RFC 3339）、级别（DEBUG/INFO/WARN/ERROR，`LOG_LEVEL`）、消息 |
| `service` | 全部 | 服务名 |
| `trace_id`、`span_id` | 请求与事件处理中 | W3C trace，与响应头 `X-Trace-Id` 相同 |
| `request_id` | HTTP 请求 | `X-Request-Id`（合法则沿用客户端的） |
| `method`、`path`、`route`、`status`、`bytes`、`duration_ms`、`client_ip` | 访问日志 `http request` | `route` 是路由模板；5xx 记为 ERROR |
| `server`、`method`、`code`、`duration_ms` | `grpc request` | gRPC 方法与状态码 |
| `event_id`、`event_type`、`topic`、`group`、`attempt`、`error` | 事件处理 | 失败重试为 WARN，进入死信为 ERROR |
| `env`、`version`、`instance` | `service starting` | 启动时的环境、提交号、实例 |

脱敏（`internal/platform/logging`，按字段名，大小写与 `-`/`_` 不敏感）：`password`、`token`、`otp`、`otp_ticket`、`captcha_token`、`authorization`、`cookie`、`secret`、`api_key` 等替换为 `[REDACTED]`；`email`、`phone`、`identifier`、`target` 与各类 IP 字段掩码（`e***@example.com`、`2407:cdc0:f001::*`）。`scripts/e2e/pii.sh` 在测试服验证日志与 ClickHouse 里没有明文邮箱和验证码。

## 按 trace 查询

用户报错时拿响应头 `X-Trace-Id` 或错误体 `trace_id`（32 位十六进制）：

```bash
scripts/trace.sh 7b1c...e9 [24h]
```

输出该 trace 在所有服务里的日志（按时间排序）和它引起的事件（ClickHouse `events` 中 `correlation_id` 等于 trace ID；消费者处理事件时延续原 trace，所以下游事件也在其中）。一次注册的例子：

```text
== logs (last 10m)
...30:36.261  user-service  INFO  grpc request  method=/exchange.user.v1.UserService/CreateUser code=OK duration_ms=2
...30:37.702  auth-service  INFO  http request  method=POST route=/v1/auth/register/complete status=201 duration_ms=1444
...30:37.702  api-gateway   INFO  http request  method=POST route=/v1/auth/register/complete status=201 duration_ms=1447
== events (ClickHouse events with correlation_id = the trace ID)
auth.UserRegistered, auth.LoginSucceeded, notification.NotificationCreated, ledger.EntryPosted, ledger.BalanceChanged ×3
```

手工方式：`task deploy:logs` 或 `docker compose logs --since 1h | grep <trace_id>`；ClickHouse `SELECT occurred_at, topic, event_type FROM events FINAL WHERE correlation_id = '<trace_id>'`。

## 回滚

1. **代码**：`task deploy -- <提交号>`（= 服务器上 `bash /opt/exchange/src/deploy/server-update.sh <提交号>`），按该提交重建镜像、更新容器、核对 topic、同步参考数据并发布对应的 H5。`deploy/server-update.sh` 自身的改动要第二次运行才生效（bash 已读入旧函数）。
2. **数据库**：迁移只做增量（加表、加列、加约束前先回填），旧代码可以跑在新结构上，所以回滚代码不需要回滚结构。确需撤销时每个迁移都有 `-- +goose Down`（集成测试验证过往返）：本机 `goose -dir migrations/<目录> -table goose_db_version postgres "<DSN>&search_path=<schema>" down`，schema 分别为 auth、users、notify、instrument、ledger，DSN 取自 `.env`；平台表（outbox/inbox）记录在 `platform_db_version`。
3. **功能**：高风险能力都在功能开关后（ADR-0005），`exchangectl flags set <key> --off --reason ...` 立即生效，比回滚代码快。
4. **参考数据**：改回 `deploy/instruments/test.json` 后部署；交易对状态用 `exchangectl instruments pair-status`。
5. **验证**：`task deploy:status` 全部 healthy，`task e2e` 通过，ledger 对账指标为 0。
