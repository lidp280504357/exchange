# Cloudflare Turnstile 接入

组件已在 Cloudflare 后台创建，站点密钥 `0x4AAAAAAFFaEWha0DGuMHNR`（公开，前端用），密钥只存在本地 `.env` 的 `TURNSTILE_SECRET`（已被 git 忽略）。允许的 hostname 由 `TURNSTILE_HOSTNAMES` 控制，当前为 `astras.vip,localhost`；生产环境不得包含 `localhost`。

## 后端（已实现）

包：`internal/platform/captcha`。

- `captcha.Verifier` 接口：业务层只依赖它。
- `captcha.NewTurnstile(cfg)`：调用 `https://challenges.cloudflare.com/turnstile/v0/siteverify`，10 秒超时，发送 `secret`、`response`、`remoteip`、`idempotency_key`；校验 `success`、`hostname` 在允许列表、`action` 与期望一致；失败返回 `*captcha.VerificationError`，`Reason` 是稳定错误码（`invalid-input-response`、`timeout-or-duplicate`、`hostname-mismatch`、`action-mismatch`、`provider-unavailable`）。
- `captcha.LoadTurnstileConfig(os.Getenv)`：读取 `TURNSTILE_SECRET`、`TURNSTILE_HOSTNAMES`。
- `captcha.Static{Token: "..."}`：本地开发与测试用的固定令牌实现，公开环境禁止启用。

在 `POST /v1/auth/otp/request` 处理器里的用法（阶段 1 实现时照此接入）：

```go
res, err := verifier.Verify(ctx, captcha.Request{
    Token:    body.CaptchaToken,      // 前端表单字段 cf-turnstile-response
    RemoteIP: clientIP(r),            // 经 Cloudflare 代理时取 CF-Connecting-IP
    Action:   "otp_request",
})
if err != nil {
    // 映射为 AUTH_CAPTCHA_REQUIRED（缺失/超长）或 AUTH_CAPTCHA_FAILED；provider-unavailable 记指标并返回 503
}
```

令牌单次有效：同一令牌第二次校验会返回 `timeout-or-duplicate`，前端每次重试前必须 `turnstile.reset(widgetId)`。

## 前端（H5 脚手架建立时接入）

```html
<script src="https://challenges.cloudflare.com/turnstile/v0/api.js" async defer></script>
<form id="otp-form">
  <input name="identifier" />
  <div class="cf-turnstile" data-sitekey="0x4AAAAAAFFaEWha0DGuMHNR" data-action="otp_request"></div>
  <button type="submit">发送验证码</button>
</form>
```

提交时把表单字段 `cf-turnstile-response` 作为 `captcha_token` 发给 `/v1/auth/otp/request`。React 中建议用显式渲染：`turnstile.render(el, { sitekey, action: "otp_request", callback })` 保存 `widgetId`，请求失败重试前调用 `turnstile.reset(widgetId)`。每个使用场景（注册、登录、忘记密码）用不同的 `data-action`，后端按场景传期望的 `Action`。

## 验证

- 密钥有效性：用假令牌调 siteverify，应返回 `success:false` 且错误码为 `invalid-input-response`；若出现 `invalid-input-secret` 说明密钥错误。已于 2026-09-28 验证通过。
- 端到端：H5 上线后在 `astras.vip` 页面取一个真实令牌，第一次校验成功、同一令牌第二次校验被拒。

## 密钥轮换

Cloudflare 后台 Turnstile → 该组件 → Rotate secret key，然后更新 `.env` 与服务器上的环境变量，重启服务即可，站点密钥不变。
