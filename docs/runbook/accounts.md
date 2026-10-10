# 账户状态、资格与用户通知运维

需求 §5.3、§5.4；实现见 `internal/user`（主档、状态机、资格）与 `internal/notification`（站内信、安全通知邮件）。接口契约 `api/openapi/user.yaml`、`api/openapi/notification.yaml`。

## 账户状态

状态机（附录 B）：`ACTIVE ↔ RISK_REVIEW`；`ACTIVE/RISK_REVIEW → FROZEN`；`FROZEN → ACTIVE`；任何状态 → `CLOSED`（终态；`RISK_REVIEW/FROZEN → CLOSED` 2026-10-10 补）。其他变更返回 409 `USER_STATUS_TRANSITION_INVALID`。

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

管理后台的账户列表与概览用 gRPC `ListUsers`（按注册时间新到旧，游标分页）与 `UserStats`（总数、某时刻以来的新增、最近若干天每日新增）。关键字（B167）：`ListUsers` 的 `q`（去掉首尾空白后 2 到 64 个字符，否则 400，B170）只留用户名含它的账户（不分大小写，`strpos` 子串匹配，下划线与百分号不是通配符），再加上 `user_ids` 里的账户（别处按同一关键字匹配到的，即 auth-service `SearchUsers` 的邮箱、手机号结果，最多 500 个，多了 400），`q` 为空时 `user_ids` 不起作用；`FindUsername` 按用户名精确找账户（不分大小写，走 `users_username_lower` 索引），长得不像用户名的直接 404。后台的查找经 auth-service `FindUser` 用它（见 [auth.md](auth.md)）。

### 用户名与头像（设计 2026-10-07 头像与用户名，I0/I1）

- **用户名**：注册时抽一个 `user_` + 8 位小写字母数字（`crypto/rand`，撞了换一个重来，最多 5 次）；`username_changed_at` 为空表示还是抽到的。`PUT /v1/user/username`：3–20 位字母、数字、下划线，不以下划线开头（库表 CHECK 同样约束）；不分大小写唯一（`lower(username)` 唯一索引 `users_username_lower`，撞了 409 `USER_USERNAME_TAKEN`）；保留词 400 `USER_USERNAME_INVALID`——整词：house、root、system、null、undefined、api、www、help、service、security、platform、exchange、operator、bot，含有即拒：admin、astras、support、official、staff、moderator（`internal/user/domain/username.go`）；7 天一次（409 `USER_USERNAME_COOLDOWN`，`details.next_change_at`），完全相同的名字什么也不改，大小写不同算一次改名。改名发 `ProfileUpdated{fields: [username]}` 与审计 `user.username_changed`（actor `user:<ID>`），不发邮件。登录仍用邮箱或手机。
- **头像**：`POST /v1/user/avatar`（multipart，一个 `file` 部分，图片 ≤ 5 MB、请求 ≤ 8 MB，nginx 对这个地址放宽到 8m），`DELETE /v1/user/avatar` 回到默认。按内容判断格式，只收 PNG、JPEG、WebP，边长 ≥ 64、像素 ≤ 2048×2048、每通道 8 位（审查 FY C52：16 位的 4096² PNG 几百 KB 就能解码出 128 MB，超过 user-service 的 192 MiB；渐进式 JPEG 的系数每像素每分量还要 4 字节；站点上传前先把大照片缩小），否则 400 `USER_AVATAR_INVALID`（`details.reason` 说明原因），超过 5 MB 413 `USER_AVATAR_TOO_LARGE`。处理（`internal/user/adapters/avatars`，同一时间只处理一张）：解码 → 取中间的正方形 → 缩放到 256 与 64（CatmullRom）→ JPEG 按 EXIF 方向转正 → 编码为无损 WebP（纯 Go 的 `github.com/HugoSmits86/nativewebp`），不保留原图与任何元数据。文件在 `AVATAR_DIR`（测试服 user-service 容器的 `/data/avatars`，即服务器 `infra/uploads/avatars`）下 `<用户 ID>/<16 位随机>.webp` 与 `_64.webp`，先写临时文件再改名，644；新头像记进库以后才删旧文件（删失败只告警，文件留着没人引用）。库里 `users.avatar` 存 `{path, thumb_path, uploaded_at, size, sha256}`，空即默认头像（两站内置 12 个，按用户 ID 选）。没配 `AVATAR_DIR` 时上传答 503。
- **分发**：nginx `snippets/uploads.conf`（三个站点都 include）只读直出 `/uploads/avatars/<UUID>/<名>.webp`，`image/webp`、`Cache-Control: public, max-age=2592000, immutable`、`nosniff`，其它路径 404。换头像就是换地址，不用清缓存；但删除或重置后，Cloudflare 已缓存的旧地址最长 30 天内仍可访问（地址随机，只有看过的人知道）。
- **后台重置**（单人，权限 `users.status`，审计 `admin.users.username_reset`（含新旧名）/`admin.users.avatar_reset`）：admin-service 调 user-service gRPC `ResetUsername`（重新抽名，`username_changed_at` 清空、不进 7 天冷却）、`ResetAvatar`（删文件，没有头像时什么也不做）；user-service 发 `ProfileReset{field, username, actor, reason}`（user.events）与审计 `user.username_reset`/`user.avatar_reset`，站内信 `USERNAME_RESET`/`AVATAR_RESET` 由通知服务按它发。
- 资料接口与 gRPC `User` 都带 `username`、`username_changed_at`、`avatar_url`、`avatar_thumb_url`（站内路径，无头像为空）；后台 `UserSummary` 带 `username`、`avatar_url`、`avatar_thumb_url`。

### 账户类型（L0，用户类型标记设计 2026-10-09）

- **取值**：`users.kind`（迁移 users 00004，`NOT NULL DEFAULT 'HUMAN'`、CHECK、索引）——`HUMAN` 真人（每个注册都是）、`BOT` 平台币模拟市场的机器人、`TEST` 端到端脚本的账户、`SYSTEM` HOUSE 等系统账户。**只是后台的显示与筛选属性**：交易、手续费（机器人零手续费照旧按 `MARKET_MAKER_USER_IDS`）、风控、通知、market-sim 的注册与调度一律不读它；公开接口与两站不显示；平台币机器人是正式功能，上线后照常买卖 ASTRA。
- **改动**：只有运维与脚本改，后台不提供改类型的操作。每次改动记在 `user_kind_changes`（from/to/actor/reason/时间），并发审计 `user.kind_changed`；设成已有的类型什么都不记（幂等）。
  - 运维：`exchangectl users kind --user <ID>[,<ID>...] --kind BOT --reason "..."`，或 `--email-like 'e2e-%@example.com'`（按 auth-service 的邮箱 LIKE 匹配，邮箱一律小写）；在任一应用容器里执行，如 `ssh exchange sudo docker exec exchange-infra-user-service-1 /app/exchangectl users kind ...`，解除人取 `EXCHANGECTL_ACTOR`。
  - 内部接口（compose 网络内，`api/internal/users.yaml`）：`PUT /internal/users/{id}/kind`（`{kind, reason, actor?}`，`actor` 缺省为 `internal`）；`GET /internal/users/ids?kind=BOT,TEST,SYSTEM`（逗号分隔或重复，不分大小写）返回这些类型的全部账户 ID（按 ID 排序），带 ETag 与 `Cache-Control: max-age=60`，`If-None-Match` 命中答 304——后台据此从别的服务的列表里去掉非真人（L1/L2 的 `exclude_user_ids`）。带 `X-User-Id`（经网关来的）的请求答 404。
  - `scripts/ops/astra.sh mark`：机器人（market-sim 名单里的）标 `BOT`、HOUSE（`apps.env` 的 `HOUSE_USER_ID`）标 `SYSTEM`；幂等，已有账户也补标；`astra.sh seed` 最后自动调它。只打标，不改状态。HOUSE 在 user-service 没有账户（`COMMON_NOT_FOUND`）只提示一句；其他失败（连不上、服务出错、参数不对）照原样报出并以非零退出（B177）。新服务器上 seed 与打标是上线步骤（[launch.md](launch.md) 步 12），不是测试环节。
  - 端到端：`scripts/e2e/lib/common.sh` 被加载时挂一个退出钩子（脚本注册过账户、或外壳脚本设了 `E2E_MARK_RUN=1` 时才动作，B187），脚本结束时用一次 `exchangectl users kind --email-like '%-<RUN>@example.com' --kind TEST` 把本次注册的账户标为 `TEST`（端到端的 `e2e-…-<RUN>`、故障注入演练的 `fault-…-<RUN>` 都在内；`astra-bot-NN` 不带 RUN，不受影响；改动人 `e2e-<脚本名>`）；失败只警告，不让用例失败。两站的浏览器冒烟注册的账户（也是 `e2e-…@example.com`）由下面的补标命令覆盖。
- **存量补标**（测试服 2026-10-10 做过一次，以后可随时重跑，幂等）：`scripts/ops/astra.sh mark`；`exchangectl users kind --email-like 'e2e-%@example.com' --kind TEST --reason "..."`，同样对 `fault-%@example.com`（故障注入演练）与 `h5-%@example.com`（旧 H5 的测试账户）。2026-10-10 补完后测试服是 BOT 24、SYSTEM 1（HOUSE）、TEST 1,619、HUMAN 2。
- **读**：gRPC `User.kind`、`ListUsersRequest.kinds`（可多值）、`UserStatsResponse.by_kind`（每种类型的总数与某时刻以来的新增，按 HUMAN、BOT、TEST、SYSTEM；`total` 与 `created_since` 是它们的和）；`UserStatsRequest.kinds`（B185，与 `ListUsersRequest.kinds` 同义）只数这些类型——总数、新增、按天的 `days` 与 `by_kind` 都按它算，不给即全部，未知类型答 InvalidArgument。后台默认只看真人（L1）。
- **清理过的测试账户**（L4，用户 2026-10-10「直接把测试用户清理了吧，需要测试再创建」）：不删行（账本只追加，每条分录都指向账户），而是结清 + 关闭 + 隐藏：挂单撤掉、仓位与借款了结、余额划入 ADJUSTMENT、状态 CLOSED（auth 收到 CLOSED 自动吊销全部会话）之后，user-service 记 `users.purged_at`（迁移 users 00005，`MarkPurged`：只对 CLOSED 账户、只记一次、审计 `user.purged`）。清理过的账户在后台的用户列表与统计里默认不出现（gRPC `ListUsersRequest.include_purged` 才列出；`by_kind` 不计），`GET /internal/users/ids` 仍包含它们（它们的历史订单与流水照样按类型排除）；`User.purged_at` 给出时间。`MarkPurged` 只对 TEST 且未豁免的账户（B180）。
- **清理工具**（L4）：`scripts/ops/purge.sh [--dry-run] [--older-than 24h] [--limit N] [--user ID,...] [--email-like P] [--reason TEXT]`——在运维锁下（dry-run 不拿锁）于 user-service 容器里跑 `exchangectl users purge`，只处理 `kind = TEST`、未清理、未豁免的账户，按注册时间从旧到新，每个账户之间停 50 ms（`--pace`，免得上千次关闭同时涌到 auth 与通知服务）。逐个账户：有合约仓位、挂单或条件单的先请 derivatives-service 清场（L4b `POST /internal/derivatives/users/{id}/flatten`，对 HOUSE 以强平同款保护价 IOC 平仓，最长约 40 s，客户端超时 90 s，不重试——C79 之后引擎慢时答 complete:false，重调会与仍在跑的那次并行，B188 ②；409 的说明照原样列出，B188 ③）、有杠杆负债的先请 margin-service 结清（L4b `POST /internal/margin/users/{id}/settle`，用账户里同币种余额还本息；还不够的逐笔从现货以该用户身份转入（`POST /v1/margin/transfer` IN，幂等键 `purge:<用户>:<资产>:in:<账户与交易对短哈希>:v<当时现货行版本>`，同一资产的两笔各有各的键，B188 ①）再结一次）——两者只在需要时调（每次调用都有一条服务级审计），没平完或没还清、或答 409（正在强平）的跳过并列出；→ 撤现货与杠杆挂单（以该用户身份 `DELETE /v1/orders` 直连 spot-trading-service，等挂单结束、冻结释放，最多 `--wait` 20 s，超时跳过）→ 无负债的杠杆账户余额（撤单、冻结释放之后重新读到的，B184 ①）转回现货（以该用户身份 `POST /v1/margin/transfer` OUT，幂等键 `purge:<用户>:<资产>:<账户与交易对的短哈希>:v<该行版本>`）→ 现货与合约账户（含币本位的各币合约账户）的可用余额以 MANUAL_ADJUSTMENT 划入系统账户 ADJUSTMENT（幂等键 `purge:<用户>:<账户>:<资产>:v<该行版本>`，流水里存为 `adjust:purge:…`；键里的版本是账本行已记的条数：同一余额重跑即重放，余额变过（清扫后又有入账）就是新键，不会因同键不同金额卡住，B184 ②；审计 `ledger.manual_adjustment`；需要开关 `ledger.manual_adjustment`）→ 还有任何余额就跳过 → 不论原来是什么状态直接 CLOSED（原因码 `TEST_ACCOUNT_PURGE`；auth 收到后吊销全部会话，通知服务照常发一条状态变更站内信与邮件，example.com 走 mock）→ `purged_at`。在途提现的账户跳过并逐个列出原因（PENDING_REVIEW 的由后台驳回后再清）。输出：清理前后的计数（TEST 未清理/其中豁免/已清理）、清理与跳过的个数（按原因）、划入 ADJUSTMENT 的金额（按账户与资产，杠杆账户的计在现货）；有账户失败时整条命令以非零退出。`--dry-run` 只读不改、不调清场与结清接口，报出会清理的个数、其中要先清场/结清的个数与会划转的金额（这些账户按当前余额估）。
- **豁免**：`scripts/ops/purge.sh exempt (--user ID,... | --email-like P) [--off] --reason TEXT`（即 `exchangectl users exempt`，迁移 users 00006 的 `purge_exempt`，审计 `user.purge_exempt`，幂等）。端到端 `funding.sh` 的常驻对冲账户（多空各一，U 本位与币本位各一对，邮箱记在本机 `~/.cache/exchange-e2e/funding-*`）每次运行都确认豁免，对冲没了换新的时解除旧账户的豁免；若对冲账户已被清理（登录答 `USER_CLOSED`），删掉状态文件另开一对（B184 ⑤）。
- **端到端自清**：`scripts/e2e/lib/common.sh` 的退出钩子在把本次运行注册的账户（`%-<RUN>@example.com`）标为 TEST 之后，接着按本次记下的用户 ID 对它们跑一次清理（不按邮箱：RUN 只到秒，同一秒启动的另一会话的脚本会共用后缀，B184 ④）。ID 记在导出的 `$E2E_REGISTERED` 文件里（一行一个）：`register` 自己写，脚本调起的程序自己注册的账户（浏览器冒烟测试）也要把 ID 追加进去，否则只会被标 TEST、不被清理；钩子在 common.sh 被加载时就挂上，没有注册过账户的脚本退出时什么也不做；它注册得最早、在退出时最后执行，脚本自己的收尾（撤单、平仓）都在它之前。合约仓位先清场、负债先结清，清场或结清后仍留下的、在途提现的与豁免的账户跳过（打出 `note: skip …`），失败只警告；设了 `E2E_MARK_RUN=1` 的外壳脚本（web.sh、webflows.sh）即使自己没注册也按邮箱标 TEST（B187）。浏览器冒烟测试（`web/e2e/*.mjs`）自 F38 起用本次的 RUN 命名账户（会被标 TEST），把 ID 写进 `$E2E_REGISTERED` 之前不会被钩子清理，靠定期或一次性清理。

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
| `ProfileReset`（后台重置用户名，`field` USERNAME） | `USERNAME_RESET`（带新用户名） | 否 |
| `ProfileReset`（后台重置头像，`field` AVATAR） | `AVATAR_RESET` | 否 |

- 重置的两种通知（B140，2026-10-07）：只在站内，告诉用户不符合规范、新用户名（重置后不进冷却，可以马上改）或已恢复默认头像；操作人与理由只进审计，不写进通知。两站点开到个人资料页 `/account/profile`（I2 上线后，B146）。

- 合约的三种通知（`CONTRACT_*`）：金额以合约的结算资产表示（U 本位为 USDT，币本位为该币；事件没带 `settle_asset` 的旧事件按 USDT），数量的单位是币（U 本位）或张（币本位：面值 `contract_size` 大于零，事件没带面值时按 `-USD-PERP`）；全仓预警写该结算币的全仓账户，逐仓预警写合约与仓位方向（事件的 `direction`，C49 起单向持仓也有；旧事件退回双向持仓的 LONG/SHORT），逐仓强平写方向（单向持仓按数量的正负）、数量与标记价格，自动减仓写方向（同预警）、成交价与已实现盈亏。全仓被接管时引擎对该结算币的每个全仓仓位各发一条 `LiquidationStarted`，通知按账户合并：收件箱的键是「用户 + 结算币 + 分钟」的名字型 UUID（v5，收件箱的 `event_id` 是 uuid 列，重放时仍合并），同一次接管只发一条「合约全仓账户强平」，不写某个仓位的方向、数量与标记价（审查 FG，B133；R10，B138）。按分钟分桶：跨整分钟的一次接管会发两条，同一分钟里同一账户的第二次接管会并进第一次——可接受，事件本身都在 `derivatives.liquidation.events` 里。数量单位按事件的 `contract_size`（`AdlExecuted` 自 3785d360 起也带）。`LiquidationFilled`（强平单逐笔成交）不发通知。资金费结算不发通知（每 8 小时每个仓位一条，币安默认也不发），记录在交易页的资金费页签。两站的通知中心把它们归入「资产」，预警用警告色、强平与自动减仓用危险色，点开到该合约的交易页（全仓预警到资产页）。
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
