# 风控规则运维

需求 §5.13；实现见 `internal/risk`（risk-service）与 user-service 的处置消费者（`internal/user/transport/consumer`）。

## 链路

1. auth-service 发 `auth.events`（UserRegistered、LoginSucceeded、LoginFailed）。
2. risk-service 按规则评估。inbox 去重，重投的事件不会重复计数。
3. 命中任一规则：在 `risk.assessments` 记一行，并发 `risk.RiskScored`（分数、动作、命中的规则）。
4. 动作为 REVIEW/REJECT，且功能开关 `risk.enforce` 对该用户打开：再发 `risk.RiskActionTaken`。
5. user-service 把 ACTIVE 账户置为 `RISK_REVIEW`，原因码 `RISK_RULE`，操作者 `risk-service`。它同样用 inbox 去重，所以运维关闭审核后，同一事件重投也不会重开。非 ACTIVE 的账户保持原状态。
6. 之后走已有链路：auth 让旧令牌去刷新（风控审核中不能划转），站内信与安全邮件通知用户。

## 内置规则

`exchangectl risk rules` 打印内置规则（JSON）：

| 规则 | 事件 | 条件 | 分数 | 动作 |
|---|---|---|---|---|
| `registration_burst_device` | 注册 | 同一设备 24 小时内的第 3 个及以后的注册 | 60 | REVIEW |
| `registration_burst_network` | 注册 | 同一网段（IPv4 /24、IPv6 /48）1 小时内的第 30 个及以后 | 40 | STEP_UP |
| `new_device_login` | 登录成功 | 用户已有设备后，又从新设备登录（注册时的第一台设备不算） | 20 | NONE |
| `login_failures` | 登录失败 | 同一账户 15 分钟内的第 5 次及以后 | 40 | STEP_UP |

- 分数是命中规则的分数之和（最多 100），动作取命中规则里最严重的。
- 第一版只执行 REVIEW/REJECT（置 `RISK_REVIEW`）。STEP_UP 是给后续敏感操作的建议，阶段 2 提现起用；NONE 只记录。
- 频率窗口按事件发生时间算，积压后补处理或重放的结果与实时一致。
- 网段规则只建议加验，因为共享出口 IP 的运营商网络会误报。
- 不做 IP 地理判断（ADR-0009）。地区规则（`kind: region`）按用户申报的国家；按项目前提不限制地区，所以内置规则里没有地区规则。

## 改规则

规则是 JSON 数组，每条的字段：

| 字段 | 说明 |
|---|---|
| `id` | 规则名 |
| `event` | `auth.UserRegistered`、`auth.LoginSucceeded`、`auth.LoginFailed` |
| `kind` | `velocity`、`new_device`、`region` |
| `key`、`window`、`threshold` | velocity 规则用：`key` 为 `user`、`device` 或 `network`；`window` 如 `15m`、`24h`，1 分钟到 720 小时；`threshold` 至少 2 |
| `regions` | region 规则用：大写的 ISO 3166-1 alpha-2 代码 |
| `score` | 0–100 |
| `action` | `NONE`、`STEP_UP`、`REVIEW`、`REJECT` |

启动时严格校验：未知字段、重复 id、取值越界都会让 risk-service 拒绝启动。换规则有两种方式：

- 改 `internal/risk/domain/default_rules.json` 后部署（有单测覆盖）。
- 用环境变量 `RISK_RULES_FILE` 指向一个规则文件，替换内置规则。文件要挂进 risk-service 容器，并在 `apps.env` 里设置该变量。

## 执行开关

`risk.enforce` 默认关闭：只记分，不处置（ADR-0005）。测试服只对地区 `AQ` 打开，供端到端测试 `scripts/e2e/risk.sh` 验证处置链路，其他用户只记分：

```bash
exchangectl flags set risk.enforce --on --allow-regions AQ --reason "e2e: enforce risk rules for the test region"
```

全量打开前，先看一段时间的评估分布，确认误报可以接受。

## 查看与处理

```bash
exchangectl risk assessments --user <user_id>     # 某用户的评估（分数、动作、是否执行、命中规则）
exchangectl risk assessments --limit 50           # 最近的评估
exchangectl users show <user_id>                  # 状态历史里可以看到 RISK_RULE 与 risk-service
exchangectl users status <user_id> --to ACTIVE --reason REVIEW_CLEARED   # 审核通过
```

ClickHouse：

```sql
SELECT occurred_at, payload FROM events WHERE topic = 'risk.events' ORDER BY occurred_at DESC LIMIT 20
```

管理后台（2026-10-02 设计 C2）：用户页的「风控」标签列出该用户的评估，头部显示最近一次的分数与动作，并可转人工审核（`RISK_REVIEW`）或审核通过后恢复。数据经 risk-service 只读的 gRPC `ListAssessments`（端口 9186，admin-service 用 `RISK_GRPC_ADDR` 连接）。

## 数据

| 表（`risk` schema） | 内容 | 保留 |
|---|---|---|
| `user_devices` | 每个用户用过的设备 | 长期 |
| `velocity_events` | 频率规则计数，每个事件每条规则一行 | 最长窗口（24 小时），每小时清理 |
| `assessments` | 每次有命中的评估 | 长期 |

指标用通用的：`kafka_consumer_*`（消费 `auth.events` 的积压与死信）、`outbox_*`，user-service 的 `kafka_consumer_*`（消费 `risk.events`）。
