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
| 旧管理后台（已删除，阶段 4 B5） | `https://astras.vip/admin/*` 301 到 `https://admin.astras.vip/*` | — | — |

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
    - 文字的对比度（WCAG 4.5:1）：浅色页面上的涨跌、品牌与状态文字用 `text-*-strong`（浅色主题加深，深色主题就是原色）；落在 15% 淡底上的涨、跌、信息、危险文字用 `text-*-soft-fg`（向正文色挪 30%，`Badge` 的 soft 样式用它）；涨、跌、危险的实色底上用黑字（`Badge` 的 solid、买卖与危险按钮、未读数）。
    - 触控尺寸：`--tap`（44 px）映射成 `size-tap`、`h-tap`、`min-h-tap`、`min-w-tap`、`w-tap`。根字号是 14 px，`size-11` 之类的 rem 尺寸只有 38.5 px，要 44 px 时用这组。
    - 层级：`--z-sticky`（30，页面里的粘性表头与吸底栏）< `--z-topbar`（35，两站的顶栏、手机站的底部 tab 栏；PC 的断线提示条在它下面一层，`z-[calc(var(--z-topbar)-1)]`，顶栏的菜单盖得住它——重连时打开的「合约」菜单第一项曾被提示条盖住）< `--z-sheet` < `--z-dialog` < `--z-dropdown` < `--z-toast`，用法如 `z-[var(--z-topbar)]`。sticky 的顶栏自成层叠上下文，里面用 CSS 打开的菜单只能跟着顶栏的层级走，所以顶栏要压住页面里所有粘性元素（B61：行情表的表头曾盖住「合约」菜单的第一项）；Radix 的弹层渲染到 body，不受影响。
  - 组件与 Storybook 故事，动效预设在 `src/lib/motion.ts`。

应用之间不互相 import；取数、推送、格式化、校验、i18n 与组件都放共享包。

## PC 站页面（B2）

| 区域 | 路径 | 代码 | 共享逻辑（`packages/core/src`） |
|---|---|---|---|
| 首页、行情、币种 | `/`、`/markets`、`/coin/:symbol` | `pages/markets` | `markets/`：行情列表排序筛选、自选（本机与账户同步）、迷你走势图 |
| 交易终端 | `/trade/:symbol`、`/futures/:symbol` | `pages/trade`（全高 `TerminalShell`，无页脚） | `trading/`：交易对、订单与成交、K 线分页、合约（仓位、保证金、止盈止损、资金费）、终端偏好 |
| 资产 | `/assets`、`/assets/{deposit,withdraw,transfer,history}` | `pages/assets` | `assets/`（估值、流水、划转）、`wallet/`（网络、地址格式、提现计算、充提时间线） |
| 杠杆账户 | `/assets/margin` | `pages/assets/Margin.tsx`、`parts/MarginDialog.tsx`（手机站 `parts/MarginSheet.tsx`） | `margin/`：`math.ts`（风险率分区与显示、负债、可还）、`hooks.ts`（条款、账户（`margin` 频道的 ACCOUNT 推送由 `query/private.ts` 的 `applyMarginAccount` 直接写进缓存；与该账户上一条被采用的推送比（还没有时与缓存比），余额、冻结、借款或利息变了才重拉借款、可借额度与资金流水，只是估值变了不重拉；比缓存旧的与缺交易对的逐仓推送不用、也不记；账户推送不补发，连接断开重连后重拉一次账户（`onReconnected`）；另每 15 秒刷新，给没有负债、只有价格变化时不推送的账户估值）、可借额度、划转/借币/还币调用、入口判定）、`form.ts`（两站共用的表单逻辑：账户、币种、上限、校验、幂等键）；仪表是 `@exchange/ui` 的 `MarginLevel` |
| 账户 | `/account/{security,sessions,settings}`、`/notifications` | `pages/account` | `user/`（资料、会话、安全、通知）、`auth/`（登录流程、密码规则、注册） |
| 认证 | `/login`、`/register`、`/reset` | `pages/auth`（居中卡片 `AuthShell`） | `auth/` |
| 公告、帮助 | `/announcements`、`/help` | `pages/content` | `content/`：Markdown（`packages/core/content/*.md`，中英两份）与安全的渲染器；后台发布的文章（`GET /v1/announcements`、`/v1/help`）叠加在自带文件之上，同 slug 以接口为准，下线的连同自带文件一起隐藏，1 分钟内到达（见 `admin.md`「运营」） |

- 路由：每个区域在 `pages/<区域>/routes.tsx` 登记页面，`App.tsx` 按外壳（`AppShell`、`TerminalShell`、`AuthShell`）挂载；需要登录的页面 `auth: true`，未登录跳 `/login?next=`。
- 文案：外壳的在 `src/i18n.ts`，各区域的在 `src/i18n/<区域>.ts`（命名空间 `pcTrade`、`pcAssets` 等）。`routing.tsx` 的 `lazyPage` 与页面 chunk 并行加载该区域文案并注册，首屏不带全部页面的文字。
- 敏感操作：`features/auth/StepUp.tsx` 的 `useStepUp()`（身份验证器或邮箱/短信验证码换 step-up 令牌），`OtpStep` 是"人机验证 → 发送验证码 → 6 位码"的共用步骤。
- 快捷键：`⌘K`/`Ctrl+K` 全站搜索；终端里 `/` 打开交易对搜索，`B`/`S` 切买卖。
- 顶栏的下拉菜单（现货交易、合约、资产、账户）只用 CSS：悬停时打开，键盘聚焦时也打开（`group-has-[:focus-visible]`，只认键盘带来的焦点）；点过的菜单项会失焦，所以鼠标点完移开、或键盘按 Enter 跳转后菜单都会收起（B109，用户反馈：以前用 `focus-within`，点击留下的焦点让菜单在鼠标移开后仍开着）。冒烟测试悬停打开"资产"、点"充值"、移开鼠标，要求菜单收起。
- 杠杆账户（杠杆设计 2026-10-06 §7，批次 E4 的第一部分）：全仓卡片（风险率仪表、总资产/总负债/净资产、各币种的可用/冻结/已借/利息/净资产与借、还、划转）、逐仓列表（每个交易对一张，含强平价估算）、借币利率；三个弹窗共用 core 的 `useMarginForm`：划入的上限是现货可用，划出是账户可用（服务端另按预警线与负债限制），借币是 `max-borrowable`（并写明受哪一界限制），还币是负债与可用的较小者，点"最大"且能还清时发 `ALL`。入口：资产侧栏的"杠杆账户"与资产总览总资产卡里的"杠杆账户"一格（PC `assets-margin`、手机 `margin-entry`，显示净额与占比，点进杠杆页），只对 `MARGIN_TRADE` 资格开放的人、或仍有杠杆资产或负债的人显示（关闭后还能还币、划出）。两站的总资产（资产总览、手机首页资产卡、"我的"资产卡与其占比条、24 小时盈亏估算）计入杠杆账户：各币净额（可用 + 冻结 − 借款 − 利息，可为负）按参考价折算，不打 haircut，与现货、合约同一口径（B102，core `valuePortfolio` 的第三个参数、`useMarginHoldings`：杠杆对用户开放时请求账户，否则只用缓存里的）；资产分布环仍只按现货与合约的币种；`margin.enabled` 对用户关闭时页面顶部说明，借币按钮不可点。仪表分区：低于预警线红，预警线以上两倍间距内黄，再往上绿，无负债显示 999。划转、借币、还币的弹窗（手机是 sheet）的表单组件不进这一页的首屏：页面加载完、浏览器空闲时预载（core `useIdleImport`：load 事件之后 `requestIdleCallback`，最多等 3 秒；没有它的浏览器如 iPhone Safari 在 load 之后 3 秒），预载完再打开立即显示（ui 的 `preloadable`：预载完成后 `React.lazy` 的导入是一个立即兑现的 thenable，渲染时不挂起，React 对 Suspense 显示内容的 300 ms 节流因此不再让第一次打开慢三分之一秒；每个块一个实例，在 `pages/assets/parts/lazyMargin.ts` 里供杠杆页与交易页共用，哪一页预载过另一页都直接用；元素类型始终是同一个 lazy 组件，导入前挂上的实例在导入后不会被重挂；导入失败后换一个新的 lazy 组件，下次挂载重新导入）（B108、B114、B115）。
- 杠杆的其它入口（E4、B108）：行情列表与首页（PC 的行情表与实时榜，手机的行情列表与热门币卡片）在支持逐仓的交易对旁标出最高倍数（"10x"，core `useIsolatedLeverage`），按公开的 `GET /v1/margin/pairs` 决定、对所有访客显示（与币安一致，不看登录与资格）；通知中心里 `MARGIN_WARNED` 用警告色、`MARGIN_LIQUIDATING`/`MARGIN_LIQUIDATED` 用危险色的三角警示图标（core `noticeRisk`），归"资产"一类，点开到杠杆账户页（`noticeLink`）；帮助中心"交易"类有《杠杆交易入门》（`packages/core/content/help/margin-trading.{zh-CN,en}.md`：全仓与逐仓、可借额度、计息、风险率与预警/强平线、强平流程、借还方式、划转、风险提示）。
- 交易终端的杠杆模式（E4 的第二部分）：现货下单面板上方的账户切换（现货 / 全仓 / 逐仓，交易对两种币都能作保证金才有全仓、交易对开了逐仓才有逐仓；`margin.enabled` 对用户关闭时整条不显示），杠杆账户下，账户切换下面一行是倍数、借还方式（普通、自动借款、自动还款，下单带 `account` 与 `side_effect`）与划转/借币/还币（同资产页的弹窗或 sheet），按币安压成一行（B120）；可用余额换成该杠杆账户的可用，自动借款时加上可借（`max-borrowable` 每 10 秒刷新），"充值"换成"划转"。表单下方「可用」下面依次是"可借"（下单这一边要花的币——买入是计价币、卖出是基础币——现在最多能借多少）、逐仓的强平价（估算）与风险率（ui `OrderForm` 的 `info`）。PC 的下单面板在三种账户下左右留白相同（`p-3`，根字号 14 px 下为 10.5 px，与合约面板一致），杠杆条下的分隔线通到面板边缘，pc-smoke 量这三处的边距；面板上的划转、借币、还币与余额不足提示里的"划转"都默认这个币。账户选择记在终端偏好（`exchange.terminal` 的 `tradeAccount`），交易对不支持或功能关闭时退回现货；借还方式只在本次访问里有效（不写进偏好，免得哪天忘了自动借款还开着；旧版本存下的在 store 第 2 版的迁移里丢掉，B99）。`margin.auto_borrow` 关闭时下单返回 `MARGIN_DISABLED` 带 `flag` = `margin.auto_borrow`，提示"自动借款暂未开放，借还方式已改为普通"并把借还方式改回普通；别的 `MARGIN_DISABLED`（不带这个 flag）提示"杠杆交易暂未开放"、让资格重新查询，终端退回现货。可借额度只问平台出借的币（保证金资产里 `borrowable` 的），别的不问（会是 422）。杠杆的对话框与 sheet 不进交易页首屏：下单账户是杠杆账户时空闲预载（记住的杠杆账户在页面加载后即预载），预载后打开立即显示（与资产页共用同一个 `preloadable`，B108、B114、B115）。委托列表给杠杆单标"全仓"/"逐仓"；`orders` 推送的受理消息带 `account`、`side_effect`，推送新建的委托也能标上。共享逻辑在 core 的 `margin/trade.ts`。

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
- 触控目标不小于 44 px：页面里直接做大，尺寸用 `size-tap`、`h-tap`、`min-h-tap`，不用 `size-11`、`h-12`（14 px 根字号下只有 38.5、42 px）。共享组件在触屏上（Tailwind 的 `pointer-coarse:`）自己放大：中号与大号按钮、大号输入框、页签、K 线工具栏的周期与指标按钮。共享组件里的小图标按钮（清除、复制、重试、面板关闭）用 `hit-area` 工具类（`packages/ui/src/styles/theme.css`），在触屏上给出 44 px 的点击区，外观不变；`hit-area` 会被横向滚动的容器裁掉，那里要真的做大。
- 离线：`public/sw.js` 只缓存 `offline.html`，导航请求断网时显示"网络不可用"页；构建产物由 nginx 的 `immutable` 缓存负责，不进 service worker。
- 文案：外壳的在 `src/i18n.ts`（命名空间 `m`），各区域的在 `src/i18n/<区域>.ts`（`mAuth`、`mTrade`、`mAssets`、`mAccount`、`mMarkets`、`mContent`），随页面加载。

## 本机开发

```bash
task web:install            # 装依赖（第一次或改了 package.json 后）
task web:dev                # PC 站 http://localhost:5173；task web:dev -- m|admin
task web:storybook          # 设计系统目录 http://localhost:6006
task web:check              # 类型与契约一致、无硬编码颜色、类型检查、单元测试（task ci 也跑）
task web:types              # 改了 api/openapi 或 api/admin 后重新生成类型（生成文件提交入库）
task web:build              # 构建全部站点
task web:lighthouse         # 对部署后的两站各三页跑 Lighthouse（性能预算见设计 §12.1）
```

- 代理与来源：PC 站与手机站的 `/v1`、WebSocket 与上传的头像 `/uploads` 由 Vite 代理到 `https://astras.vip`，`API_ORIGIN=...` 可改；后台的 `/admin/v1` 代理到 `https://admin.astras.vip`。
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
- **平台资料**（设计 2026-10-04 §4.1，`@exchange/core/platform/index`）：两个用户站在根组件调 `useBrandingEffects()`，启动时与之后每分钟读 `GET /v1/platform/profile`，改名、换图不需要重新构建。
  - 读到之后：页面标题、favicon 与 apple-touch-icon、`theme-color`、CSS 的 `--brand`（`--brand-soft` 跟着变，`--brand-fg` 按亮度取深色或白色）都改成资料里的；所有文案里的 `{{brand}}` 换成资料的名称（i18next 的 `defaultVariables`）。
  - 顶栏标志用资料的深色背景标志（没有就用浅色的，再没有用自带的）。页脚的版权、合规文案、联系方式与社交链接也取自资料。
  - 测试模式（`test_mode`，2026-10-04 由学习模式改名）开着时：资料的 `banner` 为真则三个外壳顶部显示横幅（资料文案，空着时显示「测试模式」）；PC 首页与手机站欢迎卡、「我的」身份卡显示「测试模式」徽标；内容页底部显示模拟资金提示；内容只显示测试模式的那一套（见下面「内容按模式」）。关掉就都不显示，内容换成正式的那一套。资料若还是改名前的缓存（只有 `learning_mode`），`normalizeProfile` 按它补出测试模式。
  - 注册方式为 `CLOSED` 时，注册页显示资料里的关闭提示，不显示表单。
  - 「注册即送」文案用资料的 `welcome_credits`（`useWelcomeCredits()`，如「10,000 USDT、0.1 BTC」），清单为空时改用不提赠送的文案。
  - 接口读不到时用 `DEFAULT_PROFILE`：自带的名称、图标与颜色，不显示横幅、不承诺赠送、注册开放。页面不会白屏。
- **法律页与首页横幅**（设计 §4.4）：`/legal/:slug`（terms、privacy、risk、fees、about、contact），PC 站页脚与手机站「我的 → 条款与政策」进入。默认稿在 `web/packages/core/content/legal/`，首页横幅的默认稿是 `content/home/home-hero.*.md`。后台发布的覆盖稿优先，撤回的不显示。单篇文章（法律页、横幅、公告与帮助）先看该栏目的已发布列表（core `listedAs`，每个栏目与语言一分钟取一次）：列表里有才去取这一篇，撤回的直接不显示，列表完整（不足 100 条）而没有它就直接用默认稿、不发请求——接口对没有发布的文章按契约回 404，浏览器会在控制台标红（B117，用户报告首页横幅的 404）；列表取不到或满 100 条时照旧去取。两站冒烟检查首页、行情、币种、资产与条款页首屏没有失败的请求（`lib.mjs` 的 `firstScreenFailures`；首页与条款页另在注册前以访客身份查一次，B118）。
  - 首页横幅：标题、副标题（文章摘要），正文里第一个 Markdown 链接是按钮（`useHero()`）。手机站的欢迎卡在有赠送时仍以赠送为标题。
- **内容按模式**（设计 2026-10-04 §4.4）：站点按资料的测试模式只显示那一种模式的内容，切换模式即自动换稿。
  - 整篇：内置稿的 front matter `modes: TEST | FORMAL | BOTH`（默认 BOTH），后台文章的同名字段由 notification-service 的公开接口按模式过滤（它每 10 秒读一次资料）。打包的「测试环境」公告是 TEST。站点每分钟读一次资料、内容接口缓存 15 秒，切换后约 1 分钟内全部一致；资料读到之前（或读失败之前）内容查询不发，免得测试站先按正式模式取一遍再换；资料请求失败时按全局设置重试一次（约 1 秒后），再失败才按内置的正式模式取内容，所以资料接口出故障时内容晚出现这一次重试的时间。
  - 段落：正文里 `:::test` … `:::` 与 `:::formal` … `:::` 之间的行只在那种模式下保留（只认这两个标签、不嵌套、未闭合算到文末，其它 `:::xxx` 与代码块里的原样保留）。过滤只在 core 的 `renderByMode(md, mode)`（`@exchange/core/content/markdown`）一处，内置稿、后台文章与后台编辑器预览共用；接口照原文返回正文。没写摘要的后台文章，列表里的摘要是该模式下正文的第一段（notification-service 的 `domain.InMode` 按同样的规则取段，再取 `Excerpt`）。
  - 写稿约定：只在测试环境成立的话（Sepolia、模拟短信、1 分钟冷却、「测试环境」字样）放进 `:::test`，需要正式说法的写 `:::formal`；充提与费率的正式说法指向充值、提现页与费率说明，不点名网络；两种模式都不写死注册赠送的数额。模拟资金的提示由内容页底部的提示统一给出，稿子里不再各写一句。模式块里再出现的 `:::test`、`:::formal` 行按普通文字显示；模式块里单独一行 `:::` 就结束这个模式块，所以块里不要再套其它 `:::` 容器（它的结束行会提前结束模式块）。core 的单测断言正式模式下的稿子不含这些字样，演练（`launch-drill.sh`）在两种模式下检查帮助中心的渲染结果。
  - 查询键带上模式（`contentKeys.list(section, locale, mode)`），切换模式是一次新的查询。

## 币本位合约（币本位设计 2026-10-06 §2.6，G4）

- **列表**：core `fetchContracts`（`useContracts` 的取数）读 `/v1/market/contracts?margin_type=ALL`，去掉 `PREPARE` 的合约——币安列表的合约（G1c）先以 `PREPARE` 上架、由合约后端会话分批开放（`house.sh open-contracts`，见 [instruments.md](instruments.md)），开放前两站不显示。`isInverse(c)`（`contract_size` 大于 0）区分币本位；`useSettleAssets()` 列出各结算资产（USDT 与币本位合约的币），每个对应一个合约账户。
- **算术**：core `trading/coinMargined.ts` 按 derivatives-service 的做法算反向合约（ADR-0020）：数量是整数张；每张的保证金（币价值 ÷ 杠杆）与手续费按结算币精度各自向上取整；开仓买单按 min(限价, 标记价) 预留（价格越低一张值的币越多），市价卖单按保护价（标记价 ×(1 − 价格带)，向上取到 tick）；风险限额按币（档位的 `max_notional` 以币计），盈亏按币（张 × 面值 ×(1/开仓价 − 1/标记价)）；`min_notional` 比的是美元面值（张 × 面值）。`contractMath(c, decimals)`（`useContractMath(c)` 取结算币精度）把线性与反向合约统一成一套接口（预留价、开仓成本、最多可开、风险余量、风险检查、名义价值、价值、按标记价的盈亏），两站下单框与仓位卡只调这一套。
- **显示**：数量按张（「张」/ 英文「Cont」），下单框下方显示 ≈ 币与美元；下单框的钱包余额、保证金余额、可用是结算币的合约账户（`useFuturesAccount(asset)`，即 `/v1/derivatives/account?asset=`；USDT 不带参数），划转链接带 `?asset=`；仓位卡的持仓数量按张、保证金与未实现盈亏按币（照币安不另列价值行，用户 05:5x，B134；并排的卡片等高，操作按钮都在底部一行）；止盈止损对话框只会按线性合约估盈亏，币本位的不显示估算；委托、成交与资金费表格按每行的 `settle_asset` 显示金额与币种，数量带「张」。盘口与成交的数量单位为张，成交额为 USD（B119，见 [market-data.md](market-data.md)）。
- **菜单与切换**：PC 顶栏照币安（审查 FE，B131）：「交易」下拉为「现货」「杠杆交易」，「合约」下拉为「U本位合约」「币本位合约」「合约数据」（`/futures/data`，用户 04:46 补充），每项一个图标、标题与一行说明，打开该类型最近访问的交易对或合约（之前没访问过时为 BTC-USDT、BTC-USDT 的全仓杠杆账户、BTC-USDT-PERP、BTC-USD-PERP；「杠杆交易」取最近访问的、两个币都可作全仓抵押的交易对；「现货」「杠杆交易」同时把交易页的账户设为现货或全仓，即 `tradeAccount`；判断抵押币要的杠杆资产列表 `/v1/margin/assets`，登录后才取，访客要到指针或焦点进入「交易」菜单才取，不是每个页面都取，审查 GA，B143）；「现货」的图标是 K 线，不和「划转」共用；「资产」下拉同样样式（用户 06:0x，B135）：资产总览、充值、提现、划转、资金流水各带图标与一行说明；菜单不再列最近的交易对，手机站不变。合约选择器（PC 交易页的 PairPicker、手机 PairSheet）按 U 本位/币本位分组；手机合约页顶部有 U 本位/币本位切换（两类合约都列出时才显示），切到同一个币的另一种合约，没有就到第一个交易中的。
- **数量单位**（审查 FE，B130）：币本位的数量框右侧可选「张 | 结算币 | USD」（core `useOrderAmount`，偏好存在设置 `contractUnit`，默认张）：按币或美元输入时按面值与当时的价格（限价单为限价，市价单为标记价，还没有标记价时为最新价）换成整张（向下取整，大于零而不足一张的按一张），框下的换算行显示另外两种单位，滑块与盘口点选按所选单位填写，下单接口永远收张数；切换单位时把已填的数量换算过去；张数显示成币时向上取到币的精度，再换回来张数不变（审查 FN，B136）。
- **市价单的价格框**（审查 FE，B132）：选市价时价格框清空、置灰、不能点（与限价时同一个输入框，占位「市价」），两站的现货、杠杆与合约下单框一致。
- **现货市价单按数量或金额**（B157 第二版，2026-10-07 用户说清，同币安）：市价单与限价单同样有「数量」与「金额」两栏（价格行照 B132 置灰、占位「市价」），填哪栏按哪栏下单，另一栏清空、按最新价灰字估算（占位「≈ …」）：买入按金额传 `quote_amount`，按数量传 `quantity`（服务端按保护价冻结，见 [trading.md](trading.md)；表单按最新价加价格带检查余额，「可买」与滑块的百分比同样按驱动栏算：按数量时用价格带之上的价，按金额时用最新价，B161）；卖出按数量传 `quantity`，按金额按最新价换算成数量（按 lot 向下取）再传。滑块按出资资产走：买入按可用计价资产设金额，卖出按可用基础资产设数量。从限价切到市价时保留出资那一栏（买入金额、卖出数量），切回限价时两栏按价格重新联动。ui 的 `OrderForm` 两站的现货与杠杆共用（单测覆盖买按金额与数量、余额按价格带、卖按金额换算、切换保留）；合约下单框的市价单按数量下（币安同样）。
- **市价平仓**（审查 FE，B129）：市价单只能成交对手盘当时的深度，未成交的部分被撤销。仓位卡的「平仓」用 core `closeAtMarket`：按剩余数量最多连发三笔只减仓市价单，每笔等到结束（每 400 毫秒读一次订单，最多 10 秒），某笔一点没成交就停；全平了提示「已平仓」，只平了一部分提示已平多少、剩多少，带「继续平仓」按钮；一点没平时按最后一笔的状态说明：被拒绝写出原因，10 秒还没结束提示仍在处理，其余提示对手盘暂时没有深度并带「继续平仓」（B136）。下单框「平仓」页的市价单同样等结果并给出这三种提示。
- **资产**：资产总览的「合约」视图每个持有的结算资产一张合约账户卡（USDT 的总在，`data-testid` 为 `futures-summary` 与 `futures-summary-<币>`）；某币的合约账户行「交易」去以它结算的合约（core `contractFor` 先找结算资产）；划转页从合约账户转出时读该币自己账户的可转额度；币种列表只列合约账户能持有的币（各结算资产，以及合约账户里还有余额的其它币，core `transferCoins`，B128），地址里带的币不在其中时改为 USDT。
- **流程**：两站 flows 的 G4 步（`FLOWS_ONLY=G4`）：用注册赠送买 40 USDT 的 BTC，从划转页转 0.0003 BTC 到合约账户，BTC-USD-PERP 下单框检查数量单位「张」、≈ 币与美元、BTC 的钱包余额与可用，市价开多 1 张（确认框写 1 张与 100 USD），仓位卡检查持仓数量 (张)、价值与未实现盈亏 (BTC)，从卡上市价平仓；手机站从 BTC-USDT-PERP 用顶部切换到币本位。BTC-USD-PERP 不在交易时、或账户剩下的 USDT 不到 45 时（之前的步骤会花掉一些）跳过并写明原因；这一步开始就登记退出清理（`flows-lib.mjs` 的 `coinMarginCleared`）：失败留下的仓位按市价平掉，合约账户里的 BTC 划回现货（B128）。

## 语言：简体、繁体与英文（繁体设计 2026-10-06，G7）

- 语言有 `zh-CN`（简体中文）、`zh-TW`（繁體中文）、`en`（core 的 `LOCALES`）；菜单里每种语言用它自己的文字写名称（`LOCALE_NAMES`，不随界面语言翻译）。首次访问取浏览器的第一语言：`zh-TW`、`zh-HK`、`zh-MO`、`zh-Hant*` 为繁体，其它中文为简体，其余为英文（core `localeOf`）；之后按设置，存在本机。切换入口：PC 顶栏的地球菜单（悬停或键盘聚焦展开，三种语言）、页脚、设置页；手机站设置页。
- `<html lang>` 跟着语言；繁体时字体栈换成繁体字体（ui `styles/index.css` 的 `:root:lang(zh-TW)`：PingFang TC、Microsoft JhengHei、Noto Sans TC、Hiragino Sans TC 排在简体字体之前、系统界面字体之后，拉丁字母与简体时一致；不下载字体文件）。图表的字体随 `lang` 一起更新。
- 繁体文案由简体生成，不手写：`web/packages/core/scripts/gen-zh-tw.mjs`（`pnpm i18n`，即 `task web:i18n`）用 OpenCC 的简→台湾正体（含台湾用语，s2twp）转换，再叠加术语表 `web/packages/core/src/i18n/zh-TW.overrides.ts`。术语表两站共用，按币安繁体站的用语：數據、數位資產、電子郵件、手機號碼、用戶、帳本、登出、註銷（帐户注销，不是登出）、綁定、審核、查看、類型、項目、權限、代碼（币种代码）、重設、取得等；「臺」一律写「台」，「賬」写「帳」。生成物入库：core `src/i18n/zh-TW.ts`、ui `src/i18n.zh-TW.ts`、两站 `src/i18n.zh-TW.ts` 与各分区的 `src/i18n/<分区>.zh-TW.ts`、内容 `content/<栏目>/<slug>.zh-TW.md`、币种资料 `src/coins.zh-TW.ts`（名称与简介），以及通知服务模板里简体文字的繁体对照 `internal/notification/domain/zh_tw_gen.go`（见 [accounts.md](accounts.md)）。
- 改文案只改简体（`zh-CN`）与英文，然后 `pnpm i18n` 并提交生成的文件；`task web:check`（CI 同）用 `pnpm i18n:check` 核对生成物与源一致，不一致就失败。某个词转得不对就加进术语表（左边是简体词，右边是繁体写法；最长匹配优先，OpenCC 自己认识的更长的词仍按它的，如 数据库 → 資料庫），再生成。`node scripts/gen-zh-tw.mjs --convert 文字…` 试转换；`--review` 列出台湾用语的替换、术语表的命中与一对多字的转换结果，供人工过一遍。三种语言的键集合一致由两站的 `src/i18n.test.ts` 检查（core、ui、外壳与每个分区）；两站的页面渲染测试也跑繁体（`pages.tw.test.tsx`）。
- 回落：某个键缺繁体时显示简体（i18next 的 `fallbackLng`）。内容缺繁体文件、后台文章没写繁体时显示简体，并提示「本文暫無繁體中文版，以下為簡體中文原文」。平台资料的文本（`textOf`）与后台写的币种简介同样回落简体；后台只写了简体简介时，繁体页面显示后台的简体，而不是仓库里较旧的繁体。
- 后台（`admin.astras.vip`）不提供繁体界面：`initI18n` 的参数里没有 `zh-TW` 的应用，在繁体浏览器上按简体显示（存下的语言也改回简体）。后台内容编辑器的繁体页签是 G7b（后台会话）。
- 体积：繁体文案和英文一样，与各自的页面块打在一起，不另外请求。入口 JS 增加约 3.5 KB（本机 gzip -9：PC 158.9 → 162.4 KB，手机站 146.4 → 150.0 KB）；全部 JS 约增加 16 KB，主要在币种资料块（约 3.7 KB）与各分区的块（0.3–2.8 KB）。
- 冒烟：两站在设置页切到繁体，检查首页、行情、交易、资产、帮助五页的繁体文字与繁体字体，截图到 `SHOTS`（检查按钮、页签与表头在繁体下的宽度），再切回简体。

## 合约数据（币本位与币安合约数据设计 2026-10-06 §3.3，批次 F）

合约的持仓量、三种多空比（大户账户数、大户持仓量、全市场账户数）、主动买卖量、基差、资金费率历史与爆仓流，数据是 market-data-service 存下的参考市场统计（接口 `GET /v1/market/{symbol}/futures-data`、`/v1/market/{symbol}/liquidations`、`/v1/market/futures/overview`，频道 `liquidations:{symbol}`，见 [market-data.md](market-data.md)「合约数据」）。页面上不写数据来源（ADR-0010）。

| 位置 | PC 站 | 手机站 |
|---|---|---|
| 合约终端 | 中间一栏「图表 \| 数据」页签（`pages/trade/parts/FuturesCenter.tsx`，币安合约页的「交易数据」也在图表区）：图表在「数据」下面照常挂着（`invisible`，不卸载、不改尺寸），数据面板盖在上面、按需加载，指针移到页签栏时开始预载 | SwipeTabs 最后一项「数据」（`pages/trade/parts/FuturesDataTab.tsx`）：第一次切到才加载与取数，切走后停止轮询 |
| 总览 `/futures/data` | 外壳页：U 本位/币本位切换、搜索、可排序的表（标记价、指数价（≥ 1280 px）、24h 涨跌与成交额、持仓量（美元价值与数量）、资金费率与倒计时（倒计时由共享的一秒时钟直接写进文字，行不每秒重绘），16 行起在自己的框里虚拟滚动），上方是该组合计（持仓总价值、24h 成交额、资金费率正/负个数），表下是所选合约的数据面板（行的「数据」或点行；地址 `?symbol=`） | 页面外壳：同样的分组、搜索与合计，排序在下拉框里，合约一行一个，点行在 sheet 里看该合约的数据面板，sheet 底部「去交易」 |
| 行情列表「合约」类别 | 多两列持仓量（美元价值）与资金费率（可排序，`sort=oi\|funding`），≥ 1280 px 时让出 24h 高低的位置，窄屏时让出近 7 天与「交易」按钮（点行即进终端）；工具栏 U 本位/币本位切换（`margin=coin`）与「全部合约数据」链接 | 名称下一行改为「费率 … · 持仓 …」，不画 24h 走势；排序面板多三项（持仓量、资金费率高低）；同样的切换与链接 |

- 共享逻辑在 core `@exchange/core/futures/index`（不进 core 的 index）：`useFuturesData`（显示时按 `nextRead` 在下一点该出来时重取：最新一点的时间加一个周期（主动买卖量记在成交周期的起点，加两个；资金费率加该合约的结算间隔）再加 90 秒（资金费率 120 秒），晚了就每分钟一次，没有点时 5 分钟一次，本机时钟慢于数据时最多等一整个间隔；标签页在后台时不取，回到前台时若下一点已到即补取一次；换周期时先显示上一周期的点并变淡，不跳动）、`useFuturesOverview`（每 30 秒，只在「合约」类别与总览页）、`useLiquidations`（REST 一天内最近 100 条 + 频道推送，按服务的主键 合约、时间、仓位方向 去重，重连后重取一次）、`METRIC_FORMS`/`METRIC_VALUES`（每项统计的图形与数值单位）、`formatValue`、列表的分组与排序（`sortFuturesRows`、`overviewRows`、`overviewTotals`）。
- 哪些合约出现在列表与总览：取终端自己的合约列表（core `useContracts()` 的同一份查询，G4 第二部分起是 `margin_type=ALL`，两种保证金类型都有），所以列表里的合约点进去都能打开；PREPARE 的不出现；列表与总览开着时每分钟重取一次（合约分批开盘）。行情列表等交易对与合约两份都到了才显示（之前是骨架），免得表格在合约晚到时从随页面滚动跳成框内滚动（「全部」200 行起在自己的框里；框至少 min(760 px, 窗口高减 180 px)，并拉长到左侧类别栏的高度，框下不留空白，F19）；合约请求失败时骨架要多等它重试的那一次，之后交易对照常显示、合约行暂缺且页面不另提示，等每分钟的重取或下次进入页面补上（审查 R25，F8）。平台币的两个永续没有 `reference_symbol`：面板不发请求，直接显示"暂无数据"（控制台不出现 404 红字，同 B117）；接口答 404 `MARKET_NO_FUTURES_DATA` 时同样处理。
- 图表是 ui `@exchange/ui/futures/index` 的 `SeriesChart`（SVG，不用图表库，不进 K 线图的块）：每张图一条 y 轴（右侧），持仓量是线加 10% 底色、基差是线、资金费率是按正负着色的柱、三种多空比是多（下）空（上）堆到 100% 的柱、主动买卖量是买在零轴上、卖在零轴下的镜像柱；柱宽不超过 24 px、柱间 2 px、数据端 4 px 圆角。指针、触摸（横向拖动读点，不触发手机终端的左右滑动换页）与方向键都能看每个点的全部数值；每张卡片可切到表格（`SeriesTable`，同样的点，最新在上）；用方向键走到的点由旁边的 `aria-live` 区域读出（图表是 `role="img"`，里面的内容读屏不读）。统计的说明在信息图标的弹层里（点击或轻点，手机上也能看）。涨跌色沿用站点的 `--up`/`--down`（色觉辅助靠位置：零轴上下、堆叠上下与图例）。
- 文案在两站的 `src/i18n/futures.ts`（命名空间 `pcFutures`/`mFutures`），随合约终端、行情页与总览页加载；`vite.config.ts` 的 routePreload 里 `^/futures/data` 排在 `^/futures/` 前面（先匹配者生效）。数据面板是单独的块（ui `preloadable`），不在任何页面的首屏里。
- 冒烟（两站第 7b 步）：合约终端「数据」页签七张图都画出、爆仓有行或写明"最近一天没有爆仓"，PC 切到 1 小时后按 `period=1h` 重取；ASTRA 永续显示"暂无数据"且没有请求；行情「合约」类别有持仓量与资金费率；`/futures/data` 列出合约并画出所选合约的面板（手机在 sheet 里）。

## 头像与用户名（用户头像与用户名设计 2026-10-07，批次 I2）

用户名注册时随机生成（`user_` 加 8 位），可改，7 天一次；头像可上传，没上传时是按用户 ID 选出的内置头像（接口 `GET /v1/user/profile` 的 `username`、`avatar_url`、`avatar_thumb_url`，`PUT /v1/user/username`，`POST`/`DELETE /v1/user/avatar`，见 [accounts.md](accounts.md)）。

| 位置 | PC 站 | 手机站 |
|---|---|---|
| 个人资料页 `/account/profile`（需要登录） | 账户中心左栏第一项：头像卡片（「更换头像」或把图片拖到卡片上，上传时头像外一圈进度、可取消；「恢复默认」先确认）、用户名（「修改」打开对话框，冷却中按钮置灰并写明何时可改）、UID 与注册时间 | 「我的」身份卡的「编辑资料」与设置页顶部一行进入：点头像或「更换头像」打开相册，「恢复默认」先弹确认 sheet；用户名一行点开 sheet 修改；UID 点按复制 |
| 其它显示位置 | 顶栏账户菜单的触发器是头像（点击去个人资料页），菜单头部是头像、用户名与 UID；账户中心左栏顶部同样 | 「我的」身份卡（标题是用户名，不再显示脱敏的邮箱/手机号）；设置页顶部（登录后） |

- 头像组件：ui `Avatar` 是图片加回退（加载时是淡色圆；不用 Radix 的头像原件，它在 PC 顶栏、首屏里），带 `seed`（用户 ID）时，没有图片或图片加载失败就显示 ui `DefaultAvatar`：12 个内置头像（12 个身份色 `--id-*` 各配一个白色几何图形，无文字，内联 SVG、不发请求），按用户 ID 的 FNV-1a 哈希取模 12 选出（`defaultAvatarIndex`，单测钉住了两个 ID 的结果，后台用同一规则）；不带 `seed` 仍是首字母头像。当前用户的头像用 ui `@exchange/ui/profile/MyAvatar`：32 px 及以下用 64 px 的缩略图、更大的用 256 px 的；资料还在读时显示占位圆，免得每次打开页面先闪内置头像再换成上传的。Storybook「Base/Avatar」的 BuiltIn 列出 12 个。
- 上传（core `@exchange/core/user/avatar`）：只收 PNG、JPEG、WebP（20 MB 以内的原图）；在浏览器里取中间的正方形、缩到不超过 512×512 再编码成 WebP（浏览器不能写 WebP 时为 PNG），所以发给服务端的远在它的限制之内（5 MB、边长 2048、每通道 8 位，审查 C52），EXIF 方向由浏览器转正、不带元数据；边长不足 64 像素的在本机就拒绝。用 XHR 发送以显示进度（令牌过期时刷新后重发一次，与 `authFetch` 相同）；服务端回来后先把新图预取进浏览器缓存，再写进资料缓存，页面上所有头像一起换，不闪内置头像。`useAvatarUpload` 给出 准备中 / 上传中（进度，1 表示服务端在保存）/ 失败 三种状态。
- 用户名：`checkUsername` 按契约检查（3–20 个字母、数字或下划线，不以下划线开头，保留名称照 `internal/user/domain/username.go`，以服务端为准）；`nextUsernameChange` 按 `username_changed_at` 加 7 天算出何时可再改（注册时抽到的或被后台重置的为 null，可马上改）。服务端的 `USER_USERNAME_*` 显示在输入框下，五个错误码的文案在 core 的 `errors`。
- 本机开发：两站的 Vite 把 `/uploads` 也代理到测试服（上传的头像由 nginx 直出）。
- 文案在两站的 `src/i18n/profile.ts`（命名空间 `pcProfile`/`mProfile`），随个人资料页加载；顶栏、左栏与设置页用 core 的 `nav.profile`、`common.uid`。
- 冒烟（两站第 8b 步）：新注册用户的用户名是 `user_` 加 8 位、头像是内置的（PC 顶栏菜单与个人资料页，手机「我的」）；改名后立即显示且进入 7 天冷却；在页面里画一张图（PC 900×600 PNG、手机 600×900 JPEG）上传，个人资料页显示服务端的 256 px WebP、PC 顶栏换成 64 px 的、手机「我的」显示上传的；「恢复默认」后回到内置头像。上传、改名与删除的响应也按契约校验。

## App 下载（App 下载页设计 2026-10-07 §4，批次 H3）

两个平台的 App 由后台「系统 → App 下载」设置（外部链接或上传的安装包，见 [admin.md](admin.md)），站点读公开接口 `GET /v1/platform/apps`（某平台不提供时为 null；core `@exchange/core/platform/apps` 的 `usePlatformApps`，每分钟重读）。两个平台都不提供时，下载页显示「暂未提供 App」。下面的入口只看后台「显示下载入口」开关（回答里的 `entry.visible`，H5；core `useAppEntry`，回答没有它或还没读到时按开）：开着时两个平台都没配置也照常显示，PC 顶栏的二维码面板写「暂未提供 App」（读不到接口时写「加载失败 · 请检查网络后重试」，F21）；关着时入口都不出现，`/download` 直接访问仍能打开（H6）。

| 位置 | PC 站 | 手机站 |
|---|---|---|
| 下载页 `/download`（公开） | 每个平台一张卡片：二维码、版本与构建号、更新时间、大小、系统要求（Android 按 API 级别写版本，如 24 → 7.0）、SHA-256（可复制）、更新说明、按钮（APK「下载 APK」、链接「前往下载」/「前往 App Store」；企业签名的 iOS 只能在 iPhone/iPad 上装，不给按钮，二维码下写明用 iPhone 扫码在 Safari 中安装）、安装说明（APK 的未知来源、iOS 的信任企业级开发者）、iOS 的配置描述文件 | 本机平台在前并标「本机」，主按钮（iOS 企业签名为「安装」，即 `itms-services` 链接）、安装说明默认展开；另一个平台在后；不显示二维码 |
| 入口 | 顶栏语言按钮左边的下载图标（悬停或键盘聚焦弹出每个平台的二维码与「更多下载方式」，点击去下载页）；页脚「关于」列「下载 App」 | 「我的」的「其他」组与设置页底部「下载 App」 |

- 二维码里放什么（core `qrUrl`）：链接方式放链接本身（商店直接打开）；上传的安装包放下载页地址加 `?platform=`——手机扫了会被分流到手机站的下载页，那里有安装按钮与说明（`itms-services` 链接放进二维码打不开，Android 也需要未知来源的说明）。`?platform=` 让该平台排在前面。
- 二维码中间贴系统标志（F25，用户 10-09 要求）：ui `PlatformLogo`（Android 机器人头、苹果标，内联 SVG，取 Simple Icons 的 CC0 路径，单色 `currentColor`）放在白底圆角徽章里居中，徽章边长为码的 24%（面积约 6%，要求不超过 20%）；带标志的码用纠错等级 H（`QrCode` 的 `logo`，不带的照旧 M）。PC 下载页卡片与顶栏面板都有，手机站不显示二维码。冒烟第 8c 步用 Chrome 的 BarcodeDetector 从截图读回两张卡片与顶栏面板的码，须等于各自的链接（`lib.mjs` 的 `decodeQr`；Chrome 没有 BarcodeDetector 时——macOS 的有，并非每个平台的都有——打一行 `note` 后照常往下，不读回，F29）。
- 手机站按 UA 判断本机平台（core `devicePlatform`：iPhone/iPad/iPod，或自称 Macintosh 但有触屏的 iPad；Android）。
- 体积：二维码库（qrcode.react）、顶栏的二维码面板（`features/download/lazyQrs.ts`，指针或焦点第一次到下载图标时开始加载，连同文案）与下载页各自成块，不在首屏；顶栏只多一个读 `/v1/platform/apps` 的查询与图标（PC 入口 172.6 → 174.0 KB，手机 150.9 → 151.0 KB）。
- 文案在两站的 `src/i18n/download.ts`（`pcDownload`/`mDownload`）；顶栏、页脚与「我的」用 core 的 `nav.download`、`nav.downloadApp`。
- 冒烟（两站第 8c 步）：先按测试服当时的设置检查（都不提供时「暂未提供 App」，有提供时对应的卡片；PC 顶栏与页脚、手机「我的」的入口按开关有无，开着而都不提供时 PC 顶栏面板写「暂未提供 App」），再用 `lib.mjs` 的 `withApps` 把这一页的 `/v1/platform/apps` 换成一个上传的 Android 安装包加一个 App Store 链接（`APPS_OFFERED`，不改测试服的设置）：PC 两张卡片各有二维码、APK 的大小与系统要求、按钮与商店链接，顶栏下载面板两个二维码，页脚有链接；手机（冒烟用 iPhone 的 UA）iOS 在前且标「本机」、APK 卡片有安装说明，「我的」出现「下载 App」；最后换成开关关闭（`APPS_HIDDEN`）：两站都没有入口，`/download` 仍能打开（H6）。

## 产品线开关（产品线开关设计 2026-10-07 §1 #2、#7，批次 K2）

后台「资产与交易对」页的「产品线」卡开关币币交易、U 本位合约与币本位合约（见 [admin.md](admin.md)、[feature-flags.md](feature-flags.md)）。站点读公开接口 `GET /v1/platform/products`（core `@exchange/core/platform/products` 的 `useProducts`/`useOpenProducts`，每分钟重读，开关变化一分钟内生效；读到之前与读不到时按全开，与服务端"没存的按开"一致——接口出错不会把产品线藏起来）。产品线：交易对属币币交易；合约按规格的 `margin_type`（core `trading/pairs` 的 `fetchContracts` 每读一次合约列表就记下，`marginTypeOf`），列表读到之前按代号（`-USD-PERP` 属币本位，其余属 U 本位）（`productOf`，F18 ③）。

| 关闭后 | PC 站 | 手机站 |
|---|---|---|
| 菜单与入口 | 顶栏「交易」菜单随币币交易整组不出（杠杆交易在现货终端里，§1 #7）；「合约」菜单里 U 本位、币本位各自不出，都关则整组不出；页脚「产品」列同样 | 底栏「交易」与首页快捷入口指向开着的产品线（core `tradeEntry`：最近访问的终端若开着就去它，否则依次现货、U 本位、币本位的默认交易对） |
| 列表与搜索 | 行情列表的类别（现货、合约）、合约类别里 U 本位/币本位的切换（只剩一条线时不出切换、直接列它）、搜索面板、终端的交易对选择器、首页与行情页的板块、合约数据总览的分组 | 行情的类别、搜索、合约类别的切换、终端的交易对面板与 U 本位/币本位切换、首页板块、「我的」的行情一瞥、合约数据总览 |
| 终端地址 | `/trade/*`、`/futures/*` 外包一层 `ProductGate`：关闭的产品线显示「××暂未开放」页（不是 404，终端的代码块不加载；`/futures/` 后面不是合约的地址照旧由终端答）；冷加载时等开关第一次读到再显示（`useProductsRead` 让页面挂起、显示页面骨架，关闭线的终端一刻也不出现；第一次读失败则按全开继续，F18 ②）。裸地址 `/trade`、`/futures` 与顶栏「合约」的链接经 `entryOf` 去开着的产品线（F18 ④） | 同 PC |
| 资产与划转 | 资产页关闭线的合约账户卡把「去合约交易」换成「待处置」，资产行与资金流水的「交易」不指向关闭的终端（`useTradeLinks` 给 null）；划转页向合约账户划入时不列关闭线的币，两条合约线都关时只能从合约账户划出、方向键不可点（F18 ①）；服务端拒绝时 `PRODUCT_CLOSED` 显示「该产品已暂停交易：只能平仓、撤单与把资金划出」 | 同 PC |
| 杠杆 | core `useMarginOpen` 在现货关闭时按关闭算（§1 #7，F18 ⑤）：资产页的杠杆账户卡与左栏入口只给有资金或负债的用户，杠杆账户页显示关闭提示（借币不可用，还币与划出照常）；杠杆划转「划入」只列账户欠着的币并注明「只能划入用于还币」，什么都不欠时只剩「划出到现货」（core `margin/form` 的 `assetChoices`，F20）；划入最多到该币的欠款（含利息，按币的精度向上取整以便还清，`transferInMax`），提示里写出现欠多少；冷打开时账户还没读到也先给「划入」，读到后不欠才转「划出」，换到欠着款的账户或逐仓交易对时再转回「划入」，每次转换清空数量（`inwardOf`、`transferTurn`，F22/F23）；上限在所依赖的数据读到之前写「—」 | 同 PC |
| 待处置 | 资产总览顶部一条提示（关闭的产品线仍有仓位、挂单、合约账户余额，或现货关闭时杠杆账户有资金或负债），点进 `/assets/closed`：仓位市价平仓（只减仓）、挂单撤单、合约账户余额去划转页划出、杠杆账户去杠杆账户页还币与划出；不能开新仓 | 同 PC，卡片式 |

- 首页与行情页的板块：现货关闭时改排开着的合约（先 U 本位，再币本位）；行情页「热门」只剩币本位时按 USD 成交额排。资产总览里关闭产品线的合约账户卡余额为零时不出。
- 待处置的数据（core `@exchange/core/platform/windDown`）只在有产品线关着时读，全开时页面不多发任何请求；它不在首屏代码里。
- 体积：首屏只多读开关的查询、`ProductGate` 与裸地址的转向（PC 入口 174.8 → 176.0 KB，手机 151.1 → 152.2 KB，含 F18）；「暂未开放」页与待处置页各自成块。
- 文案在两站的 `src/i18n/products.ts`（`pcProducts`/`mProducts`），随这两页与资产总览加载。
- 冒烟（两站第 8d 步）：用 `lib.mjs` 的 `withProducts` 把这一页的 `/v1/platform/products` 换成币币交易与 U 本位关闭、币本位开着（`PRODUCTS_PAUSED`，不改测试服的设置）：PC 顶栏没有「交易」、「合约」菜单只有币本位与合约数据，类别没有「现货」，列表与搜索只有 `-USD-PERP`，合约类别没有 U 本位/币本位切换；手机类别没有「现货」、列表与搜索只有币本位、底栏「交易」指向币本位合约、币本位终端没有切换；两站 `/trade/BTC-USDT`、`/futures/BTC-USDT-PERP` 显示「暂未开放」页而 `/futures/BTC-USD-PERP` 照常、裸地址 `/trade` 转到它；资产页出现待处置提示（第 5 步划入合约账户的 12.34 USDT），待处置页给出划出链接。真实的关与开由后台的冒烟与 `admin.sh` 覆盖（后台会话 K3）。

## packages/ui 里这几批的组件与故事（F10）

都按子路径导入、不进 ui 的 index（`DefaultAvatar` 除外），文案由站点以属性传入（三语文案不进故事），Storybook 里只看形态：

| 组件 | 子路径 | 做什么 | Storybook |
|---|---|---|---|
| `SeriesChart` | `@exchange/ui/futures/index` | 合约数据的四种图形（线与底色、按正负着色的柱、堆到 100% 的两份、零轴上下的镜像柱），一条右轴，指针、触摸与方向键读点，`aria-live` 读出键盘走到的点 | Futures/SeriesChart（四种图形、变淡的旧周期、一个点、无点） |
| `FuturesMetric`、`SeriesTable`、`MetricCard`/`InfoHint` | `@exchange/ui/futures/index` | 一张统计卡片：标题与说明弹层、当前值与区间变化、图表与表格切换、加载/无数据/出错 | Futures/FuturesMetric |
| `LiquidationTape` | `@exchange/ui/futures/index` | 爆仓流：最新在上、多空按颜色与文字、价值（USD），后到的一行滑入 | Futures/LiquidationTape（流、按张、持续到来、空） |
| `Avatar`（`seed`）、`DefaultAvatar` | `@exchange/ui` | 头像：图片加回退；`seed` 为用户 ID 时回退到 12 个内置头像之一 | Base/Avatar（首字母、坏图回退、12 个内置、按种子、上传的图） |
| `MyAvatar` | `@exchange/ui/profile/MyAvatar` | 当前用户的头像（读资料，32 px 及以下用缩略图，`decorative` 在挨着用户名处不重复读名） | 需要会话，不单列故事 |
| `UploadRing` | `@exchange/ui/profile/UploadRing` | 头像上传时外面那一圈：准备与保存时转动，上传时按进度填满 | Profile/UploadRing |
| `AppCard`、`AppQrPanel` | `@exchange/ui/download/AppCard`、`…/AppQrPanel` | 下载页的平台卡片（宽：二维码在事实旁，PC；窄：数值靠右，手机）与 PC 顶栏的二维码面板；按钮、二维码与安装说明由站点决定（按钮与安装说明总在卡片底部，并排的两张卡片对齐）。`AppQrPanel` 只有 PC 顶栏一处在用，放进 ui 只为进 Storybook | Download/AppCard（APK、App Store 链接、企业签名 iOS、手机本机/另一平台、顶栏面板） |

手机站的窗口化列表 `WindowList`（`apps/m/src/components/`，合约数据总览也用它）在应用里，不在 ui，不进 Storybook。

## 无障碍与状态

- 减少动效：系统开了"减少动态效果"时，两个用户站的 `MotionConfig` 带 `skipAnimations={prefersReducedMotion()}`（`packages/ui/src/lib/motion.ts`），motion 的淡入、错开入场一并跳过（只设 `reducedMotion="user"` 时不透明度动画仍在）；全局 CSS 把动画与过渡的时长和延迟都清零（`packages/ui/src/styles/index.css`），`Drawer`、`Sheet` 也各自用 `useReducedMotion`。
- 截断文字：`DataTable` 的单元格被省略号截断时（单元格自己，或里面的 `.truncate`、`.text-ellipsis`、多行截断元素），自动带上整段文字的 `title`，放得下时再去掉（`packages/ui/src/data/cutTitles.ts`，每次渲染与宽度变化后测量）。表格之外的截断文字由页面自己给 `title`；`TimeText` 的 `titled` 让绝对时间也带完整时间的提示。
- 空状态：插图、标题、一句说明这里会出现什么、一个下一步（`EmptyState` 的 `title`、`description`、`action`）。交易终端的委托、成交、仓位列表用各站的 `pages/trade/parts/EmptyList.tsx`：PC 站的下一步把光标放进下单表单，手机站的打开下单面板，合约仓位另有"划转"。

## 部署

`deploy/server-update.sh` 第 5 步在 `node:24-slim` 容器里对 `web/` 执行一次 `pnpm install --frozen-lockfile`，构建以下内容：

- 三个站点逐个构建（`pnpm -r --workspace-concurrency=1 --filter "./apps/*" build`，与 `pnpm build` 相同但不并行：每个站点是 `tsc` 加 `vite`，三个同时构建会用尽测试服的内存，2026-10-02 两次让整机几分钟无响应）；PC 站构建时同时生成 API 参考；
- `pnpm --filter @exchange/ui build-storybook`：Storybook。

全部成功后才同步到 nginx 的静态目录：`/opt/exchange/infra/nginx/sites/{pc,m,admin,storybook}`；旧后台 `web/admin` 在新后台完成后（B5）删除，部署时清掉 `nginx/admin`，`astras.vip/admin/*` 由 nginx 301 到 `admin.astras.vip`。旧 H5（`web/h5`，阶段 1–3）在手机站完成后（B3）删除，`/h5/*` 由 nginx 301 到首页。pnpm 缓存在命名卷 `exchange-pnpm-store`，Turnstile 站点密钥取自服务器 `apps.env`。

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
  - 列表里跳动的价格用 `FlashLayer` 闪动：闪动层在文字后面重挂，文字节点不变。不要用 `key` 重挂文字：每次重挂都是一次新的"最大绘制"，会把 LCP 拖到最后一次跳价。
  - 闪动只给列表用（10% 底色、600 ms）。头部大号价格用 `PriceText` 的 `flash={false} arrow`：文字色 150 ms 过渡，旁边的箭头指示最近一次涨跌，箭头位置一直占着，不会挤动旁边的内容。
  - 盘口没有动效。深度每秒推 10 次，任何淡入、滑动、闪动在这个频率下都是闪烁：
    - `OrderBook` 的行是按与价差的名次作 key 的固定槽位，档位进出只改文字；深度条不过渡。
    - `useOrderBook` 的 `every: 250` 限制重画频率，`minQty: displayUnit(数量位数)` 把会显示成 0.0000 的档位并入外侧一档。
    - 默认聚合步长由 core 的 `useBookStep` 按价格取（`defaultBookStep`），用户的选择按交易对存在本机。
    - 公开盘口每边只有 200 档（`PublicDepth`），稠密的盘口里这 200 档只覆盖很窄的价格：BTC-USDT 约 2–40 USDT，按 10 合并从来填不满，稠密时默认的 1 也填不满（2026-10-05 20:20 美股开盘时约 5 USDT，每边五行；B71，审查 CD）。所以盘口把 `steps` 传给 `useOrderBook`：选的步长填不满面板时，按能填满的最粗一档显示（core 的 `OrderBook.fit`）。回到较粗的一档要多出几档余量，免得在临界处来回切换；这样停在细一档时，菜单里不提供更粗的几档。不要为此加大 `PublicDepth`：market-maker 也读公开盘口给 HOUSE 报价，档数一变 HOUSE 的报价和撮合引擎的参考簿都跟着变。
    - 步长框的值仍是所选的步长：实际按更细一档显示时，触发器显示"≈ 0.1"（≈ 用 `text-fg-3`），悬停提示与读屏说明（`aria-describedby`）写明原因（不写服务端的 200 档）：填不满时"盘口深度不够按 1 填满，已按 0.1 显示"，停在细一档等余量时"所选 1 刚够行数、余量不足，暂按 0.1 显示"（`fit` 返回的 `fills` 是能填满的几档，`fits` 是菜单提供的几档）；菜单里不提供的几档变灰，行尾注"深度不足"或"余量不足"，整行悬停有完整的说明；选中当前显示的那一档即存为偏好（B73、B75）。记住的步长按交易对、所选步长与行数区分：面板大小一变（行数变），就按新的行数重新判断，拖动窗口时可能在两档之间切换一下（B71 的设计，审查 CE 记下）。
    - PC 盘口撑满面板（B74，`OrderBook` 的 `onRows`）：在页面上量两侧行容器实际得到的高度（面板减去渲染出来的标签、工具栏、表头与中间价条），每边的行数是能放下的至少 20 px 的行数（5–20 行），行高按这块高度均分（20–24 px，可以是小数像素），最后一行买盘离面板底部不到 1 px；窗口矮到 5 行也放不下时，行按 20 px 裁掉，不留白；超过 20 行 × 24 px 的高度按设计留白。算法是 ui 的纯函数 `fitRows`（有单测）；第一次测量与告诉 `BookPanel` 行数都在浏览器绘制之前（`useLayoutEffect`），首帧就按量出的行数画，只有在量不出时才用 12 行。原来用"面板高度 − 132 px"按 20 px 一行取整，余数（最多 2 行）加上常数的偏差（实际只有 113 px）全留在面板底部；测试模式的横幅让窗口矮的用户又少了一行，空白更明显。
    - `pc-smoke.mjs` 登录后在现货与合约两个终端、1280 × 760、1280 × 800、1440 × 900、1920 × 1080 四个尺寸下在页面上量（不抄 `BookPanel` 的常数）：每边行数等于"两侧高度 ÷ 2 ÷ 20"（5–20），最后一行买盘到面板底边不超过 4 px（20 行 × 24 px 放满之后的留白除外）；再存上步长 10 核一次，触发器显示"≈"并有说明，菜单里 10 变灰。
    - 改动后用无头 Chrome 数 20 秒内 `[data-book-row]` 的挂载次数，目标为 0。预览标签页在后台，会压住推送，测不准。
  - 横向滚动的轮播（公告条等）里不要放视觉隐藏的文字（`sr-only`）。它是绝对定位的，在滚出视野的那一页里不受滚动容器裁剪，手机上会把布局视口撑宽：`position: fixed` 的 tab 栏跟着变宽，只露出前几个，`html` 的 `overflow-x: clip` 挡不住。要补给读屏的文字放进链接的 `aria-label`。排查方法：看 `innerWidth` 是否大于屏宽，再逐个隐藏区块，看哪个让它恢复。
  - 手机站「我的 → 关于 Astras」显示的版本号来自构建时的 `VITE_APP_VERSION`（部署脚本传入提交号），本机为 `dev`。
  - K 线图关掉了 TradingView 角标（`attributionLogo: false`）。图表库许可要求的归属与链接由 `ChartCredit` 显示在 PC 页脚与手机帮助页底部，换图表库或删这两处之前要另找位置放。
  - K 线的图例（日期、开高低收、涨跌、量与均线）叠在图上（半透明底），蜡烛的价格轴顶部按图例实际高度留空（`topMargin`：图例高 + 12 px 占窗格的比例，最少 8 %、最多一半；图例或图表尺寸变化时重算，交易对、周期、指标或宽度不变时只增不减，免得十字线移动时价格轴跟着跳）。手机币种页图例会折成 3–5 行，原来固定的 8 % 让最高的蜡烛压在图例下面（B116，用户报告）。两站冒烟在终端（PC 合约与现货的 1 小时线、手机合约）与币种页（手机日线、PC 1024 宽）上按画布像素检查最高的蜡烛在图例下方（`lib.mjs` 的 `legendClear`）。
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
  - 杠杆（2026-10-06）：PC 与手机冒烟注册后经 `scripts/e2e/lib/margin-user.sh` 只为自己的用户打开 `margin.enabled`（同 `margin.sh` 的做法，脚本结束时恢复原样，所以 `web.sh` 自己持运维锁），再在杠杆账户页从对话框/sheet 划入、划出 10 USDT，在交易终端切到全仓（显示风险率与可借）再切回；单独运行冒烟脚本（没有 `MARGIN_USER_HELPER`）不动开关，杠杆关闭时只检查页面说明"未开放"；
  - PC 站浏览器冒烟测试 `web/e2e/pc-smoke.mjs`（workspace 包 `@exchange/e2e`，headless Chrome，中文界面）：表单注册（人机验证用环境的旁路令牌，验证码读开发收件箱）→ 资产页欢迎资金 → 退出再登录（错误密码就地提示、`?next=` 回跳）→ 行情搜索 → 现货终端挂限价单并撤单 → 划转到合约并在资金流水出现 → 充值地址 → 合约终端 → 通知、设备、帮助 → 语言切换 → 退出；页面脚本错误即失败，所有 API 响应按 OpenAPI 契约校验。本机对开发服务器跑：`APP=http://localhost:5173 node web/e2e/pc-smoke.mjs`（`SHOTS=目录` 保存截图）；
  - 手机站浏览器冒烟测试 `web/e2e/m-smoke.mjs`（390 × 844、触屏、iPhone UA，nginx 因此不分流到 PC 站）：表单注册 → 资产 tab 欢迎资金 → 在"我的"里退出（确认面板）再登录 → 行情搜索 → 现货终端从下单面板挂限价单（下单确认）并撤单 → 划转与流水 → 充值地址 → 合约终端 → 通知、设备、帮助 → 语言切换 → 退出；检查同 PC。本机：`APP=http://localhost:5174 node web/e2e/m-smoke.mjs`；
  - 管理后台浏览器冒烟测试 `web/e2e/admin-smoke.mjs`（阶段 4 B5，1440 × 900）：`web.sh` 经 ssh 建一个临时 ADMIN（随机密码从标准输入传入、不打印，结束时停用），登录后走遍全部页面，见 [admin.md](admin.md#页面一览)；`/admin/v1` 的响应按 `api/admin/admin.yaml` 校验。本机：`ADMIN_EMAIL=… ADMIN_PASSWORD=… APP=http://localhost:5180 node web/e2e/admin-smoke.mjs`（开发服务器第一次打开会预构建依赖并刷新页面，等它就绪再跑）；
  - 三个冒烟测试共用 `web/e2e/lib.mjs`（Chrome、旁路令牌、开发收件箱、契约校验、按可见文字找按钮）。面板有滑入动画，测试等它停稳（`sheetOpen`）再点，关闭后等遮罩消失再点页面。
- 运行时性能（阶段 4 B7）：`task web:perf`（`web/e2e/perf.mjs`，headless Chrome，对已部署的站点，BTC-USDT 每秒约 10 条深度消息）测量 Lighthouse 管不到的 §12.1 预算：
  - 终端页一分钟推流里的主线程长任务（> 50 ms）；
  - 盘口重绘：盘口最多每 250 ms 重绘一次（`BOOK_EVERY`，2026-10-01 用户要安静的盘口），从节流通知它重绘（core 的 `useOrderBook` 在页面有 `__perfBookNotify` 数组时记下时间，只给这个脚本用）到显示它的那一帧，p50/p95/最大值，预算 PC 50 ms、手机 100 ms（B67）；节流的盘口只在通知时变化，其它原因的渲染不带进新档位（`b178292`）：脚本打印一分钟里盘口变化的次数，没有对应通知的变化有一次就不达标：多半是节流被绕过，一次重绘慢过整个节流窗口（250 ms）也会这样；盘口与成交带都等到可见才开始计（手机站的面板隐藏挂载），没有样本的一项打印 "no samples" 并记为不达标；找不到要按的标签（盘口、成交）时记为不达标，跳过依赖它的测量，其余站点照常测；
  - 每条深度消息到它之后第一次通知所在的那一帧，p50/p95/最大值，预算 250 + 50 ms（PC）、250 + 100 ms（手机）（B70）；只观察买卖两侧，中间的最新成交价与标记价不归盘口节流；
  - 每条成交消息到成交带（终端的「最新成交」/「成交」页签）随后第一次变化所在的那一帧，30 秒，预算 50 / 100 ms（不节流）；
  - 切换交易对（新交易对的快照到达到盘口出现）；
  - 离开再回到终端（15 秒宽限内不新建 WebSocket、不重收快照，盘口重新出现的时间）；
  - 手机站以四分之一 CPU（`emulateCPUThrottling(4)`，近似中端手机）跑同样的测量，另测行情列表滚动帧率；
  - 设计系统里 1000 行虚拟表格（Storybook `data-datatable--virtual-1000`）滚动 3 秒的帧率；
  - 杠杆交易（B108，`node web/e2e/perf.mjs margin` 单独跑）：两站各注册一个新账户、经接口划 20 USDT 进全仓、从表单登录，测杠杆账户页重新加载的 LCP（按行情页预算 2.0 / 2.5 s）；入口 JS（`index.html` 自己写的入口模块与 modulepreload 标签，每个地址都一样，压缩后）按首屏预算 250 / 200 KB 判定（审查 DV ③，B114；即 B3 记录的"首屏入口 JS（index.html 引用的入口与预加载）"，量法写在杠杆交易验收记录 §2.3）；首屏前拉取的全部 JS 只报告，与同一会话下资产总览的并列，并写明其中路由预加载（`route-preload.mjs` 按地址加的）占多少；页面空闲时预载的对话框块（`MarginDialog`/`MarginSheet` 那次动态导入一并请求的块，按 modulepreload 链接的批次认出）从首屏里剔除、单独报告大小与相对首屏的请求时间（量之前等这批块到齐，最多 15 秒）；从资产总览经链接回到杠杆账户页（≤ 200 ms）；经链接进交易页、把下单表单切到全仓到仪表出现（≤ 200 ms）、对话框块都到了之后打开借币弹窗（只报告）、全仓模式下一分钟的长任务（终端预算）；最后重新加载交易页（记住了全仓）：对话框块在 load 事件（手机：打开下单 sheet）之后多久预载，以及这时打开借币弹窗（只报告）。杠杆对新账户没开时跳过。`PERF_DEBUG=1` 打印入口、路由预加载、首屏与空闲预载里最大的 JS 文件。结果记在 [杠杆交易验收记录](../杠杆交易验收记录-2026-10-06.md) §2.3。
  - `node web/e2e/perf.mjs memory`：PC 终端页 30 分钟（`MINUTES`）的 JS 堆增长（前后各强制一次 GC）。
  未达预算时退出码非 0；`BUDGET=warn` 只报告。结果记在 `docs/阶段4验收报告.md`。
- 检查清单流程（2026-10-04）：`scripts/e2e/webflows.sh [pc] [m] [admin]`（`task e2e` 里排在 `web.sh` 之后；单独运行先持运维锁：`scripts/ops/lock.sh run -- bash scripts/e2e/webflows.sh pc m admin`）把 [ui-checklist.md](ui-checklist.md) 能稳定判断的项在 headless Chrome 里逐项走一遍，每站约 4–6 分钟（网络慢时到 12 分钟）。逐项对应与结果见 `docs/阶段4验收报告.md` §8.1。
  - 脚本：`web/e2e/pc-flows.mjs`、`m-flows.mjs`、`admin-flows.mjs`，公共部分 `flows-lib.mjs`（建在 `lib.mjs` 的 Chrome 之上）。每一步以清单编号命名（`3`、`P4`、`M1`、`A2`），在自己的标签页（独立的浏览器上下文，各自的设备、时区、减少动效等媒体特性）里跑；只等事件与状态，不靠定时等待。
  - 第 1 步（两站）：访客依次打开 `PRIVATE` 列表里每个需要登录的页面（资产各页、安全、设备、个人资料、通知），都应跳到 `/login?next=<该页>`，登录后回到最后一个（B151：之前只看 `/assets/history`，新加的个人资料页并没有被这一步检查）。
  - 输出 `ok|FAIL 编号 说明 (秒)`，每站一行汇总；失败的步骤把每个标签页的截图与日志（错误、地址、控制台、页面错误、失败请求）写到 `FLOWS_OUT`（默认 `~/.cache/exchange-e2e/flows/<run>`），打印路径后继续下一步；每站的结果另存 `<站点>-summary.json`。`FLOWS_ONLY=3,P4` 只跑这几项（准备步骤 `—` 总会跑）。
  - 后台流程经 ssh 建一个临时 ADMIN 与一个临时 AUDITOR（随机口令与密钥从标准输入传入、不打印），结束时停用；危险操作只打开、填写后取消，最后核对审计里 ADMIN 只有登录与退出。
  - 只为故障场景拦截请求（断网、令牌过期的 401、标记价降级）；服务器上只多出流程自己注册的账户与它们挂了又撤的单，不碰托管方。PC 流程的账户在每个下单步骤结束时撤单，脚本退出前（不论怎么结束）再撤一次，最后一步核对没有剩下的挂单。
  - 前提：P2、P6 要挂 0.0002 BTC 的限价买单，靠注册赠送的 USDT。送多少读公开的平台资料（`welcome_credits`）：不送就跳过这两步并写明原因；送了就等账本入账（最多 40 秒），等不到算失败。
  - 杠杆 P9（PC）与 M7（手机，2026-10-06，E4）：要杠杆交易对流程账户开放（`MARGIN_TRADE` 资格，测试服 `margin.enabled` 已全局打开）且有 25 USDT 以上，否则跳过并写明原因。经接口先划 20 USDT 进全仓，然后在页面上：行情列表 BTC/USDT 旁的倍数徽标；交易页切到全仓看到风险率仪表与可借额度，打开划转、还币弹窗（PC），从面板的借币弹窗（手机是叠在下单抽屉上的借币抽屉）借 1 USDT，账户有了负债与风险率；从全仓挂一笔低 5%、约 15 USDT 的限价买单（数量按当时价格与交易对的 lot 算，`flows-lib.mjs` 的 `budgetBuy`，价格涨了也买得起），委托标「全仓」后撤单；资产页「杠杆账户」显示全仓与逐仓两组，从 USDT 的「还币」点"最大"全部还清（核对借款与利息归零）。结束时（不论成败，包括划入之后任何一步出错）经接口撤单、还清并把 USDT 划回现货，步骤超出时间预算被放下时由退出前的清理再做一次；接口调用每步只登录一次（每次登录都计入同 IP 与新设备风控）。两站的前提一致：25 USDT 以上，不依赖 P2/P6 的 50 USDT。
  - 步骤超出预算（默认 240 秒）时记失败，接着跑下一步：它自己的标签页关掉，共享的标签页换新页并回到原地址（经上下文的 Cookie 保持登录）。每一步在自己的异步上下文里跑，超时的步骤之后再碰标签页、它的方法或 `open()` 都会抛错，不会动到下一步的页面。
  - 判断方法：对比度在页面里按 WCAG 2.x 算，文字色叠在祖先的背景与盖在文字下面的无文字绝对定位层（滑块、深度条）上，渐变或图片上的文字不判，等"同步中"消失、过渡结束再读；减少动效看 `document.getAnimations()`；点击目标看自身尺寸或命中区域（离中心 21 px 处 `elementFromPoint` 仍落在它上面），被固定栏盖住的先滚到屏幕中间；横向溢出找到具体元素，绝对定位的元素按它的包含块判断是否被裁剪（`sr-only` 不受非定位祖先的 `overflow` 裁剪，会撑宽页面）。
  - 本机对开发服务器：`APP=http://localhost:5173 node web/e2e/pc-flows.mjs`（或在 `web/e2e` 里 `pnpm flows:pc`，另有 `flows:m`、`flows:admin`；手机站 5174；后台 5180，另给 `ADMIN_EMAIL`、`ADMIN_PASSWORD`、`AUDITOR_EMAIL`、`AUDITOR_PASSWORD`）；P8（要部署后 nginx 的按设备分流）与 M4（只有生产构建注册 service worker）在本机跳过。
- 人工检查清单：[ui-checklist.md](ui-checklist.md)。
