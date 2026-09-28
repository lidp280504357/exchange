# API 网关运维

需求 §5.1、§7.1、§7.3、§12.1；实现见 `internal/gateway`（`routes.go` 路由表、`auth.go` 鉴权、`limits.go` 限流、`idempotency.go` 幂等键、`ws.go` WebSocket），入口 `cmd/api-gateway`。

## 路由

| 路径 | 鉴权 | 上游 |
|---|---|---|
| `GET /v1/time` | 无 | 网关自身 |
| `/v1/auth/{otp/verify,terms,register/complete,login/*,password/reset/*,token/refresh}` | 无（令牌流程） | auth-service |
| `POST /v1/auth/otp/request` | 可选（STEP_UP 等场景需要） | auth-service |
| 其余 `/v1/auth/*` | 必需 | auth-service |
| `/v1/market/{assets,pairs,pairs/*}` | 无 | instrument-service |
| `/v1/user/*` | 必需 | user-service |
| `/v1/account/*` | 必需 | ledger-service |
| `/v1/notifications`、`/v1/notifications/*` | 必需 | notification-service |
| `GET /v1/ws` | 协议内 `auth` | 网关自身 |
| `/v1/dev/*`（仅非生产） | 无 | notification-service 开发收件箱 |

新的公开路径必须写进 `routes.go`，否则默认要求登录。网关剥掉客户端伪造的 `X-User-Id`/`X-Session-Id`/`X-Auth-Scope`，由鉴权结果重新设置。

## 限流

Redis 计数（键前缀 `gw:rl:`，窗口从第一次命中开始），响应带 `X-RateLimit-Limit`、`X-RateLimit-Remaining`，超限返回 429 `COMMON_RATE_LIMITED` 与 `Retry-After`：

| 规则 | 配额 | 维度 |
|---|---|---|
| `ip` | 1200 次/分 | 所有请求按客户端 IP |
| `ip_auth` | 60 次/分 | 匿名令牌流程与验证码按 IP |
| `user` | 600 次/分 | 登录用户的全部请求 |
| `user_transfer` | 60 次/分 | `POST /v1/account/transfers` |

验证码另有 auth-service 自己的频控（`docs/runbook/otp.md`）。Redis 不可用时放行并记警告（Redis 只放可丢数据，§4）。客户端 IP 取 nginx 的 `X-Real-Ip`（Cloudflare 真实 IP）。

## 幂等键

登录用户的写请求（POST/PUT/PATCH/DELETE）带 `Idempotency-Key`（8–100 位字母、数字或 `_.:-`）时：

- 作用域 = 用户 + 方法 + 路径，Redis 键 `gw:idem:<user>:<method> <path>:<key>`，保存 24 小时。
- 同键同 body：直接重放第一次的状态码与响应体，响应头 `Idempotent-Replayed: true`，不再打到上游。
- 同键不同 body，或第一次请求仍在处理：409 `COMMON_IDEMPOTENCY_CONFLICT`。
- 上游 5xx：释放键，客户端可用同键重试。
- 涉及资金的服务自己在 PostgreSQL 记录键（账本划转），所以 Redis 丢失也不会重复记账。

## WebSocket（`wss://astras.vip/v1/ws`）

nginx 的 `location /v1/ws` 已转发 Upgrade（读超时 300 秒，靠服务端心跳保活）。协议（§7.3）：

```text
→ {"op":"auth","token":"<access_token>"}          ← {"op":"auth","ok":true,"user_id":"..."}
→ {"op":"subscribe","args":["balances","notifications"],"last_seq":12}
                                                   ← {"op":"subscribe","ok":true,"args":[...]}
← {"channel":"balances","seq":13,"data":{"account_type":"SPOT","asset":"USDT","available":"...","frozen":"...","entry_type":"..."}}
← {"channel":"notifications","seq":14,"data":{"id":"...","type":"NEW_DEVICE_LOGIN","title":"...","body":"..."}}
← {"op":"ping","ts":...}   → {"op":"pong"}      （客户端也可发 ping，服务端回 pong）
```

- 私有频道 `balances`（来自 `ledger.BalanceChanged`）、`notifications`（来自 `notification.NotificationCreated`）、`orders`（`order.events`）、`fills`（`trade.events`，买卖双方各一条）、`deposits`（`wallet.deposit.events` 的充值状态变化，见 [wallet.md](wallet.md)）。
- 公共行情频道无需登录：`ticker:{symbol}`、`depth:{symbol}`（快照 + 带 `seq`/`prev_seq` 的增量，每 30 秒重发快照）、`trades:{symbol}`、`candles:{symbol}:{interval}`，见 [market-data.md](market-data.md)。网关各实例从末尾读 `market.depth`、`market.candle.events`、`trade.events`、`order.events`，在本地维护深度与最近一条 ticker/K 线。
- 事件带每用户单调 `seq`；重连时带 `last_seq` 补发缓冲内（每用户最近 1000 条）的缺失事件，缓冲不够时回 `{"op":"resync"}`，客户端改走 REST 全量拉取。
- 服务端每 15 秒 ping，约 35 秒没有 pong 就断开；单连接最多 50 个订阅，单用户最多 10 条连接（超出回 `COMMON_RATE_LIMITED` 并断开）；允许的浏览器来源 `WS_ORIGINS`（默认 `astras.vip,localhost:5173`），没有 Origin 的客户端（App、脚本）不受限。
- 访问令牌过期时推 `{"op":"error","code":"AUTH_TOKEN_EXPIRED"}`，60 秒内重新 `auth` 可继续，否则以 4001 关闭。
- 网关以**无消费组**方式从末尾跟读 `ledger.events` 与 `notification.events`（`kafka.Tail`），每个实例都收到全部事件并按本实例已连接的用户过滤；实例重启期间的事件不补推（客户端重连后用 REST 拉当前状态）。补发缓冲在实例内存中，阶段 1 只有一个网关实例。
- 指标：`ws_connections`、`ws_pushed_total{channel}`。

## 端到端检查

`scripts/e2e/gateway.sh`（`task e2e` 一起跑，需要本机 Node 22+）：限流头、幂等重放、WebSocket 鉴权/订阅/余额与通知推送。
