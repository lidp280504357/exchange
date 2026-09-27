# 验证码（OTP）运维

需求 §5.3、§6；实现见 `internal/auth`（挑战、频控、人机验证）与 `internal/notification`（投递）。

## 链路

`POST /v1/auth/otp/request` → auth-service 校验 Turnstile、频控（Redis `auth:rl:*`）、建挑战（验证码只存 HMAC）→ 后台 gRPC `SendOtp` → notification-service 按服务商链投递 → `POST /v1/auth/otp/verify` 换一次性票据。

- 不存在的账号（或已注册账号再次注册）生成**诱饵挑战**：响应相同、不发码、任何验证码都返回 `AUTH_OTP_INVALID`。
- 频控：同一目标 60 秒 1 次、每小时 5 次、每天 10 次；同 IP 每小时 20 次；同设备每小时 10 次；短信全局每小时 `SMS_HOURLY_LIMIT`（200）/ 每天 `SMS_DAILY_LIMIT`（1000），超出暂停短信并记错误日志。
- 短信需要功能开关 `auth.sms`，高风险地区用 `--deny-regions` 保持仅邮箱：`exchangectl flags set auth.sms --on --deny-regions XX --reason ...`。

## 服务商

| 通道 | 链（主 → 备） | 说明 |
|---|---|---|
| 邮件 | Resend → mock（非生产） | `RESEND_API_KEY`、`MAIL_FROM`；域名 astras.vip 已在 Resend 验证 |
| 邮件（测试域名） | mock | `MOCK_EMAIL_DOMAINS`，默认 example.com / example.org / example.net，测试永远不发真实邮件 |
| 短信 | mock | 暂无短信供应商合同（决策 #8） |

每个服务商重试 1s、4s、16s 后切换下一个；不可重试的错误（无效地址、鉴权失败）立即切换；连续 5 次失败熔断 60 秒。全部失败时 `notify.deliveries.status = FAILED` 并发 `notification.DeliveryFailed`（死信记录）。指标：`notify_sends_total`、`notify_provider_circuit_open`、`notify_deliveries_failed_total`。

## 开发收件箱（仅非生产）

mock 服务商"发出"的消息存 `notify.mock_messages`（保留 1 天），经网关 `GET /v1/dev/messages?target=<地址>` 查看（手机号的 `+` 要写成 `%2B`）：

```bash
curl -s 'https://astras.vip/v1/dev/messages?target=someone%40example.com'
```

## 端到端测试的人机验证

非生产环境可在 `apps.env` 配 `CAPTCHA_BYPASS_TOKEN`（服务器上已生成，本地 `.env` 同值），请求带该值即视为通过；生产环境配置了它服务会拒绝启动。H5 页面照常使用 Turnstile。
