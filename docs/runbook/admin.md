# 管理后台（admin-service 与 web/admin）

实施计划 §6.3 任务 11，需求 §5.12（RBAC、双人审批、操作审计、提现审批、交易对管理、功能开关、用户处置）、§5.14。

## 组成

```
浏览器 https://astras.vip/admin/ ──nginx──> 静态文件（web/admin 构建产物，/opt/exchange/infra/nginx/admin）
                          /admin/v1/* ──nginx──> admin-service:8093（不经用户网关）
admin-service ──gRPC──> auth-service（按邮箱/手机号找用户）、user-service（改账户状态）、
                        ledger-service（余额、手动调账）、instrument-service（资产与交易对、改交易对状态）
              ──内部 REST──> wallet-service（提现列表与审批）、spot-trading-service（撤销用户全部挂单）
              ──config schema──> 功能开关（与 exchangectl flags 同一张表，变更与审计事件同一事务）
              ──ClickHouse audit_logs──> 审计查询
              ──admin schema──> 管理员、会话、双人审批申请；自己的 outbox 发 audit.events
```

- 服务：admin-service，HTTP 8093（nginx 转发 `/admin/v1/`），运维 9094，schema `admin`（`admins`、`admin_sessions`、`approvals`）。契约 `api/admin/admin.yaml`（不进公开 API 文档）。
- 前端：`web/admin`（React + TS + Vite，`base: /admin/`），部署脚本第 6 步在 node 容器里构建后同步到 nginx。本机 `task admin:dev`（http://localhost:5180/admin/，`/admin/v1` 代理到测试服）。
- 与需求的差异：需求要求独立域名与网关、仅办公网/VPN 访问。学习项目只有一个域名，改为同域名的 `/admin/` 路径 + 独立服务（不经用户网关）+ 强制 TOTP；会话 Cookie 限定 `Path=/admin/`，与用户站的 Cookie 互不可见。需要 IP 白名单时见下文。

## 登录与会话

- 没有注册入口：管理员由运维用 `exchangectl admin create` 在 admin-service 容器里创建（它有 `ADMIN_SECRET_KEY`）。
- 登录 = 邮箱 + 密码（Argon2id，至少 12 位）+ 身份验证器 6 位码（RFC 6238，前后一步误差，每个时间步只能用一次）。连续 5 次失败锁定 15 分钟；同一 IP 每分钟最多 10 次登录请求。未知邮箱与已知邮箱耗时相同。
- 会话：随机令牌只存 SHA-256；Cookie `admin_session`，HttpOnly、Secure、SameSite=Strict、Path=/admin/；8 小时到期，1 小时无请求失效；退出或停用管理员时服务端撤销。
- CSRF：除 GET 外每个请求必须带 `X-Admin-CSRF: 1`（跨站表单无法设置自定义头；Cookie 又是 SameSite=Strict）。
- TOTP 密钥用 `ADMIN_SECRET_KEY`（AES-256-GCM，附加数据为管理员 ID）加密存放；换掉这个密钥会让所有管理员的 TOTP 失效，只能重建账号。

## 角色与权限

| 角色 | 权限 |
|---|---|
| ADMIN | 全部 |
| OPERATOR | 读 + 改账户状态、撤销用户挂单、改交易对状态、切换功能开关 |
| FINANCE | 读 + 提现审批、发起与审批手动调账 |
| AUDITOR | 只读（用户、资产与交易对、功能开关、提现、审计日志） |

越权返回 403 `ADMIN_FORBIDDEN`。前端按 `/admin/v1/me` 返回的权限列表显示菜单与按钮，但以服务端检查为准。

## 功能

- **提现审批**：按状态列出提现（默认 `PENDING_REVIEW`），显示风控分与命中规则；批准/拒绝需理由，审批人为管理员邮箱（超过 20,000 USDT 需两位不同审批人，规则在 wallet-service）。`exchangectl wallet approve|reject` 仍可用。
- **用户**：按用户 ID、邮箱或手机号（`+` 开头的 E.164）查找，显示状态与余额；改账户状态（状态机见附录 B，原因为大写代码，例如 `SUSPICIOUS_LOGIN`、`REVIEW_CLEARED`）；强制撤销全部挂单（撮合引擎异步完成）。
- **资产与交易对**：列出资产、网络与交易对；交易对状态是单交易对紧急开关（`TRADING ⇄ HALT`，`CANCEL_ONLY` 之后只能下线，不可恢复交易）。资产与网络参数（精度、充提开关、手续费等）仍以 `deploy/instruments/test.json` 为准，每次部署幂等同步，后台只读——否则下次部署会把后台改动覆盖回去。
- **功能开关**：列出全部已知开关（从未设置的显示为关闭、版本 0），切换启用状态并写理由；地区、账户状态、白名单等规则保持不变（改规则用 `exchangectl flags set`）。服务 5 秒内生效。
- **调账审批（双人）**：FINANCE/ADMIN 发起给用户现货账户加（正数）或扣（负数）某资产，另一位有审批权限的管理员批准后，由 ledger-service 以幂等键 `approval:<id>` 记 `MANUAL_ADJUSTMENT` 分录（对手方 `ADJUSTMENT` 系统账户）。自己不能批准自己的申请；账本拒绝（例如开关 `ledger.manual_adjustment` 关闭）则申请变为 `FAILED`；账本无响应则保持 `PENDING`，可再次批准（幂等键保证不重复记账）。审批期间申请行加锁，两人同时处理时后到者得到 `ADMIN_APPROVAL_DECIDED`。
- **审计日志**：按操作者（管理员邮箱、`cli:<用户名>`）或对象（`user:<id>`、`pair:<symbol>`、`flag:<key>`、`approval:<id>`、`admin:<id>`）查询 ClickHouse `audit_logs`，写入后几秒可查。后台的每个动作都有审计事件：登录/登录失败/退出、账户状态（user-service 记，操作者为管理员邮箱）、撤单、交易对状态、开关（含前后值）、调账申请/批准/驳回、提现审批（wallet-service 记）。

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
- IP 白名单（可选）：在服务器建 `/opt/exchange/infra/nginx/snippets/admin-access.local.conf`，内容如 `allow 203.0.113.7; deny all;`，`task deploy` 或 `nginx -s reload` 后对 `/admin/` 全部生效（真实客户端 IP 由 Cloudflare real-ip 配置还原）。部署同步不会覆盖或删除这个文件。
- 指标：运维端口 9094（`outbox_pending`、`http_server_*`）；Prometheus 任务 `admin-service`。
- 端到端：`bash scripts/e2e/admin.sh`（每次创建 4 个随机管理员、结束时停用；覆盖页面与安全头、登录与 Cookie、角色、冻结/解冻、交易对状态往返、撤单、开关往返、双人调账、审计查询、退出与停用）。

## 常见错误码

| 错误码 | 含义 |
|---|---|
| `ADMIN_LOGIN_FAILED` | 邮箱、密码或验证码错误，或验证码已用过 |
| `ADMIN_LOCKED` | 连续失败 5 次，锁定 15 分钟 |
| `ADMIN_UNAUTHORIZED` | 没有会话或会话已过期/撤销 |
| `ADMIN_FORBIDDEN` | 角色没有该权限 |
| `ADMIN_CSRF` | 写请求缺少 `X-Admin-CSRF: 1` |
| `ADMIN_SELF_APPROVAL` | 不能批准自己的调账申请 |
| `ADMIN_APPROVAL_DECIDED` | 申请已处理 |
| `ADMIN_EXISTS` | `admin create` 的邮箱已存在 |
