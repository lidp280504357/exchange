# 账户状态、资格与用户通知运维

需求 §5.3、§5.4；实现见 `internal/user`（主档、状态机、资格）与 `internal/notification`（站内信、安全通知邮件）。接口契约 `api/openapi/user.yaml`、`api/openapi/notification.yaml`。

## 账户状态

状态机（附录 B）：`ACTIVE ↔ RISK_REVIEW`；`ACTIVE/RISK_REVIEW → FROZEN`；`FROZEN → ACTIVE`；`ACTIVE → CLOSED`。其他变更返回 409 `USER_STATUS_TRANSITION_INVALID`。

阶段 1 用运维 CLI 改状态（阶段 2 由管理后台接管）：

```bash
# 测试服（在 user-service 容器里运行，读容器环境变量）
ssh exchange sudo docker exec exchange-infra-user-service-1 /app/exchangectl users show <user_id>
ssh exchange sudo docker exec exchange-infra-user-service-1 /app/exchangectl users status <user_id> --to FROZEN --reason SUSPICIOUS_LOGIN --note "工单 42"
```

- `--reason` 是大写原因码（如 `SUSPICIOUS_LOGIN`、`USER_REQUEST`、`REVIEW_CLEARED`），`--note` 进审计记录；操作者自动记为 `cli:<系统用户>`。
- 一次变更在同一事务里：更新 `users.users`、追加 `users.user_status_changes`、经 outbox 发 `user.UserStatusChanged`（user.events）与 `audit.AdminActionPerformed`（audit.events，进 ClickHouse `audit_logs`）。
- 生效链路：auth-service 消费 `UserStatusChanged`，在 Redis 写 `auth:stale:<user_id>`（毫秒时间戳，变更时刻 + 1 秒，TTL 15 分钟）；网关对签发时间不晚于它的访问令牌返回 `AUTH_TOKEN_EXPIRED`，客户端刷新后拿到新 scope（`FROZEN` 为只读 `read`）。变更为 `CLOSED` 时同时吊销全部会话。
- notification-service 同时给用户发站内信与邮件（`STATUS_CHANGED`）。

## 资格（eligibility）

`GET /v1/user/eligibility?feature=` 与 gRPC `UserService.CheckEligibility` 共用一套规则：先看账户状态（§5.4 影响矩阵），再看功能开关及其地区等规则。

| 功能 | 开关 | ACTIVE | RISK_REVIEW | FROZEN | CLOSED |
|---|---|---|---|---|---|
| `SPOT_TRADE` | 无 | ✓ | ✓ | `USER_FROZEN` | `USER_CLOSED` |
| `DEPOSIT` | 无 | ✓ | ✓ | ✓（入账但不可动） | `USER_CLOSED` |
| `DERIVATIVES_TRADE` | `derivatives.trading` | 看开关 | `USER_RISK_REVIEW` | `USER_FROZEN` | `USER_CLOSED` |
| `TRANSFER` | `account.transfer` | 看开关 | `USER_RISK_REVIEW` | `USER_FROZEN` | `USER_CLOSED` |
| `WITHDRAW` | `wallet.withdraw` | 看开关 | `USER_RISK_REVIEW` | `USER_FROZEN` | `USER_CLOSED` |

开关关闭或不存在返回 `USER_NOT_ELIGIBLE`；开关因地区规则拒绝返回 `USER_REGION_NOT_ALLOWED`。改开关见 [feature-flags.md](feature-flags.md)。

## 个人资料

`PATCH /v1/user/profile` 可改语言、时区、防钓鱼码。防钓鱼码（4–20 位字母数字，空串清除）要 step-up：user-service 调 auth-service gRPC `ConsumeStepUp` 兑换 `X-Step-Up-Token`。防钓鱼码会出现在我们发出的每封安全通知邮件开头。

## 用户通知

notification-service 以消费组 `notification-service` 读 `auth.events` 与 `user.events`，每个事件最多生成一条站内信（inbox 去重），同事务发 `notification.NotificationCreated`（阶段 1 任务 15 起经 WebSocket `notifications` 频道推送）。

| 事件 | 站内信类型 | 邮件 |
|---|---|---|
| `UserRegistered` | `WELCOME` | 否 |
| `LoginSucceeded`（新设备，注册除外） | `NEW_DEVICE_LOGIN` | 是 |
| `IdentityBound` / `IdentityRebound` | `IDENTITY_CHANGED` | 是 |
| `PasswordChanged` | `PASSWORD_CHANGED` | 是 |
| `LoginFailed`（本次锁定） | `ACCOUNT_LOCKED` | 是 |
| `UserStatusChanged` | `STATUS_CHANGED` | 是 |

- 文案按用户语言（zh-CN / en）与时区渲染；数据里只有掩码后的 IP、身份。
- 邮件发到用户邮箱，没有邮箱才发短信（短信只含标题，不带链接）；投递走与验证码相同的服务商链、重试与熔断，结果记在 `notify.deliveries`（`kind = NOTICE`，模板名 `notice.<类型>`）。邮件是尽力而为：取联系人或投递失败只记日志，站内信不受影响。
- 用户不存在（主档查不到）的事件直接丢弃并记警告；user-service 不可用时事件按 1s/5s/30s/5m 重试后进 `.dlq`。

查看某用户的通知：

```sql
SELECT type, title, read_at, created_at FROM notify.notifications WHERE user_id = '<user_id>' ORDER BY id DESC LIMIT 20;
```

## 端到端检查

`scripts/e2e/account.sh`（`task e2e` 会连同 `auth.sh` 一起跑）：注册 → 资料与资格 → 欢迎与新设备通知（含邮件）→ 用 exchangectl 冻结 → 旧令牌被要求刷新、刷新后只读 → 解冻。改状态通过 `EXCHANGECTL`（默认 `ssh exchange sudo docker exec ... /app/exchangectl`）。
