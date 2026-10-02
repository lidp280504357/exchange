# 管理后台（admin-service 与 web/apps/admin）

实施计划 §6.3 任务 11，需求 §5.12（RBAC、双人审批、操作审计、提现审批、交易对管理、功能开关、用户处置）、§5.14。

## 组成

```
浏览器 https://admin.astras.vip/ ──nginx──> 静态文件（web/apps/admin 构建产物，/opt/exchange/infra/nginx/sites/admin）
                     /admin/v1/* ──nginx──> admin-service:8093（不经用户网关）
https://astras.vip/admin/* ──301──> https://admin.astras.vip/*（旧后台的地址，阶段 4 B5 起）
admin-service ──gRPC──> auth-service（按邮箱/手机号找用户）、user-service（改账户状态）、
                        ledger-service（余额、手动调账、保险基金注资与系统账户余额）、
                        instrument-service（资产、交易对与合约，改交易对与合约状态）
              ──内部 REST──> wallet-service（提现列表与审批）、spot-trading-service（撤销用户全部挂单）、
                             derivatives-service（合约状态与只减仓、强平监控）
              ──config schema──> 功能开关（与 exchangectl flags 同一张表，变更与审计事件同一事务）
              ──ClickHouse audit_logs──> 审计查询
              ──admin schema──> 管理员、会话、双人审批申请；自己的 outbox 发 audit.events
```

- 服务：admin-service，HTTP 8093（nginx 转发 `/admin/v1/`），运维 9094，schema `admin`（`admins`、`admin_sessions`、`approvals`、`settings`）。契约 `api/admin/admin.yaml`（不进公开 API 文档）。
- 前端：`https://admin.astras.vip`（`web/apps/admin`，浅色主题，阶段 4 B5 重做完成，见下文「后台页面」）。整站包含 `snippets/admin-access*.conf`，可以挂访问限制，见 [web.md](web.md)；用户 2026-09-30 决定暂不做访问限制，服务器上没有这个文件。阶段 2 的旧后台 `web/admin`（`https://astras.vip/admin/`）已删除，旧地址 301 到新后台的同一路径。本机 `task web:dev -- admin`（http://localhost:5180），`/admin/v1` 代理到测试服。
- 与需求的差异：需求要求独立域名与网关、仅办公网/VPN 访问。学习项目只有一个域名，改为同域名的 `/admin/` 路径 + 独立服务（不经用户网关）+ 强制 TOTP；会话 Cookie 限定 `Path=/admin/`，与用户站的 Cookie 互不可见。阶段 4 起后台有了独立域名 `admin.astras.vip`；访问限制与 TOTP 目前按用户决定暂缓（见下文）。

## 登录与会话

- 没有注册入口：管理员由运维用 `exchangectl admin create` 在 admin-service 容器里创建（它有 `ADMIN_SECRET_KEY`）。
- 登录 = 邮箱 + 密码（Argon2id，至少 12 位）+ 身份验证器 6 位码（RFC 6238，前后一步误差，每个时间步只能用一次）。连续 5 次失败锁定 15 分钟；同一 IP 每分钟最多 10 次登录请求。未知邮箱与已知邮箱耗时相同。
- **暂不校验验证码**（用户 2026-09-30 决定）：开关 `admin.login_without_totp` 打开时只凭邮箱与密码登录。
  - 验证码不要求也不校验；登录页通过 `GET /admin/v1/login-options`（`totp_required`）得知后隐藏验证码输入框（选项读到之前登录按钮等待，读不到时显示输入框）。
  - 登录审计的 `details` 带 `"totp_checked":false`，锁定与限流不变。
  - 测试服已打开这个开关。恢复要求验证码：`exchangectl flags set admin.login_without_totp --off --reason "..."`，5 秒内生效，不用重新部署；已绑定的身份验证器不受影响。
- 会话：随机令牌只存 SHA-256；Cookie `admin_session`，HttpOnly、Secure、SameSite=Strict、Path=/admin/；8 小时到期，1 小时无请求失效；退出或停用管理员时服务端撤销。
- CSRF：除 GET 外每个请求必须带 `X-Admin-CSRF: 1`（跨站表单无法设置自定义头；Cookie 又是 SameSite=Strict）。
- TOTP 密钥用 `ADMIN_SECRET_KEY`（AES-256-GCM，附加数据为管理员 ID）加密存放；换掉这个密钥会让所有管理员的 TOTP 失效，只能重建账号。

## 角色与权限

| 角色 | 权限 |
|---|---|
| ADMIN | 全部，包括只有它有的 `settings.write`（后台设置：双人审批与单人限额） |
| OPERATOR | 读 + 改账户状态、撤销用户挂单（全部或单笔）、改交易对与合约状态、解除合约只减仓与强制平仓（`derivatives.write`）、切换功能开关（后台自己的 `admin.*` 开关除外）、备注与标签（`users.notes`）、账户安全操作与换绑审核（`users.security`）、查看完整联系方式（`users.contacts`）、风控冻结（`ledger.hold`） |
| FINANCE | 读 + 提现审批与搁置、发起与审批手动调账（现货或合约账户）和保险基金注资、充值处置与补记（`deposits.review`）、备注与标签、查看完整联系方式、风控冻结 |
| AUDITOR | 只读（用户（联系方式脱敏）、资产与交易对、合约（`derivatives.read`）、功能开关、提现、审计日志、报表） |

越权返回 403 `ADMIN_FORBIDDEN`。前端按 `/admin/v1/me` 返回的权限列表显示菜单与按钮，但以服务端检查为准。`admin.*` 开关（`admin.login_without_totp`、`admin.two_person_approval`）在开关页也要 `settings.write`，运营不能借开关页关掉双人审批。

## 资金操作：单人与双人审批（2026-10-02 设计 C1）

资金操作 = 手动调账（`MANUAL_ADJUSTMENT`，对手方 `ADJUSTMENT`）、保险基金注资（`INSURANCE_CONTRIBUTION`）与补记充值（`DEPOSIT_BACKFILL`，C2c，见下文「充值处置与补记」）。每笔都是 `approvals` 表的一行（`mode` 为 `SINGLE` 或 `TWO_PERSON`），账本以幂等键 `approval:<id>` 只记一次（补记由 wallet-service 按托管方交易号只记一次）。

- **开关 `admin.two_person_approval`**（未设置即关闭）。打开：每笔都要另一位管理员批准（原流程）。关闭（单人模式，测试服现状，因为只有一位管理员）：有权限的管理员自己执行，但有护栏：
  - 单笔折合不超过 `single_max_usdt`（默认 100,000 USDT）；
  - 同一管理员 24 小时内单人操作合计（含结果未知的待处理项）不超过 `daily_max_usdt`（默认 500,000）；
  - 折合按该资产 USDT 交易对的最新价（market-data-service tickers），USDT 按 1；没有报价的不能单人执行；
  - 超过任一限额、或没有报价时，自动转为待另一位管理员批准，`escalation` 写明原因（`SINGLE_LIMIT`、`DAILY_LIMIT`、`NO_PRICE`；双人模式下是 `TWO_PERSON_MODE`，明确要求审批的是 `REQUESTED`）。
- **提现**：单人模式下，需两人审核的提现（> 20,000 USDT，wallet-service 规则）折合不超过 `withdrawal_max_usdt`（默认 100,000）时一人批准即完成（admin-service 把 `sole_max_usdt` 传给 wallet-service 的内部审核接口，wallet 审计里记 `sole_max_usdt`）。
- **接口**：
  - `POST /admin/v1/users/{id}/adjustments`：用户页的调账，单人模式限额内立即记账（返回 `EXECUTED` 与 `journal_id`），否则返回 `PENDING`；
  - `POST /admin/v1/ledger/adjustments` 与 `POST /admin/v1/derivatives/insurance-fund/contributions`：不带 `direct` 时总是交给另一位管理员（原行为，端到端的双人流程用它），带 `"direct": true` 同上；
  - 可选 `reference`（工单号等，≤ 64 字）写进分录备注；
  - 账本无响应时返回 `COMMON_UNAVAILABLE`，详情 `approval_id` 指向那笔留在 `PENDING` 的操作，申请人可在「审批」里点「完成」重试（`POST /admin/v1/approvals/{id}/decide`，单人模式的操作允许申请人自己完成），不会重复记账。
- **自己的申请**：不能自己批准双人申请（`ADMIN_SELF_APPROVAL`），但可以自己拒绝（撤回）。
- **分录备注只取申请理由**（加 `[reference]`），不再拼审批理由：账本比对幂等请求时包括备注，重试换了理由会被当作冲突（修于 C1）。审批理由在审计里。
- **设置**：`GET /admin/v1/settings`（所有管理员可读，含调用者 24 小时已用额 `daily_used_usdt`），`PUT /admin/v1/settings`（ADMIN，理由必填；限额审计 `admin.settings.changed`，双人开关经开关表审计 `flag:admin.two_person_approval`，本实例立即生效，其它 5 秒内）。也可以用 `exchangectl flags set admin.two_person_approval --on --reason "..."` 打开。
- **审计动作**：`admin.ledger.adjustment_requested/executed/failed/approved/rejected`、`admin.derivatives.insurance_requested/executed/failed/approved/rejected`、`admin.deposits.backfill_requested/executed/failed/approved/rejected`，详情含 `mode`、`escalation`、`value_usdt`（补记还有网络、交易号、哈希、地址与 `"custodian_checked":false`）。

## 待办与实时推送（C1）

- `GET /admin/v1/todo`：待审核提现（最多数到 200）与待处理的资金操作数，按角色返回（没有权限的为 0），读不到的记在 `partial`。
- `GET /admin/v1/events`：Server-Sent Events。连上即发一次 `todo`，之后每 10 秒检查、变了才发；20 秒没有事件发一行注释保活（nginx 读超时 60 秒、Cloudflare 100 秒）；会话结束（退出、过期、停用）时发 `signed_out` 并关闭。流本身不算请求，不会让会话保持活跃。响应头 `X-Accel-Buffering: no` 让 nginx 不缓冲；服务端对这条连接取消读写超时。
- 前端只开一条流，写进 Query 缓存；流断开时每 15 秒轮询 `/todo`。
- C2 起多一项 `identity_requests`：待审核的换绑申请（有 `users.security` 才数，最多数到 200；auth-service 读不到时记在 `partial`）。
- C2c 起多一项 `deposits`：等人处理的充值（有 `deposits.review` 才数，最多数到 200；wallet-service 读不到时记在 `partial`）。铃铛与概览的这一项进入充值页的「待处理」。

## 用户页（2026-10-02 设计 C2）

用户有自己的页面 `/users/<id>`（原抽屉的地址 `?user=<id>` 跳到这里）：左侧是账户摘要与处置，右侧是标签页。

- **摘要**：UID、邮箱与手机号（脱敏；有 `users.contacts` 的可点「显示完整」，每次都审计 `admin.users.contacts_revealed`，只记显示了哪几种、不记值；离开页面即丢弃）、状态与标签、注册与最近登录、风险评分（风控规则最近一次命中的分数与动作）。
- **资料与身份**：基本资料、身份（类型、脱敏值、验证与绑定时间）、已同意的条款与风险披露版本、状态变更时间线（user-service 新增 gRPC `GetUserHistory`）。
- **安全**（auth-service 新增的 gRPC，见 [auth.md](auth.md#管理后台的安全操作grpcc2)）：身份验证器状态与重置、密码修改时间与登录锁定、生成临时密码、活跃会话（逐个或全部退出）、设备、登录记录（游标分页）。操作需 `users.security` 与理由，审计 `admin.users.totp_reset`、`admin.users.password_reset`、`admin.users.sessions_revoked`。
  - **临时密码**只显示一次（应答带 `Cache-Control: no-store`，不进日志与审计）：原密码失效、全部会话退出、登录锁定清除，用户收到密码重置邮件，24 小时内的提现转人工审核。通过可信渠道告知用户，并提醒用户登录后立即修改。用户站暂不强制"下次登录必须改密码"（见设计稿 §10 C2 的遗留）。
- **风控**：风控规则对该用户的评估（触发事件、分数、动作、是否执行、命中规则及说明；risk-service 新增只读 gRPC `ListAssessments`），并可转人工审核（`RISK_REVIEW`）或审核通过后恢复（同「改账户状态」，需 `users.status`）。
- **余额与资金**：现货与合约账户每个资产的可用、冻结、合计与 USDT 估值（按该资产 USDT 交易对的最新价，没有价格的列出来、不计入总估值）；**风控冻结**（`ledger.hold`，ADMIN、OPERATOR、FINANCE）：冻结现货可用余额的一部分或解冻，由账本记分录与审计（见 [ledger.md](ledger.md#接口)）；调整余额可选现货或合约账户。
- **订单**：现货订单（可单笔撤单，`orders.cancel`，审计 `admin.orders.canceled`）与合约当前委托（可单笔撤单，审计 `admin.derivatives.order_canceled`）。
- **仓位**：合约持仓（开仓均价、标记价、预估强平价、未实现盈亏、保证金与模式），每 5 秒刷新；**强制平仓**（`derivatives.write`）先撤该仓位的平仓挂单，再以市价全部平掉，见 [derivatives.md](derivatives.md#管理后台与读模型)。
- 成交、充值、提现、**备注与标签**（`users.notes`；备注只增不改，审计 `admin.users.note_added`；标签为大写代码，整体替换，审计 `admin.users.tags_changed` 带前后值）、审计。
- **身份变更申请**（`/identity-requests`，侧栏「用户」组）：只有一种身份的用户换绑邮箱或手机号要人工审核。默认列出待审核的，值脱敏；有 `users.security` 的可通过（身份改为新值并通知用户）或拒绝，需理由，审计 `admin.users.identity_request_decided`。
- **批量审核提现**：`POST /admin/v1/withdrawals/review-batch` 一次最多 50 笔，用同一个理由逐笔处理、逐笔审计，各自返回结果；审核队列可勾选（一键选中低风险的）后一起批准或拒绝。提现详情列出该用户最近的提现与托管方回调。

接口：`GET /admin/v1/users/{id}`、`/notes`（GET、POST）、`PUT …/tags`、`GET …/security`、`POST …/contacts/reveal`、`GET …/login-history`、`POST …/sessions/revoke`、`POST …/totp-reset`、`POST …/password-reset`、`GET …/history`、`GET …/risk`、`GET /admin/v1/identity-requests`、`POST /admin/v1/identity-requests/{id}/decide`、`GET …/balances`、`GET/POST …/holds`、`DELETE …/holds/{hold}`、`POST …/orders/{order}/cancel`、`GET …/contract-orders`、`POST …/contract-orders/{order}/cancel`、`GET …/positions`、`POST …/positions/close`（契约 `api/admin/admin.yaml`）。与设计稿 §5 的差别：强制平仓按（用户、合约、持仓方向）指定仓位（`POST /admin/v1/users/{id}/positions/close`），不用仓位 ID（平仓后再开会换 ID）；解冻用 `DELETE …/holds/{hold}` 带理由的请求体。

## 提现审核：详情、筛选与搁置（2026-10-02 设计 §4.2，C2c）

- **筛选**：`GET /admin/v1/withdrawals` 多了 `held=true|false`（是否搁置）、`min_value_usdt`、`max_value_usdt`（折合金额区间）、`min_risk`（风控分下限）。
- **详情** `GET /admin/v1/withdrawals/{id}`（`withdrawals.read`）：提现本身、地址在用户地址簿里的记录（标签、添加时间、冷却期结束时间；已删除为 null）、用户今日与本月已提折合（UTC，含审批中与处理中，不含被拒与已撤销）。页面上地址在提现前 72 小时内加入的标「新地址」（同风控规则 `NEW_ADDRESS`），冷却期未过的标「冷却中」。用户限额取决于提现时 step-up 带的身份数与是否绑定身份验证器，钱包不保存，所以只显示已提金额，不显示占用比例。
- **搁置** `POST /admin/v1/withdrawals/{id}/hold`（`withdrawals.review`，`{"hold": true, "note": "..."}`，取消时 `hold: false`）：只限待审批的提现（否则 409 `WALLET_WITHDRAWAL_NOT_IN_REVIEW`）；搁置的仍在队列里，列表标「已搁置」并带备注、搁置人与时间，批准或拒绝时自动取消。wallet-service 审计 `wallet.withdrawal.hold`、`wallet.withdrawal.unhold`（操作者为管理员邮箱）。

## 充值处置与补记（2026-10-02 设计 §4.3，C2c）

充值页有三个视图：全部充值（读模型）、**待处理**（`?view=attention`）、**补记待回调**（`?view=manual`）；后两个直接读 wallet-service（`GET /admin/v1/deposits/review?attention=true|manual_pending=true`，`withdrawals.read`），详情 `GET /admin/v1/deposits/{id}`。处置与补记要 `deposits.review`（ADMIN、FINANCE）。

- **待处理的充值**：低于最小充值额、账户已关闭或不符合资格的（已记在系统科目 `UNCLAIMED_DEPOSIT`）、未支持的代币（没有记账）、补记后托管方回调与录入不一致的（`discrepancy`）。
  - **入账给用户** `POST /admin/v1/deposits/{id}/credit`（理由）：只限已记入 `UNCLAIMED_DEPOSIT` 的，按原币种、原数量由账本 `ReleaseUnclaimed` 转给用户现货账户（分录 `DEPOSIT_CREDIT`，幂等键 `deposit-release:<id>`，账本审计 `ledger.unclaimed_released`），充值变为 `CREDITED`、记下放行分录。不能改数量；未支持的代币与回调不一致的补记不能入账（409 `WALLET_DEPOSIT_NOT_RELEASABLE`），要补偿另做资金调整。
  - **驳回** `POST /admin/v1/deposits/{id}/reject`（理由）：只标记为已处理（`resolution = DISMISSED`），不动资金，wallet-service 审计 `wallet.deposit.dismissed`。处理过的再处理得到 `WALLET_DEPOSIT_RESOLVED`。
- **补记充值**（托管方已到账、回调丢失）：优盾网关没有按交易号查询的接口，系统无法向托管方核对。管理员先在优盾商户后台或区块浏览器核对，再在充值页「补记充值」录入网络、托管方交易号（tradeId）、充值地址、交易哈希与数量：
  - `POST /admin/v1/deposits/manual/check` 只核对不记账：网络由托管方服务、地址是该网络上某个用户的充值地址、`UDUN:<tradeId>` 与（网络、哈希、地址）都没出现过（否则 409 `WALLET_DEPOSIT_KNOWN`，详情带已有充值的 ID）、数量不超过资产精度；返回入账用户、资产、是否低于最小额与折合 USDT。
  - `POST /admin/v1/deposits/manual`（再带理由）是一笔资金操作（`DEPOSIT_BACKFILL`），护栏与调账相同：单人模式限额内立即补记（`EXECUTED`，`result` 为 `deposit <id>`），超过限额、无报价或双人模式时等另一位管理员批准。确认框明示「托管方未核对」。
  - 补记走与回调相同的路径：充值记为 `CONFIRMED`、来源 `MANUAL`、录入人为申请人，由处理器交账本入账（低于最小额同样进 `UNCLAIMED_DEPOSIT`），wallet-service 审计 `wallet.deposit.backfilled`。
  - 托管方的回调晚到时按交易号找到这笔补记：地址、资产、数量一致即记「已核对」（回调日志 `APPLIED`，不再入账）；不一致时不更正、不再入账，回调日志记 `DISCREPANCY`、充值转为待处理并告警（`wallet_custody_deposit_discrepancies_total`，告警 `CustodyDepositDiscrepancy`）。查明后驳回或另做资金调整。
  - 「补记待回调」列出还没等到回调的补记；`exchangectl wallet reconcile --network UDUN`（或 `checks --network UDUN`）的报告在对账表后单列它们——托管方余额里没有对应的到账，就是录错了。

## 功能

- **分页**（阶段 4 B1）：所有列表接口统一用不透明游标分页，返回 `{items, next_cursor}`。`next_cursor` 原样作为下一页的 `cursor` 传回，最后一页为 null；`limit` 为 1–200，默认 50。审计日志与强平记录的 `limit` 最多 500，默认 100。按接口：
  - 提现 `/admin/v1/withdrawals`：可按 `status`（`ALL` 为全部）、`user_id`、`asset` 过滤。审核队列 `PENDING_REVIEW` 默认从旧到新，其余状态从新到旧，`order=asc|desc` 可改。
  - 双人审批 `/admin/v1/approvals`。
  - 审计日志：可按 `actor`、`target`、`event_type`、`from`、`to` 过滤。
  - 强平记录。
- **列表与概览**（阶段 4 B1，B5 接入后台页面）：
  - `GET /admin/v1/users`：账户，新到旧，可按状态、地区、注册时间过滤，数据来自 user-service 的 `ListUsers`。
  - `GET /admin/v1/orders`：现货订单的最新状态，读模型 `orders_current`，可按用户、订单号（B5，全局搜索用）、交易对、状态、方向、时间过滤。交易服务受理前就拒绝的订单没有方向与类型（`side` 为空）。
  - `GET /admin/v1/trades`：现货成交，`user_id` 匹配买卖任一方；`house_side`（B5）是 HOUSE 一方的方向，用户之间成交为空。
  - `GET /admin/v1/deposits`：充值的最新状态，按检测先后，新到旧；可按交易哈希（`tx_hash`，不分大小写，B5）过滤。
  - `GET /admin/v1/dashboard?days=7`：概览，内容为：
    - 账户总数与 24 小时新增；
    - 24 小时成交笔数、活跃交易用户、按报价资产的成交额；
    - 待确认充值与待审核提现；
    - 24 小时风控事件；
    - 行情连接状态与因断流暂停的交易对（market-data 的 `/internal/market/feed`）；
    - 按日的新增用户、成交笔数与 USDT 成交额。

    哪一部分读不到就留空，并记在 `partial` 里。
  - 订单、成交、充值与概览的交易部分来自 ClickHouse，比服务晚几秒。
- **B5 新增接口**：
  - `GET /admin/v1/house`：HOUSE 的账（ADR-0013、0015）。库存是账本 `MARKET_MAKER` 各资产余额，按 USDT 交易对的最新价估值（可充提资产在前，站内资产卖出后为负）；各交易对的成交来自读模型 `trades` 的 `house_side`（买入、卖出、付出与收到），盈亏 = 净持有 × 现价 + 净收入；合约仓位是 `HOUSE_USER_ID` 在 derivatives-service 的持仓（admin-service 从 `apps.env` 读 `HOUSE_USER_ID`）。读不到的部分记在 `partial`。
  - `GET /admin/v1/health`：各服务运维端口 `/readyz` 的就绪状态与耗时（2 秒超时，并发）。目标默认是 compose 网络里的 17 个服务，可用 `HEALTH_TARGETS`（`名称=http://主机:端口,...`）覆盖。
  - `GET /admin/v1/ledger/reconciliation`：账本对账每项检查的最近一次结果与最近 50 次不一致（各带前 10 条差异），经 ledger-service 新增的 gRPC `GetReconciliation` 读 `reconciliation_runs`。
  - `GET /admin/v1/ledger/system-balances?asset=`：全部系统科目余额（留空为全部资产）。
- **提现审批**：按状态列出提现（默认 `PENDING_REVIEW`），显示风控分与命中规则；批准/拒绝需理由，审批人为管理员邮箱（超过 20,000 USDT 需两位不同审批人，规则在 wallet-service）。`exchangectl wallet approve|reject` 仍可用。可按网络筛选（`network`），托管网络的提现带 `custody`、托管方状态 `provider_status` 与交给托管方的时间 `submitted_at`。
- **托管方**（阶段 4 B6，[custody.md](custody.md)）：`GET /admin/v1/custody`（托管方币种与余额、使用它的网络、每个持有方与资产最近一次对账、托管方处理中的提现、待处理回调数）、`GET /admin/v1/custody/callbacks`（`result`、`kind`、`q` 按交易/提现 ID、哈希或地址；游标分页）、`GET /admin/v1/custody/callbacks/{id}`（含原始请求）、`POST /admin/v1/custody/callbacks/{id}/replay`（理由；只限验签通过且 `FAILED`、`UNMATCHED`、`RECEIVED` 的回调，需 `withdrawals.review`，wallet-service 写审计 `wallet.custody.callback.replay`）。
- **用户**：按用户 ID、邮箱或手机号（`+` 开头的 E.164）查找，显示状态与余额；改账户状态（状态机见附录 B，原因为大写代码，例如 `SUSPICIOUS_LOGIN`、`REVIEW_CLEARED`）；强制撤销全部挂单（撮合引擎异步完成）。
- **资产与交易对**：列出资产、网络与交易对；交易对状态是单交易对紧急开关（`TRADING ⇄ HALT`，`CANCEL_ONLY` 之后只能下线，不可恢复交易）。资产与网络参数（精度、充提开关、手续费等）仍以 `deploy/instruments/test.json` 为准，每次部署幂等同步，后台只读——否则下次部署会把后台改动覆盖回去。
- **功能开关**：列出全部已知开关（从未设置的显示为关闭、版本 0），切换启用状态并写理由；地区、账户状态、白名单等规则保持不变（改规则用 `exchangectl flags set`）。服务 5 秒内生效。
- **合约**（阶段 3 任务 10，见 [derivatives.md](derivatives.md#管理后台与读模型)）：每个永续合约的状态、只减仓（原因与时间）、标记价是否新鲜、持仓量；解除只减仓（标记价恢复后才可操作，审计 `admin.derivatives.reduce_only_lifted`）；改合约状态（与交易对同一状态机，instrument-service 记录，审计 `admin.instruments.contract_status`，合约服务约一分钟内按新状态处理）；保险基金余额与 `PNL_CLEARING`；发起保险基金注资（双人审批，类型 `INSURANCE_FUND`）；强平监控（被接管、已预警、保证金率 ≥ 0.5 的仓位，每 5 秒刷新）；强平记录（读模型，可按 WARNING/STARTED/FILLED/ADL 过滤）。
- **双人审批**（原「调账审批」；单人模式见上文「资金操作」）：FINANCE/ADMIN 发起给用户现货账户加（正数）或扣（负数）某资产，另一位有审批权限的管理员批准后，由 ledger-service 以幂等键 `approval:<id>` 记 `MANUAL_ADJUSTMENT` 分录（对手方 `ADJUSTMENT` 系统账户）。自己不能批准自己的申请（可以撤回）；账本拒绝（例如开关 `ledger.manual_adjustment` 关闭）则申请变为 `FAILED`；账本无响应则保持 `PENDING`，可再次批准（幂等键保证不重复记账）。审批期间申请行加锁，两人同时处理时后到者得到 `ADMIN_APPROVAL_DECIDED`。保险基金注资申请（`INSURANCE_FUND`，金额为正）走同一流程，批准后账本 `FundInsurance` 以同样的幂等键记 `INSURANCE_CONTRIBUTION`（对手方 `ADJUSTMENT`），审计 `admin.derivatives.insurance_requested/approved/rejected`。
- **报表**（任务 12，所有角色可读）：来自 ClickHouse 读模型（[analytics.md](analytics.md)），按交易对与 UTC 日的成交笔数、成交量、成交额、受理与被拒订单（含合约）；按资产与日的入账充值（不含未认领）与完成提现（金额、手续费）；任意交易对 1m/5m/15m/1h/4h/1d K 线；按合约与日的成交（双边笔数、成交量与成交额按买方算一次、手续费、已实现盈亏）、资金费付出与收到、强平数、ADL 数、保险基金垫付；当前各合约持仓量（多头、空头、持仓数）。数据比服务晚几秒。
- **审计日志**：按操作者（管理员邮箱、`cli:<用户名>`）或对象（`user:<id>`、`pair:<symbol>`、`contract:<symbol>`、`insurance:<asset>`、`flag:<key>`、`approval:<id>`、`admin:<id>`、`withdrawal:<id>`；充值的补记、入账与驳回记在 `user:<id>` 上）查询 ClickHouse `audit_logs`，写入后几秒可查。后台的每个动作都有审计事件：登录/登录失败/退出、账户状态（user-service 记，操作者为管理员邮箱）、撤单、交易对状态、开关（含前后值）、调账申请/批准/驳回、提现审批（wallet-service 记）。

## 后台页面（阶段 4 B5，设计稿 §10；2026-10-02 重构 C1 起）

`web/apps/admin`。C1 起的外壳（设计 2026-10-02 §3、§6）：

- 浅色为主，顶栏菜单与「设置 → 外观」可切深色（存在本机 `admin.theme`；深色复用用户站令牌）。侧栏是 `data-theme="dark"` 的深色岛，按「概览 · 用户 · 资金 · 交易 · 市场 · 风控 · 运营 · 系统」分组折叠（记在本机），没有权限的项隐藏，可收成图标栏。
- 角标与顶栏铃铛来自事件流（`/admin/v1/events`），数字增加时弹跳。
- 顶栏：环境标识、审批方式（单人/双人，点开设置）、全局搜索（⌘K / Ctrl+K；用户 ID、邮箱、手机号打开用户抽屉，订单号进入订单列表，交易哈希进入充值列表）、待办、主题、账户菜单（邮箱、角色、主题、退出）。
- 动效：登录页左半屏网格与两团漂移光斑、字标描边绘制，表单淡入上移、输入框聚焦底线从中间展开、登录中进度条、成功打勾；验证码 6 格输入（粘贴自动填满）。进入控制台侧栏滑入、卡片间隔 40 ms 淡入；切换页面内容区淡入上移 160 ms；概览数字滚动、趋势图从左向右描画 600 ms、异常服务的状态点呼吸；提示从右上滑入、成功打勾。只动 transform/opacity（字标与对勾的描边除外），`prefers-reduced-motion` 时全部关闭。
- 新页面：资金 → 资金调整（`/adjustments`：审批方式与 24 小时已用额、查找用户、方向/资产/数量/关联单号，单人模式下立即记账并显示分录号与两条分录，或显示待审原因；最近的资金操作）、资金 → 审批（`/approvals`：默认待处理，显示方式与转审原因、申请人与审批人邮箱；自己的单人操作可「完成」，自己的申请可「撤回」）、系统 → 设置（`/settings`：双人审批开关与三个限额，ADMIN 可改、其他人只读；本机主题与语言）。原「账本」页只留对账与系统科目。

| 页面 | 内容 |
|---|---|
| 概览 | 待办（待审提现、待处理资金操作、身份变更申请、待处理充值）、24 小时指标（数字滚动，可点进对应列表）、近 7/30 天成交与新增用户图、17 个服务的就绪状态与耗时、HOUSE 库存估值与盈亏、托管方状态（可访问、短缺、待处理回调、处理中的提现） |
| 用户 | 按 ID/邮箱/手机号查找，按状态、地区、注册时间筛选，列表带标签；行点击进入用户页（见上文「用户页」）；身份变更申请 |
| 订单与成交 | 两个标签；按用户、订单号、交易对、状态、方向、时间筛选；HOUSE 一方显示为 HOUSE；导出已加载的行为 CSV |
| 充值 | 三个标签：全部（按用户、资产、网络、状态、交易哈希筛选）、待处理（入账给用户、驳回）、补记待回调；「补记充值」抽屉（先校验、再走资金操作审批，明示托管方未核对） |
| 提现审批 | 默认待审批队列（旧到新），可切换状态，按折合金额区间、风控分、是否搁置筛选；行点击打开详情：进度、地址簿记录（新地址、冷却中）、今日与本月已提、风控分与命中规则、审批人、批准/拒绝/搁置（带备注）；可勾选批量审核；有新的待审批提现时出现"有新数据"条，不整表轮询 |
| 托管方 | 托管方状态与处理中的提现（可跳到提现列表）、币种与余额、对账（持有、其它持有方、在途、未入账手续费、应有、短缺）、回调日志（筛选、原始请求、重放） |
| 资产与交易对 | 交易对（参考市场与倍数、步长、费率）、资产（充提开关、网络）、合约三个标签，可搜索；交易对与合约按状态机改状态 |
| 仓位（C3） | 全部用户的合约持仓，按保证金率（维持保证金 ÷ 保证金余额）从高到低、每 5 秒刷新；「风险仓位」只看被预警、被接管或保证金率 ≥ 50% 的；可按合约、用户筛选；HOUSE 的仓位标出（用户的对手方，不能在这里平）；其余可强制平仓（`derivatives.write`）。接口 `GET /admin/v1/positions`（最多 500 个，`truncated` 表示还有更多；derivatives-service 的内部接口 `/internal/derivatives/positions`） |
| 强平记录（C3） | 强平引擎的每一步（预警、接管、强平成交、自动减仓），按环节、合约、用户、近 1/7/30/90 天筛选（读模型） |
| 合约与保险基金 | 合约状态、只减仓与解除、标记价、持仓量；保险基金与注资（按审批方式：单人模式限额内立即记账）。风险仓位与强平记录 C3 起各有一页 |
| HOUSE 流动性 | 库存估值（可充提/站内）、各交易对的买卖与盈亏、合约仓位，每 30 秒刷新 |
| 风控与开关 | 全部功能开关（说明、规则摘要、最后修改人），开关切换要确认 |
| 对账与系统科目 | 对账（每项检查最近一次结果与最近的不一致）、系统科目余额（资金调整与审批见上文的新页面） |
| 审计 | 按操作人、对象、事件、时间筛选，导出 CSV，行点击看事件全文 |
| 报表 | 交易、充提、合约（图表与表格两种视图，近 7/30/90 天）与持仓量 |

通用规范（设计稿 §10.2）：

- 列表一律服务端游标分页，每页 50 条，滚到底自动加载下一页；筛选条件写在地址栏（可分享、后退可恢复），可另存为本机"视图"；文本筛选在停止输入 0.5 秒或回车后生效。
- 危险操作统一用确认框：显示对象、理由至少 10 个字（进审计）、手动输入确认词（ID 后 4 位、交易对代码或金额），结果用提示条告知，失败时附可复制的追踪 ID。
- 枚举都有中文标签，悬停显示原始代码；金额按十进制字符串原样显示并加千分位；时间按设置里的时区。
- 端到端：`web/e2e/admin-smoke.mjs`（`scripts/e2e/web.sh` 运行，每次建一个临时 ADMIN、结束停用）：登录、概览、用户页与各标签（资料与身份、安全、余额、风控……）、身份变更申请、搜索、订单与成交、充值（待处理、补记待回调、补记抽屉，不提交）与提现队列（带筛选）、交易对改状态的确认框（取消，不真的改）、合约、HOUSE、开关、对账、审计、报表、资金调整页（审批方式、表单、记录）、审批、设置、事件流、从账户菜单退出，所有 `/admin/v1` 响应按 `api/admin/admin.yaml` 校验。

## 运维

创建管理员（交互使用：不带 `--secrets-stdin` 时随机生成密码与 TOTP 密钥并只显示一次，把 otpauth 链接或密钥录入身份验证器 App）：

```bash
ssh -t exchange 'cd /opt/exchange/infra && sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec admin-service /app/exchangectl admin create --email ops@example.com --name "Ops" --role ADMIN'
```

脚本使用时从标准输入读两行（密码、base32 TOTP 密钥），什么都不打印，见 `scripts/e2e/admin.sh`。其他命令：

```bash
ssh exchange 'cd /opt/exchange/infra && sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T admin-service /app/exchangectl admin list'
```

```bash
ssh exchange 'cd /opt/exchange/infra && sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T admin-service /app/exchangectl admin disable ops@example.com --reason "left the team"'
```

- `ADMIN_SECRET_KEY` 在服务器 `/opt/exchange/infra/admin/admin.env`（目录 700、文件 600，属主 root，只注入 admin-service，不在 `apps.env`），2026-09-29 用 `openssl rand -base64 32` 生成，从未打印。本机开发的在 `.env`。
- 锁定：等 15 分钟自动解锁；忘记密码或丢失 TOTP：停用后用新邮箱重建（没有重置入口，避免成为绕过 TOTP 的后门）。
- IP 白名单（可选）：在服务器建 `/opt/exchange/infra/nginx/snippets/admin-access.local.conf`，内容如 `allow 203.0.113.7; deny all;`，`task deploy` 或 `nginx -s reload` 后对 `admin.astras.vip` 整站生效（真实客户端 IP 由 Cloudflare real-ip 配置还原）。目前按用户决定不设。部署同步不会覆盖或删除这个文件。
- 指标：运维端口 9094（`outbox_pending`、`http_server_*`）；Prometheus 任务 `admin-service`。
- 端到端：`bash scripts/e2e/admin.sh`（对 `https://admin.astras.vip`，每次创建 4 个随机管理员、结束时停用；覆盖页面与安全头、旧地址的跳转、登录与 Cookie、角色、备注与标签、批量审核提现、冻结/解冻、交易对状态往返、撤单、开关往返、双人调账、设置的权限与校验、单人模式（ADMIN 直接 +2.5/−2.5 USDT，超过单笔限额的转审并撤回；双人模式时跳过）、待办与事件流、合约（状态、只减仓、合约状态往返、强平监控与记录、双人保险基金注资 1 USDT）、报表、用户页的估值余额、风控冻结与解冻（用户资金流水里看得到 `ADMIN_FREEZE`）、单笔撤单、合约账户调账（单人模式时）、强制平仓（用户市价买入 0.1 ETH-USDT-PERP，后台平掉后仓位为空；合约交易关闭时跳过）、充值处置与补记（用托管方替身 udun-mock：回调推迟 45 秒的 2 USDT 由 FINANCE 补记、用户余额 +2、同一交易号再补记被拒、晚到的回调记为已核对且不再入账、`exchangectl` 报告里不再列出；低于最小额的 0.5 USDT 入账给用户、0.25 USDT 驳回后不能再入账）、提现详情与搁置（对已完成的提现搁置得到 409）、安全/历史/风控与完整联系方式、换绑审核（用户换绑唯一的邮箱 → 后台通过 → 按新邮箱能查到）、重置身份验证器、全部会话退出（用户令牌立即失效）、临时密码（旧密码失效、临时密码可登录、审计里没有它）、审计查询（含充值处置的四个动作）、退出与停用）。
- admin-service 连 derivatives-service 的内部地址：`DERIVATIVES_SERVICE_URL`（compose 里是 `http://derivatives-service:8095`）。

## 常见错误码

| 错误码 | 含义 |
|---|---|
| `ADMIN_LOGIN_FAILED` | 邮箱、密码或验证码错误，或验证码已用过（`admin.login_without_totp` 打开时只看邮箱与密码） |
| `ADMIN_LOCKED` | 连续失败 5 次，锁定 15 分钟 |
| `ADMIN_UNAUTHORIZED` | 没有会话或会话已过期/撤销 |
| `ADMIN_FORBIDDEN` | 角色没有该权限 |
| `ADMIN_CSRF` | 写请求缺少 `X-Admin-CSRF: 1` |
| `ADMIN_SELF_APPROVAL` | 不能批准自己的双人申请（可以撤回；单人模式下结果未知的操作可以自己完成） |
| `ADMIN_APPROVAL_DECIDED` | 申请已处理 |
| `ADMIN_EXISTS` | `admin create` 的邮箱已存在 |
| `WALLET_DEPOSIT_KNOWN` | 补记的托管方交易号或（网络、哈希、地址）已有充值，详情 `deposit_id` |
| `WALLET_DEPOSIT_NOT_RELEASABLE` | 只有记入 `UNCLAIMED_DEPOSIT`、有币种、回调没有不一致的待处理充值才能入账给用户 |
| `WALLET_DEPOSIT_RESOLVED` | 这笔充值已经处理过 |
| `WALLET_WITHDRAWAL_NOT_IN_REVIEW` | 只有待审批的提现可以搁置 |
