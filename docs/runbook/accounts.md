# 账户状态、资格与用户通知运维

需求 §5.3、§5.4；实现见 `internal/user`（主档、状态机、资格）与 `internal/notification`（站内信、安全通知邮件）。接口契约 `api/openapi/user.yaml`、`api/openapi/notification.yaml`。

## 账户状态

状态机（附录 B）：`ACTIVE ↔ RISK_REVIEW`；`ACTIVE/RISK_REVIEW → FROZEN`；`FROZEN → ACTIVE`；`ACTIVE → CLOSED`。其他变更返回 409 `USER_STATUS_TRANSITION_INVALID`。

除运维手工变更外，风控规则在 `risk.enforce` 打开时会把 ACTIVE 账户置为 `RISK_REVIEW`（原因码 `RISK_RULE`，操作者 `risk-service`，见 [risk.md](risk.md)）。

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
| `MARGIN_TRADE` | `margin.enabled` | 看开关 | `USER_RISK_REVIEW` | `USER_FROZEN` | `USER_CLOSED` |

`MARGIN_TRADE`（杠杆设计 2026-10-06 §7）只给两站决定显不显示杠杆入口；margin-service 与交易服务自己按用户 ID 查 `margin.enabled`（不带地区），所以这个开关的规则应写用户名单而不是地区，否则资格说可以、服务却按失败即关闭拒绝。

开关关闭或不存在返回 `USER_NOT_ELIGIBLE`；开关因地区规则拒绝返回 `USER_REGION_NOT_ALLOWED`。改开关见 [feature-flags.md](feature-flags.md)。

## 个人资料

`PATCH /v1/user/profile` 可改语言、时区、防钓鱼码。防钓鱼码（4–20 位字母数字，空串清除）要 step-up：user-service 调 auth-service gRPC `ConsumeStepUp` 兑换 `X-Step-Up-Token`。防钓鱼码会出现在我们发出的每封安全通知邮件开头。

自选（阶段 4 B1）：`GET /v1/user/favorites` 与 `PUT /v1/user/favorites`（`{"symbols": [...]}`），存在 `users.favorites`（每用户一行）。
- 最多 100 个。
- 代码转大写，重复的只保留第一个，顺序按用户给的。
- 只检查格式（交易对 `BTC-USDT` 或合约 `BTC-USDT-PERP`），不核对是否上架。
- 前端未登录时存本地，登录后把两边合并再写回。

管理后台的账户列表与概览用 gRPC `ListUsers`（按注册时间新到旧，游标分页）与 `UserStats`（总数、某时刻以来的新增、最近若干天每日新增）。

## 用户通知

notification-service 以消费组 `notification-service` 读 `auth.events`、`user.events`、钱包的充值与提现事件、`margin.events` 与 `derivatives.liquidation.events`（2026-10-07 起，币本位设计 §2.6），每个事件最多生成一条站内信（inbox 去重），同事务发 `notification.NotificationCreated`（阶段 1 任务 15 起经 WebSocket `notifications` 频道推送）。

| 事件 | 站内信类型 | 邮件 |
|---|---|---|
| `UserRegistered` | `WELCOME` | 否 |
| `LoginSucceeded`（新设备，注册除外） | `NEW_DEVICE_LOGIN` | 是 |
| `IdentityBound` / `IdentityRebound` | `IDENTITY_CHANGED` | 是 |
| `PasswordChanged` | `PASSWORD_CHANGED` | 是 |
| `LoginFailed`（本次锁定） | `ACCOUNT_LOCKED` | 是 |
| `UserStatusChanged` | `STATUS_CHANGED` | 是 |
| `LiquidationWarning`（合约保证金余额到维持保证金的 1.2 倍） | `CONTRACT_LIQUIDATION_WARNED` | 是 |
| `LiquidationStarted`（合约仓位被强平引擎接管） | `CONTRACT_LIQUIDATING` | 是 |
| `AdlExecuted`（合约仓位被自动减仓） | `CONTRACT_ADL` | 是 |

- 合约的三种通知（`CONTRACT_*`）：金额以合约的结算资产表示（U 本位为 USDT，币本位为该币；事件没带 `settle_asset` 的旧事件按 USDT），数量的单位是币（U 本位）或张（币本位，`-USD-PERP`）；全仓预警写该结算币的全仓账户，逐仓写合约与仓位，强平写方向（单向持仓按数量的正负）与标记价格，自动减仓写成交价与已实现盈亏。`LiquidationFilled`（强平单逐笔成交）不发通知。资金费结算不发通知（每 8 小时每个仓位一条，币安默认也不发），记录在交易页的资金费页签。两站的通知中心把它们归入「资产」，预警用警告色、强平与自动减仓用危险色，点开到该合约的交易页（全仓预警到资产页）。
- 后台发的站内信（类型 `BROADCAST`，2026-10-02 后台设计 C4b）不来自事件：管理员在「运营 → 站内信」发给单个用户、带某个标签的账户（最多 10,000 个）或全体用户，notification-service 每 3 秒一轮、每轮每条消息 100 人分批送达（表 `notify.broadcasts`，`cursor` 记进度，`inbox` 按消息与用户去重，同一人不会收到两次），用户语言没有英文时用中文；`data` 带 `broadcast_id` 与可选的站内路径 `link`，勾了「同时发邮件」时按下面的邮件规则投递。送达与已读人数在后台的消息详情里。一条消息的一轮出错只影响它自己：等待时间 3 秒起翻倍、最多 10 分钟，连续 10 轮失败标为 `FAILED`，由后台「继续发送」（迁移 notify 00003 的 `failures`、`last_error`、`retry_at`，C5.5 ⑫）。
- 后台消息的邮件不在送达的那一轮里发：通知与一行 `QUEUED` 的投递记录（`notify.deliveries.next_attempt_at`）在同一事务写入，邮件队列每秒发 `NOTICE_MAIL_RATE` 封（默认 2，照顾服务商的限速），发送时才读用户的地址（不存明文）；失败的过 1、5、15、60 分钟再试（按失败的轮次 `deliveries.rounds` 算，迁移 notify 00004；不按服务商的尝试次数——一个渠道有两家服务商时每轮记两次——也不按入队后过了多久——队列积压几小时的邮件第一次失败就会被判到期），再失败、没有地址了或超过一天就标 `FAILED`，发 `DeliveryFailed` 事件（C5.5 ⑫、㉓）。服务商收下的那一刻（记为 `SENT`）同一条语句就把它移出队列，进程在那之后崩溃也不会重发（C5.5 ㉓）。账户事件的邮件仍当场发。
- 留存（C5.5 ⑫）：notification-service 每小时删除超过 `NOTICE_RETENTION`（默认 180 天）的站内信与不在发送中的后台消息（`FAILED` 的也删：半年后不会再继续发送）、超过 `DELIVERY_RETENTION`（默认 90 天）且不在队列里的投递记录，每次 5,000 行一批。两个期限都不能短于 7 天，配短了服务拒绝启动（C5.5 ㉓）。谁发过什么消息仍在审计里。
- 文案按用户语言与时区渲染：语言以 `en` 开头的用英文，`zh-TW`、`zh-HK`、`zh-MO`、`zh-Hant*` 用繁体中文（繁体设计 2026-10-06 §2.2），其余用简体中文；数据里只有掩码后的 IP、身份。繁体不是手写的：模板（`internal/notification/domain/templates.go`、`notices.go`）里每段简体文字都包在 `zh(lang, …)` 里，前端的生成脚本（`pnpm i18n`，OpenCC 与术语表）把它们转成繁体写进 `zh_tw_gen.go`，`task web:check` 核对它与模板一致——改了模板里的中文要重新生成；`zh_tw_test.go` 检查繁体用户的每种站内信、邮件与验证码里没有简体字。验证码邮件与短信同样按请求的语言（站点传 `language`，没传时按 `Accept-Language`）。后台发的站内信可以另写繁体（`zh-TW`），没写或只写了标题时繁体用户收到简体。
- 邮件发到用户邮箱，没有邮箱才发短信（短信只含标题，不带链接）；投递走与验证码相同的服务商链、重试与熔断，结果记在 `notify.deliveries`（`kind = NOTICE`，模板名 `notice.<类型>`）。账户事件的邮件是尽力而为：取联系人或投递失败只记日志，站内信不受影响（后台消息的邮件见上一条，经队列）。
- 用户不存在（主档查不到）的事件直接丢弃并记警告；user-service 不可用时事件按 1s/5s/30s/5m 重试后进 `.dlq`。

查看某用户的通知：

```sql
SELECT type, title, read_at, created_at FROM notify.notifications WHERE user_id = '<user_id>' ORDER BY id DESC LIMIT 20;
```

## 端到端检查

`scripts/e2e/account.sh`（`task e2e` 会连同 `auth.sh` 一起跑）：注册 → 资料与资格 → 欢迎与新设备通知（含邮件）→ 用 exchangectl 冻结 → 旧令牌被要求刷新、刷新后只读 → 解冻。改状态通过 `EXCHANGECTL`（默认 `ssh exchange sudo docker exec ... /app/exchangectl`）。
