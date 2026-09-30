# 前端：PC 站、手机站与管理后台

依据：设计文档 `docs/设计-体验重构与市场钱包扩展-2026-09-30.md` §4、§5、§12；ADR-0012。代码在 `web/`，是一个 pnpm workspace（pnpm 11、Node 24）。技术栈是 React 19、Vite 8、TanStack Query、zustand、react-router 7 与 Tailwind 4，另有 Radix（`radix-ui`）、motion、lucide-react、lightweight-charts 5、@tanstack/react-table 与 react-virtual、react-hook-form 与 zod。

## 站点与目录

| 站点 | 地址 | 代码 | 本机端口 |
|---|---|---|---|
| PC 站 | `https://astras.vip` | `web/apps/pc` | 5173 |
| 手机站（PWA） | `https://m.astras.vip` | `web/apps/m` | 5174 |
| 管理后台（浅色） | `https://admin.astras.vip` | `web/apps/admin` | 5180 |
| 设计系统目录（Storybook） | `https://astras.vip/storybook/` | `web/packages/ui` | 6006 |
| API 参考 | `https://astras.vip/docs/` | 由 `web/apps/pc/scripts/build-docs.mjs` 从 `api/openapi` 生成 | — |
| 旧管理后台（过渡期保留） | `https://astras.vip/admin/` | `web/admin`，见 [admin.md](admin.md) | 5181 |

共享包：

- `web/packages/core`：数据层，内容如下：
  - OpenAPI 客户端与生成类型：`src/api/gen`，由 `api/openapi/*.yaml` 与 `api/admin/admin.yaml` 生成；后台客户端走子路径 `@exchange/core/api/admin`，不进用户站的包。
  - 单一 WebSocket 客户端：`src/ws/client.ts`。
  - 行情存储与 React 钩子：`src/market`。
  - 私有推送写入 Query 缓存：`src/query/private.ts`。
  - 十进制运算与格式化：`src/format`。
  - i18n：`src/i18n`，包括共享文案、枚举与错误码。
  - 会话与设置：`src/session`、`src/settings`。
  - 路由常量：`src/routes.ts`，两个用户站路径一致。
  - 站点切换：`src/site.ts`。
  - 币种静态资料：`assets/coins/*.json`。
  - web-vitals：`src/vitals.ts`。
- `web/packages/ui`：设计系统，内容如下：
  - 令牌：`src/styles/tokens.css`。只有这个文件写颜色值；深色是用户站，`data-theme="light"` 是后台，`data-updown="red-up"` 对调涨跌色。
  - Tailwind 主题：`src/styles/theme.css`。默认调色板已去掉，只能用语义类名，如 `bg-bg-1`、`text-up`。
  - 组件与 Storybook 故事，动效预设在 `src/lib/motion.ts`。

应用之间不互相 import；取数、推送、格式化、校验、i18n 与组件都放共享包。

## PC 站页面（B2）

| 区域 | 路径 | 代码 | 共享逻辑（`packages/core/src`） |
|---|---|---|---|
| 首页、行情、币种 | `/`、`/markets`、`/coin/:symbol` | `pages/markets` | `markets/`：行情列表排序筛选、自选（本机与账户同步）、迷你走势图 |
| 交易终端 | `/trade/:symbol`、`/futures/:symbol` | `pages/trade`（全高 `TerminalShell`，无页脚） | `trading/`：交易对、订单与成交、K 线分页、合约（仓位、保证金、止盈止损、资金费）、终端偏好 |
| 资产 | `/assets`、`/assets/{deposit,withdraw,transfer,history}` | `pages/assets` | `assets/`（估值、流水、划转）、`wallet/`（网络、地址格式、提现计算、充提时间线） |
| 账户 | `/account/{security,sessions,settings}`、`/notifications` | `pages/account` | `user/`（资料、会话、安全、通知）、`auth/`（登录流程、密码规则、注册） |
| 认证 | `/login`、`/register`、`/reset` | `pages/auth`（居中卡片 `AuthShell`） | `auth/` |
| 公告、帮助 | `/announcements`、`/help` | `pages/content` | `content/`：Markdown（`packages/core/content/*.md`，中英两份）与安全的渲染器 |

- 路由：每个区域在 `pages/<区域>/routes.tsx` 登记页面，`App.tsx` 按外壳（`AppShell`、`TerminalShell`、`AuthShell`）挂载；需要登录的页面 `auth: true`，未登录跳 `/login?next=`。
- 文案：外壳的在 `src/i18n.ts`，各区域的在 `src/i18n/<区域>.ts`（命名空间 `pcTrade`、`pcAssets` 等）。`routing.tsx` 的 `lazyPage` 与页面 chunk 并行加载该区域文案并注册，首屏不带全部页面的文字。
- 敏感操作：`features/auth/StepUp.tsx` 的 `useStepUp()`（身份验证器或邮箱/短信验证码换 step-up 令牌），`OtpStep` 是"人机验证 → 发送验证码 → 6 位码"的共用步骤。
- 快捷键：`⌘K`/`Ctrl+K` 全站搜索；终端里 `/` 打开交易对搜索，`B`/`S` 切买卖。

## 手机站页面（B3）

路径与 PC 站完全一致（`packages/core/src/routes.ts`），多一个"我的" tab `/me`。PC 站把 `/me` 转到账户页，切换站点或设备分流都不会落到 404。

| 区域 | 路径 | 代码（`web/apps/m/src`） | 外壳 |
|---|---|---|---|
| 首页、行情、币种 | `/`、`/markets`、`/coin/:symbol` | `pages/home` | 首页与行情：tab 外壳；币种：页面外壳 |
| 交易终端 | `/trade/:symbol`、`/futures/:symbol` | `pages/trade` | tab 外壳 |
| 资产 | `/assets`、`/assets/{deposit,withdraw,transfer,history}` | `pages/assets` | 总览：tab 外壳；其余：页面外壳 |
| 我的与账户 | `/me`、`/account/{security,sessions,settings}`、`/notifications` | `pages/account` | "我的"：tab 外壳；其余：页面外壳 |
| 认证 | `/login`、`/register`、`/reset` | `pages/auth` | 全屏 `AuthShell` |
| 公告、帮助 | `/announcements`、`/help` | `pages/content` | 页面外壳 |

- 外壳（`layout/`）：
  - `MobileShell`：底部 5 个 tab（首页、行情、交易、资产、我的），顶栏的标题与右侧按钮由页面经 `usePageHeader` 填入（`layout/header.tsx`）。
  - `PageShell`：返回栏；直接打开时返回首页或页面指定的上级。
  - `AuthShell`：全屏，右上角关闭回首页。
  - `routing.tsx` 的 `PageRoute.shell` 决定页面挂在哪个外壳。三个外壳在经链接到达新页面时滚到顶部，后退时保留浏览器的滚动位置（`components/useScrollTop.ts`）。
- 交易终端：
  - 图表、盘口、成交三个 tab 可以左右滑动（`parts/SwipeTabs.tsx`）。
  - 买入、卖出按钮固定在 tab 栏上方，点开下单面板（`Sheet`）。下单确认（设置里可关）在面板内完成。
  - 下方是当前委托、历史委托、成交明细；合约另有仓位。
  - 顶栏右侧有币种信息与自选。
- 手势与组件（`components/`）：
  - `PullToRefresh`：下拉刷新。横向滑动与从面板冒上来的触摸不触发。
  - `WindowList`：按页面滚动的虚拟列表，行情 50 行以上只渲染可见的行。
  - `PillBar`：分类胶囊。
  - 长按切换自选。
- 敏感操作：`features/auth/StepUp.tsx` 的 `useStepUp()` 在面板里完成 step-up；面板里再要 step-up 时叠在上面。
- 触控目标不小于 44 px：页面里直接做大。共享组件里的小图标按钮（清除、复制、重试、面板关闭）用 `hit-area` 工具类（`packages/ui/src/styles/theme.css`），在触屏上给出 44 px 的点击区，外观不变。
- 离线：`public/sw.js` 只缓存 `offline.html`，导航请求断网时显示"网络不可用"页；构建产物由 nginx 的 `immutable` 缓存负责，不进 service worker。
- 文案：外壳的在 `src/i18n.ts`（命名空间 `m`），各区域的在 `src/i18n/<区域>.ts`（`mAuth`、`mTrade`、`mAssets`、`mAccount`、`mMarkets`、`mContent`），随页面加载。

## 本机开发

```bash
task web:install            # 装依赖（第一次或改了 package.json 后）
task web:dev                # PC 站 http://localhost:5173；task web:dev -- m|admin|admin-legacy
task web:storybook          # 设计系统目录 http://localhost:6006
task web:check              # 类型与契约一致、无硬编码颜色、类型检查、单元测试（task ci 也跑）
task web:types              # 改了 api/openapi 或 api/admin 后重新生成类型（生成文件提交入库）
task web:build              # 构建全部站点
task web:lighthouse         # 对部署后的两站各三页跑 Lighthouse（性能预算见设计 §12.1）
```

- 代理与来源：PC 站与手机站的 `/v1` 和 WebSocket 由 Vite 代理到 `https://astras.vip`，`API_ORIGIN=...` 可改；后台的 `/admin/v1` 代理到 `https://admin.astras.vip`。
- 端口不能换：刷新令牌 Cookie 的来源白名单（auth-service `ALLOWED_ORIGINS`）和网关 WebSocket 的 `WS_ORIGINS` 默认包含 `localhost:5173` 与 `localhost:5174`，以及线上的 `astras.vip`、`m.astras.vip`。
- Claude Code 预览：`.claude/launch.json` 的 `pc`、`m`、`admin`、`storybook`。手机站在预览里用 `preview_resize` 的 mobile 预设看。

## 数据层要点（设计 §4.3）

- **一条连接**：`createLive()` 建出应用唯一的 `WsClient` 与 `MarketStore`，跟随会话自动鉴权：登录、令牌刷新、退出都在同一连接上处理。
  - 订阅按引用计数，最后一个组件离开后 15 秒才退订，切页不重连。
  - 断线后 1 秒起翻倍到 30 秒、±30% 抖动重连，重连后重放全部订阅，私有频道带 `last_seq`。
  - 深度 `prev_seq` 对不上时只发一次重订阅，直到快照到达都显示"同步中"（`useSyncing`）。
  - 页面隐藏时，高频频道只留最后一条；深度在页面回来时重新要快照。
- **行情存储**：订单簿用二分插入维护，每帧最多通知一次（`requestAnimationFrame`）。
  - 组件用 `useOrderBook(symbol, depth, step)`、`useTrades`、`useTicker`、`useTickers` 读取。
  - 行情列表用 `tickers` 频道，一个订阅拿全部交易对，网关每秒合并发一次变化的。
- **私有推送**：`usePrivateSync(queryClient)` 把 `balances`、`orders`、`fills` 直接写进 Query 缓存；通知、充提、仓位做 500 毫秒去抖后失效重取；`resync` 时重取全部私有数据。
- **金额**：一律用十进制字符串，运算用 `dec`（BigInt 定点），显示用 `formatPrice`（交易对精度）与 `formatAmount`（资产精度，向下截断）。`dec.toNumber` 只用于画图。
- **时间**：`formatTime` 按用户设置的时区（默认浏览器时区），图表与表格一致。
- **枚举**：`enumLabel` 从 i18n 取，缺翻译时显示可读兜底并在开发模式告警。

## 部署

`deploy/server-update.sh` 第 5 步在 `node:24-slim` 容器里对 `web/` 执行一次 `pnpm install --frozen-lockfile`，构建以下内容：

- `pnpm build`：三个站点与旧后台；PC 站构建时同时生成 API 参考；
- `pnpm --filter @exchange/ui build-storybook`：Storybook。

全部成功后才同步到 nginx 的静态目录：`/opt/exchange/infra/nginx/sites/{pc,m,admin,storybook}`，旧后台仍在 `nginx/admin`。旧 H5（`web/h5`，阶段 1–3）在手机站完成后（B3）删除，`/h5/*` 由 nginx 301 到首页。pnpm 缓存在命名卷 `exchange-pnpm-store`，Turnstile 站点密钥取自服务器 `apps.env`。

nginx（`deploy/compose/nginx/conf.d/astras.vip.conf` 与 `snippets/site-{pc,m,admin}.conf`）：

- 三个 server 块共用源站证书（覆盖 `*.astras.vip`，`snippets/tls.conf`）。用户站的 `/v1/` 与 `/v1/ws` 转给网关（`snippets/api.conf`），同源、不需要 CORS。
- 缓存：带哈希的构建产物在 `/static/*`（Vite `build.assetsDir`，后台仍是 `/assets/*`），设 `immutable` 一年；`index.html` 与 SPA 回退 `no-cache`；文本资源 gzip（brotli 由 Cloudflare 做）。用户站的 `/assets/*` 是资产页面的路由（`/assets/deposit` 等），不能被静态资源的 location 拦下。
- **设备分流**：
  - 手机 UA 请求 PC 站的页面时，302 到 `https://m.astras.vip` 的同一路径；桌面 UA 请求手机站时 302 回 PC 站。
  - 有 `site_pref=pc|m` Cookie 时按 Cookie（页脚"切换到电脑版 / 手机版"写入，`Domain=.astras.vip`，一年）。
  - 平板按桌面处理。`/v1/`、静态资源、`/docs/`、`/storybook/`、`/admin/` 不分流。
- **后台访问限制**：`admin.astras.vip` 整站包含服务器上的 `snippets/admin-access*.conf`。用户 2026-09-30 决定暂不做访问限制，服务器上没有这个文件，后台对外可访问。
  - 以后要限制，二选一：
    - IP 白名单：建 `/opt/exchange/infra/nginx/snippets/admin-access.local.conf`，写 `allow <出口 IP>; deny all;`，然后 `nginx -s reload`；
    - Cloudflare Access：在 Cloudflare Zero Trust 给该域名配 Access（邮箱 OTP）。
  - 登录要密码；身份验证器验证码目前由开关 `admin.login_without_totp` 关掉，见 [admin.md](admin.md#登录与会话)。
  - 页面带 `X-Frame-Options: DENY`、`noindex` 与 CSP（`connect-src 'self'`）。

## 性能与检查

- 预算见设计 §12.1：首屏 JS gzip 分别不超过 250 KB（PC）与 200 KB（手机），且不含图表库；CLS 不超过 0.05；Lighthouse 分数分别不低于 90 与 80。
  - 页面按路由懒加载，每页一个 chunk。
  - `index.html` 预取 `/v1/market/pairs` 与 `/v1/market/tickers`；终端的首个价格直接取这份列表。
  - `index.html` 按打开的地址预加载该页的 chunk 及其依赖（构建插件 `web/scripts/route-preload.mjs`；落地页在两个用户站的 `vite.config.ts` 里登记），不必等入口执行完再去请求。
  - 跳动的数字（价格、盘口数量）用 `FlashLayer` 闪动：闪动层在文字后面重挂，文字节点不变。不要用 `key` 重挂文字：每次重挂都是一次新的"最大绘制"，会把 LCP 拖到最后一次跳价。
  - 共享包标了 `sideEffects`，便于摇树。
- `pnpm lint`（`web/scripts/check-tokens.mjs`）：新应用与共享包里不许出现颜色值（`tokens.css` 除外）和直接的 `toLocale*` 调用。
- web-vitals：LCP、CLS、INP、TTFB 输出到浏览器控制台，前缀 `[vitals]`。
- 端到端：`scripts/e2e/web.sh`，内容如下：
  - 三站的首页与 SPA 回退；
  - 缓存头与 gzip；
  - 设备分流，含 `site_pref` 覆盖；
  - 手机站的 SPA 回退、`/static/` 缓存头、manifest、service worker（`no-cache`）与离线页；
  - `/h5/*` 301 到首页；
  - 后台安全头与未登录的 API；
  - `/docs/` 与 `/storybook/`；
  - PC 站浏览器冒烟测试 `web/e2e/pc-smoke.mjs`（workspace 包 `@exchange/e2e`，headless Chrome，中文界面）：表单注册（人机验证用环境的旁路令牌，验证码读开发收件箱）→ 资产页欢迎资金 → 退出再登录（错误密码就地提示、`?next=` 回跳）→ 行情搜索 → 现货终端挂限价单并撤单 → 划转到合约并在资金流水出现 → 充值地址 → 合约终端 → 通知、设备、帮助 → 语言切换 → 退出；页面脚本错误即失败，所有 API 响应按 OpenAPI 契约校验。本机对开发服务器跑：`APP=http://localhost:5173 node web/e2e/pc-smoke.mjs`（`SHOTS=目录` 保存截图）；
  - 手机站浏览器冒烟测试 `web/e2e/m-smoke.mjs`（390 × 844、触屏、iPhone UA，nginx 因此不分流到 PC 站）：表单注册 → 资产 tab 欢迎资金 → 在"我的"里退出（确认面板）再登录 → 行情搜索 → 现货终端从下单面板挂限价单（下单确认）并撤单 → 划转与流水 → 充值地址 → 合约终端 → 通知、设备、帮助 → 语言切换 → 退出；检查同 PC。本机：`APP=http://localhost:5174 node web/e2e/m-smoke.mjs`；
  - 两个冒烟测试共用 `web/e2e/lib.mjs`（Chrome、旁路令牌、开发收件箱、契约校验、按可见文字找按钮）。面板有滑入动画，测试等它停稳（`sheetOpen`）再点，关闭后等遮罩消失再点页面。
- 人工检查清单：[ui-checklist.md](ui-checklist.md)。
