# 登录、令牌与会话运维

需求 §5.2、§6；ADR-0009。实现见 `internal/auth`（注册、登录、会话、step-up、换绑、身份验证器）、`internal/platform/authtoken`（JWT）与 `internal/gateway`（鉴权中间件）。接口契约 `api/openapi/auth.yaml`。

## 令牌

| 令牌 | 形式 | 有效期 | 存放 |
|---|---|---|---|
| 访问令牌 | JWT（Ed25519，`kid` 标识密钥），`sub` 用户、`sid` 会话、`scope` full/read | 15 分钟 | 客户端内存，`Authorization: Bearer` |
| 刷新令牌 | 32 字节随机数，库里只存 SHA-256 | 30 天，每次使用轮换 | WEB：Cookie `rt`（HttpOnly、Secure、SameSite=Strict、Path=/v1/auth/token/refresh）；APP（`X-Client-Type: APP`）：响应体 |
| step-up 令牌 | 随机数，库里只存 SHA-256 | 10 分钟，一次性 | 请求头 `X-Step-Up-Token` |

- 网关用 auth-service 的 `GET /internal/jwks`（只在容器网络内）按 `kid` 取公钥，10 分钟刷新一次，遇到未知 `kid` 最多每分钟重取一次。取不到密钥返回 503，不会把用户踢下线。
- 冻结账户（`FROZEN`）拿到 `scope=read`：网关拒绝 `/v1/auth/*` 以外的写请求（`USER_FROZEN`）。已注销账户（`CLOSED`）不能登录，刷新时会话被吊销。
- 刷新令牌重放：已轮换的令牌再次出现即吊销整个会话（`AUTH_SESSION_REVOKED`，事件 `SessionRevoked{reason=REPLAY}`）；轮换后 5 秒内的重复使用视为多标签页并发，只返回 `AUTH_TOKEN_EXPIRED` 让客户端用新 Cookie 重试。
- 浏览器刷新要带允许的 `Origin`（`ALLOWED_ORIGINS`，默认 `https://astras.vip,http://localhost:5173`），配合 SameSite=Strict 防 CSRF。

## 吊销

会话结束（退出、退出其他设备、改密、重置密码、重放、超出 10 个会话上限、账户注销）时，auth-service 在 Redis 写 `auth:revoked:<session_id>`，TTL 15 分钟（访问令牌寿命），网关每个请求检查。Redis 不可用时网关放行并记警告日志：被吊销的访问令牌最多再用 15 分钟，刷新令牌在库里已失效。

手工踢掉某个用户的全部会话（例如账号被盗）：

```sql
-- 在 auth schema：记下 id 后逐个写 Redis 吊销标记
UPDATE auth.sessions SET revoked_at = now(), revoke_reason = 'USER'
 WHERE user_id = '<user_id>' AND revoked_at IS NULL RETURNING id;
```

```bash
redis-cli SET auth:revoked:<session_id> 1 EX 900
```

## 密码与锁定

- Argon2id（64 MiB、3 轮、1 路），PHC 字符串存 `auth.credentials`；参数写在哈希里，以后调高无需迁移。
- 并发哈希数 `PASSWORD_HASH_CONCURRENCY`（默认 2），auth-service 容器内存上限因此是 384 MiB（`GOMEMLIMIT=320MiB`）。
- 按登录标识计失败次数（Redis `auth:login_fail:<hash>`，窗口 15 分钟，每次失败重置）：第 3 次起要 `captcha_token`，第 10 次锁 15 分钟（`AUTH_ACCOUNT_LOCKED`）。不存在的账号同样计数、同样锁，并做一次假哈希对齐耗时。提前解锁：`redis-cli DEL auth:login_fail:<hash>`，hash 是规范化标识（小写邮箱或 E.164 手机号）SHA-256 的前 12 字节十六进制（`internal/auth/application.hashKey`）：`printf %s alice@example.com | shasum -a 256 | cut -c1-24`。
- 最近 `LOGIN_SILENCE`（默认 168h = 7 天）无成功登录时，密码正确后返回 403 `AUTH_LOGIN_CHALLENGE_REQUIRED`（details 带 `login_challenge_id` 与脱敏的可用通道），客户端用场景 `LOGIN_CHALLENGE` 取验证码后调 `/v1/auth/login/challenge` 完成登录。

## 密钥

| 变量 | 说明 |
|---|---|
| `JWT_SIGNING_KEY` | 32 字节 Ed25519 种子，base64；`openssl rand -base64 32` |
| `JWT_KEY_ID` | 公钥标识，建议用生成日期，如 `k20260928` |
| `TOTP_SECRET_KEY` | 32 字节，base64，加密身份验证器密钥（AES-256-GCM，按用户 ID 绑定）；不设则无法绑定 TOTP。**不能随意轮换**：换掉后已绑定的 TOTP 全部打不开（用户需重新绑定） |

测试服两者已在 `/opt/exchange/infra/apps.env` 生成。本地 `.env` 可留空，进程内随机生成，重启后所有访问令牌失效（刷新令牌仍然有效，客户端刷新即可）。

轮换：在服务器上重新生成两项后 `task deploy`。旧访问令牌因 `kid` 未知被拒（401），客户端用刷新令牌换新令牌，用户无感知。

```bash
ssh exchange 'cd /opt/exchange/infra && sudo sed -i "/^JWT_SIGNING_KEY=/d;/^JWT_KEY_ID=/d" apps.env && { echo "JWT_SIGNING_KEY=$(openssl rand -base64 32)"; echo "JWT_KEY_ID=k$(date +%Y%m%d%H%M)"; } | sudo tee -a apps.env >/dev/null'
```

## 身份验证器（TOTP）

需求 §6.5：step-up 的顺序是 TOTP（已绑定时）> 另一身份的验证码 > 本次登录身份的验证码。

- 绑定：`POST /v1/auth/totp/setup`（需 step-up）返回 base32 密钥与 `otpauth://` 链接，此时为 PENDING；`POST /v1/auth/totp/confirm` 用应用生成的 6 位验证码确认后 ACTIVE，发 `auth.TotpEnabled`（用户收到安全通知与邮件）。重复 setup 会替换待确认的密钥；已绑定时返回 `AUTH_TOTP_ENABLED`。
- 使用：已绑定后 `POST /v1/auth/step-up` 只接受 `totp_code`，邮件或短信验证码票据返回 403 `AUTH_TOTP_REQUIRED`。算法 RFC 6238（HMAC-SHA1、30 秒、6 位），允许前后各一个时间步的误差；每个时间步只能用一次（`last_step`），重放返回 `AUTH_TOTP_INVALID`。
- 解绑：`DELETE /v1/auth/totp`，需要用 TOTP 完成的 step-up，发 `auth.TotpDisabled`。解绑（以及后台重置已绑定的）记 `credentials.totp_changed_at`（迁移 auth 00005），step-up 的安全上下文带上它（`totp_changed_at`）：之后 24 小时内的提现转人工审核（风控 `SECURITY_CHANGE`）。
- 存储：`auth.totp_credentials`，密钥用 `TOTP_SECRET_KEY` 加密后存放。
- 丢失身份验证器：目前没有恢复码，由管理后台（任务 11）人工核验后解绑。

端到端检查：`scripts/e2e/totp.sh`。

## 条款版本

注册必须提交当前的 `TERMS_VERSION` 与 `RISK_DISCLOSURE_VERSION`（`GET /v1/auth/terms` 返回当前值），不一致返回 409 `AUTH_TERMS_OUTDATED`。改版时在 `apps.env` 更新两者后部署；同意记录在 `users.consents`。

## 换绑审核

只有一种身份的账户换绑会写 `auth.identity_rebind_requests`（`PENDING_REVIEW`），由管理后台「身份变更申请」页处理（2026-10-02 设计 C2，见 [admin.md](admin.md#身份变更申请)）：通过时身份改为新值并发 `auth.IdentityRebound`（用户收到通知），拒绝只记结果；处理人（管理员邮箱）与理由记在 `decided_by`、`decision_reason`（迁移 auth 00004）。新值已被其他账户占用时通过失败（`AUTH_IDENTITY_TAKEN`）。

## 管理后台的安全操作（gRPC，C2）

admin-service 经 auth-service 的 gRPC 读写账户安全，权限由后台检查并写审计，auth-service 只记录效果：

- `GetSecurity`：身份（完整值，后台默认脱敏）、身份验证器、密码修改时间与登录锁定剩余秒数、活跃会话（IP 已脱敏）与设备、待审换绑数。
- `ListLoginHistory`：登录记录，IP 已脱敏。
- `RevokeSessions`：结束一个或全部会话，原因 `ADMIN`（`auth.SessionRevoked`，网关立即拒绝其令牌）。
- `ResetTOTP`：删除身份验证器（已绑定的发 `auth.TotpDisabled`，用户收到邮件，记 `totp_changed_at`，24 小时内的提现转人工审核）；`GetSecurity` 返回 `totp_changed_at`，后台安全页显示。
- `SetTemporaryPassword`：生成 16 位临时密码（四组四位，去掉易混字符），替换原密码，结束全部会话、清除密码登录锁定，发 `auth.PasswordChanged{via_reset: true}`（与找回密码相同：用户收到邮件，24 小时内的提现转人工审核）。临时密码只在这次应答里，不进日志与审计。
- `ListIdentityRequests`、`DecideIdentityRequest`：换绑审核（上一节）。

## 清理

auth-service 每小时删除过期一天以上的验证码挑战与票据、登录挑战、step-up 令牌和刷新令牌；会话与登录历史保留。
