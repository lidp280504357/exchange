# PC 桌面端（Tauri 2）

实施计划 §2.4（PC 端第二优先级，Tauri 2 复用 H5）与 §6.3 任务 12，需求 §6.6。

## 设计

- `desktop/`：Tauri 2 工程。窗口里跑的就是 `web/h5` 的构建产物（打包进应用，`frontendDist` 指向 `web/h5/dist`），页面、协议与 H5 完全相同。
- WebView 的 origin 是 `tauri://localhost`（Windows 为 `http(s)://tauri.localhost`），与 API（`https://astras.vip`）不同站，`SameSite` Cookie 不会随请求发出。所以 H5 在桌面应用里（运行时检测 `__TAURI_INTERNALS__`，见 `web/h5/src/lib/native.ts`）：
  - 请求 `VITE_API_ORIGIN`（默认 `https://astras.vip`），不带凭据（`credentials: omit`），用 Bearer 访问令牌；
  - 登录、注册等认证请求带 `X-Client-Type: APP`，auth-service 把刷新令牌放在响应体里；H5 把它交给应用的三个命令 `refresh_token_get/set/clear`，存进系统安全存储（`keyring` crate：macOS 钥匙串、Windows 凭据管理器、Linux Secret Service），不进 localStorage；
  - 刷新时把令牌放在请求体里（`{refresh_token, device_id}`），轮换后的新令牌立即存回；服务端拒绝（401）或会话被吊销时清除；退出登录清除；
  - WebSocket 连 `wss://astras.vip/v1/ws`；设备 ID 前缀 `desktop-`。
  - 浏览器里什么都不变（同源、HttpOnly Cookie）。
- 网关允许这几个 origin 跨域（`CORS_ORIGINS`，只回显白名单里的 origin，不允许凭据，预检在网关直接应答 204）；WebSocket 的 `WS_ORIGINS` 加了 `localhost`、`tauri.localhost`。测试服配置在 `deploy/compose/docker-compose.apps.yml`。
- 应用的 CSP：只允许连 `https://astras.vip`、`wss://astras.vip` 与 Turnstile。

## 构建

本机没有 Rust 时只能改工程文件。两种方式：

1. **GitHub Actions**：Actions 页手动运行 `desktop` 工作流（`.github/workflows/desktop.yml`），可选平台（默认 macOS、Windows、Ubuntu），产物是各平台安装包（dmg/app、msi/nsis、deb/AppImage），在运行记录的 Artifacts 里下载。未签名：macOS 首次打开要右键"打开"，Windows SmartScreen 会提示。私有仓库的 macOS 分钟按 10 倍计。
2. **本机**：装 Rust（`rustup`）与 Xcode 后：

```bash
pnpm --dir desktop install
```

```bash
pnpm --dir desktop build
```

开发时 `pnpm --dir desktop dev` 会启动 H5 的开发服务器（`VITE_API_ORIGIN=http://localhost:5173`，经 Vite 代理访问测试服，同源、无需跨域）。

图标在 `desktop/src-tauri/icons/`（由 H5 的图标按 1024 像素重画生成）。`tauri.conf.json` 已按 Tauri 2 的配置 schema 校验通过；Rust 部分尚未在本机编译过（首次构建若报 `keyring` 相关错误，先查它的特性名是否随版本变化）。

## 已知限制

- **人机验证**：注册、验证码登录、找回密码要过 Cloudflare Turnstile，而 Turnstile 按页面的主机名校验。桌面应用的主机名是 `localhost`/`tauri.localhost`，需要在 Cloudflare Turnstile 站点设置里把它们加进允许的主机名，否则这些流程在桌面应用里过不了人机验证。密码登录本身不需要（连续失败 3 次后才要），但 7 天未登录时追加的验证码也要先过人机验证。
- 没有自动更新、托盘与系统通知（需求说仅在需要时加插件）。
- 未签名、未公证。
