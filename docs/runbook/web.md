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
    - 层级：`--z-sticky`（30，页面里的粘性表头与吸底栏）< `--z-topbar`（35，两站的顶栏、PC 的断线提示条、手机站的底部 tab 栏）< `--z-sheet` < `--z-dialog` < `--z-dropdown` < `--z-toast`，用法如 `z-[var(--z-topbar)]`。sticky 的顶栏自成层叠上下文，里面用 CSS 打开的菜单只能跟着顶栏的层级走，所以顶栏要压住页面里所有粘性元素（B61：行情表的表头曾盖住「合约」菜单的第一项）；Radix 的弹层渲染到 body，不受影响。
  - 组件与 Storybook 故事，动效预设在 `src/lib/motion.ts`。

应用之间不互相 import；取数、推送、格式化、校验、i18n 与组件都放共享包。

## PC 站页面（B2）

| 区域 | 路径 | 代码 | 共享逻辑（`packages/core/src`） |
|---|---|---|---|
| 首页、行情、币种 | `/`、`/markets`、`/coin/:symbol` | `pages/markets` | `markets/`：行情列表排序筛选、自选（本机与账户同步）、迷你走势图 |
| 交易终端 | `/trade/:symbol`、`/futures/:symbol` | `pages/trade`（全高 `TerminalShell`，无页脚） | `trading/`：交易对、订单与成交、K 线分页、合约（仓位、保证金、止盈止损、资金费）、终端偏好 |
| 资产 | `/assets`、`/assets/{deposit,withdraw,transfer,history}` | `pages/assets` | `assets/`（估值、流水、划转）、`wallet/`（网络、地址格式、提现计算、充提时间线） |
| 杠杆账户 | `/assets/margin` | `pages/assets/Margin.tsx`、`parts/MarginDialog.tsx`（手机站 `parts/MarginSheet.tsx`） | `margin/`：`math.ts`（风险率分区与显示、负债、可还）、`hooks.ts`（条款、账户（`margin` 频道的 ACCOUNT 推送由 `query/private.ts` 的 `applyMarginAccount` 直接写进缓存，余额或负债变了才重拉借款与可借额度；另每 15 秒刷新，给没有负债、只有价格变化时不推送的账户估值）、可借额度、划转/借币/还币调用、入口判定）、`form.ts`（两站共用的表单逻辑：账户、币种、上限、校验、幂等键）；仪表是 `@exchange/ui` 的 `MarginLevel` |
| 账户 | `/account/{security,sessions,settings}`、`/notifications` | `pages/account` | `user/`（资料、会话、安全、通知）、`auth/`（登录流程、密码规则、注册） |
| 认证 | `/login`、`/register`、`/reset` | `pages/auth`（居中卡片 `AuthShell`） | `auth/` |
| 公告、帮助 | `/announcements`、`/help` | `pages/content` | `content/`：Markdown（`packages/core/content/*.md`，中英两份）与安全的渲染器；后台发布的文章（`GET /v1/announcements`、`/v1/help`）叠加在自带文件之上，同 slug 以接口为准，下线的连同自带文件一起隐藏，1 分钟内到达（见 `admin.md`「运营」） |

- 路由：每个区域在 `pages/<区域>/routes.tsx` 登记页面，`App.tsx` 按外壳（`AppShell`、`TerminalShell`、`AuthShell`）挂载；需要登录的页面 `auth: true`，未登录跳 `/login?next=`。
- 文案：外壳的在 `src/i18n.ts`，各区域的在 `src/i18n/<区域>.ts`（命名空间 `pcTrade`、`pcAssets` 等）。`routing.tsx` 的 `lazyPage` 与页面 chunk 并行加载该区域文案并注册，首屏不带全部页面的文字。
- 敏感操作：`features/auth/StepUp.tsx` 的 `useStepUp()`（身份验证器或邮箱/短信验证码换 step-up 令牌），`OtpStep` 是"人机验证 → 发送验证码 → 6 位码"的共用步骤。
- 快捷键：`⌘K`/`Ctrl+K` 全站搜索；终端里 `/` 打开交易对搜索，`B`/`S` 切买卖。
- 杠杆账户（杠杆设计 2026-10-06 §7，批次 E4 的第一部分）：全仓卡片（风险率仪表、总资产/总负债/净资产、各币种的可用/冻结/已借/利息/净资产与借、还、划转）、逐仓列表（每个交易对一张，含强平价估算）、借币利率；三个弹窗共用 core 的 `useMarginForm`：划入的上限是现货可用，划出是账户可用（服务端另按预警线与负债限制），借币是 `max-borrowable`（并写明受哪一界限制），还币是负债与可用的较小者，点"最大"且能还清时发 `ALL`。入口：资产侧栏的"杠杆账户"与手机站资产页的入口行，只对 `MARGIN_TRADE` 资格开放的人、或仍有杠杆资产或负债的人显示（关闭后还能还币、划出）；`margin.enabled` 对用户关闭时页面顶部说明，借币按钮不可点。仪表分区：低于预警线红，预警线以上两倍间距内黄，再往上绿，无负债显示 999。
- 交易终端的杠杆模式（E4 的第二部分）：现货下单面板上方的账户切换（现货 / 全仓 / 逐仓，交易对两种币都能作保证金才有全仓、交易对开了逐仓才有逐仓；`margin.enabled` 对用户关闭时整条不显示），杠杆账户下显示倍数、风险率、逐仓强平价、划转/借币/还币（同资产页的弹窗或 sheet）与借还方式（普通、自动借款、自动还款，下单带 `account` 与 `side_effect`）；可用余额换成该杠杆账户的可用，自动借款时加上可借（`max-borrowable` 每 10 秒刷新），"充值"换成"划转"。账户与借还方式之间还有一行"可借"：下单这一边要花的币（买入是计价币、卖出是基础币）现在最多能借多少；面板上的划转、借币、还币与余额不足提示里的"划转"都默认这个币。账户选择记在终端偏好（`exchange.terminal` 的 `tradeAccount`），交易对不支持或功能关闭时退回现货；借还方式只在本次访问里有效（不写进偏好，免得哪天忘了自动借款还开着）。`margin.auto_borrow` 关闭时下单返回 `MARGIN_DISABLED` 带 `flag`，提示"自动借款暂未开放，请把借还方式改为普通"并把借还方式改回普通；不带 `flag` 的 `MARGIN_DISABLED` 让资格重新查询，终端退回现货。杠杆的对话框与 sheet 在第一次打开时才加载（不进交易页首屏）。委托列表给杠杆单标"全仓"/"逐仓"；`orders` 推送的受理消息带 `account`、`side_effect`，推送新建的委托也能标上。共享逻辑在 core 的 `margin/trade.ts`。

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
- **平台资料**（设计 2026-10-04 §4.1，`@exchange/core/platform/index`）：两个用户站在根组件调 `useBrandingEffects()`，启动时与之后每分钟读 `GET /v1/platform/profile`，改名、换图不需要重新构建。
  - 读到之后：页面标题、favicon 与 apple-touch-icon、`theme-color`、CSS 的 `--brand`（`--brand-soft` 跟着变，`--brand-fg` 按亮度取深色或白色）都改成资料里的；所有文案里的 `{{brand}}` 换成资料的名称（i18next 的 `defaultVariables`）。
  - 顶栏标志用资料的深色背景标志（没有就用浅色的，再没有用自带的）。页脚的版权、合规文案、联系方式与社交链接也取自资料。
  - 测试模式（`test_mode`，2026-10-04 由学习模式改名）开着时：资料的 `banner` 为真则三个外壳顶部显示横幅（资料文案，空着时显示「测试模式」）；PC 首页与手机站欢迎卡、「我的」身份卡显示「测试模式」徽标；内容页底部显示模拟资金提示；内容只显示测试模式的那一套（见下面「内容按模式」）。关掉就都不显示，内容换成正式的那一套。资料若还是改名前的缓存（只有 `learning_mode`），`normalizeProfile` 按它补出测试模式。
  - 注册方式为 `CLOSED` 时，注册页显示资料里的关闭提示，不显示表单。
  - 「注册即送」文案用资料的 `welcome_credits`（`useWelcomeCredits()`，如「10,000 USDT、0.1 BTC」），清单为空时改用不提赠送的文案。
  - 接口读不到时用 `DEFAULT_PROFILE`：自带的名称、图标与颜色，不显示横幅、不承诺赠送、注册开放。页面不会白屏。
- **法律页与首页横幅**（设计 §4.4）：`/legal/:slug`（terms、privacy、risk、fees、about、contact），PC 站页脚与手机站「我的 → 条款与政策」进入。默认稿在 `web/packages/core/content/legal/`，首页横幅的默认稿是 `content/home/home-hero.*.md`。后台发布的覆盖稿优先，撤回的不显示。
  - 首页横幅：标题、副标题（文章摘要），正文里第一个 Markdown 链接是按钮（`useHero()`）。手机站的欢迎卡在有赠送时仍以赠送为标题。
- **内容按模式**（设计 2026-10-04 §4.4）：站点按资料的测试模式只显示那一种模式的内容，切换模式即自动换稿。
  - 整篇：内置稿的 front matter `modes: TEST | FORMAL | BOTH`（默认 BOTH），后台文章的同名字段由 notification-service 的公开接口按模式过滤（它每 10 秒读一次资料）。打包的「测试环境」公告是 TEST。站点每分钟读一次资料、内容接口缓存 15 秒，切换后约 1 分钟内全部一致；资料读到之前（或读失败之前）内容查询不发，免得测试站先按正式模式取一遍再换；资料请求失败时按全局设置重试一次（约 1 秒后），再失败才按内置的正式模式取内容，所以资料接口出故障时内容晚出现这一次重试的时间。
  - 段落：正文里 `:::test` … `:::` 与 `:::formal` … `:::` 之间的行只在那种模式下保留（只认这两个标签、不嵌套、未闭合算到文末，其它 `:::xxx` 与代码块里的原样保留）。过滤只在 core 的 `renderByMode(md, mode)`（`@exchange/core/content/markdown`）一处，内置稿、后台文章与后台编辑器预览共用；接口照原文返回正文。没写摘要的后台文章，列表里的摘要是该模式下正文的第一段（notification-service 的 `domain.InMode` 按同样的规则取段，再取 `Excerpt`）。
  - 写稿约定：只在测试环境成立的话（Sepolia、模拟短信、1 分钟冷却、「测试环境」字样）放进 `:::test`，需要正式说法的写 `:::formal`；充提与费率的正式说法指向充值、提现页与费率说明，不点名网络；两种模式都不写死注册赠送的数额。模拟资金的提示由内容页底部的提示统一给出，稿子里不再各写一句。模式块里再出现的 `:::test`、`:::formal` 行按普通文字显示；模式块里单独一行 `:::` 就结束这个模式块，所以块里不要再套其它 `:::` 容器（它的结束行会提前结束模式块）。core 的单测断言正式模式下的稿子不含这些字样，演练（`launch-drill.sh`）在两种模式下检查帮助中心的渲染结果。
  - 查询键带上模式（`contentKeys.list(section, locale, mode)`），切换模式是一次新的查询。

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
  - `node web/e2e/perf.mjs memory`：PC 终端页 30 分钟（`MINUTES`）的 JS 堆增长（前后各强制一次 GC）。
  未达预算时退出码非 0；`BUDGET=warn` 只报告。结果记在 `docs/阶段4验收报告.md`。
- 检查清单流程（2026-10-04）：`scripts/e2e/webflows.sh [pc] [m] [admin]`（`task e2e` 里排在 `web.sh` 之后；单独运行先持运维锁：`scripts/ops/lock.sh run -- bash scripts/e2e/webflows.sh pc m admin`）把 [ui-checklist.md](ui-checklist.md) 能稳定判断的项在 headless Chrome 里逐项走一遍，每站约 4–6 分钟（网络慢时到 12 分钟）。逐项对应与结果见 `docs/阶段4验收报告.md` §8.1。
  - 脚本：`web/e2e/pc-flows.mjs`、`m-flows.mjs`、`admin-flows.mjs`，公共部分 `flows-lib.mjs`（建在 `lib.mjs` 的 Chrome 之上）。每一步以清单编号命名（`3`、`P4`、`M1`、`A2`），在自己的标签页（独立的浏览器上下文，各自的设备、时区、减少动效等媒体特性）里跑；只等事件与状态，不靠定时等待。
  - 输出 `ok|FAIL 编号 说明 (秒)`，每站一行汇总；失败的步骤把每个标签页的截图与日志（错误、地址、控制台、页面错误、失败请求）写到 `FLOWS_OUT`（默认 `~/.cache/exchange-e2e/flows/<run>`），打印路径后继续下一步；每站的结果另存 `<站点>-summary.json`。`FLOWS_ONLY=3,P4` 只跑这几项（准备步骤 `—` 总会跑）。
  - 后台流程经 ssh 建一个临时 ADMIN 与一个临时 AUDITOR（随机口令与密钥从标准输入传入、不打印），结束时停用；危险操作只打开、填写后取消，最后核对审计里 ADMIN 只有登录与退出。
  - 只为故障场景拦截请求（断网、令牌过期的 401、标记价降级）；服务器上只多出流程自己注册的账户与它们挂了又撤的单，不碰托管方。PC 流程的账户在每个下单步骤结束时撤单，脚本退出前（不论怎么结束）再撤一次，最后一步核对没有剩下的挂单。
  - 前提：P2、P6 要挂 0.0002 BTC 的限价买单，靠注册赠送的 USDT。送多少读公开的平台资料（`welcome_credits`）：不送就跳过这两步并写明原因；送了就等账本入账（最多 40 秒），等不到算失败。
  - 步骤超出预算（默认 240 秒）时记失败，接着跑下一步：它自己的标签页关掉，共享的标签页换新页并回到原地址（经上下文的 Cookie 保持登录）。每一步在自己的异步上下文里跑，超时的步骤之后再碰标签页、它的方法或 `open()` 都会抛错，不会动到下一步的页面。
  - 判断方法：对比度在页面里按 WCAG 2.x 算，文字色叠在祖先的背景与盖在文字下面的无文字绝对定位层（滑块、深度条）上，渐变或图片上的文字不判，等"同步中"消失、过渡结束再读；减少动效看 `document.getAnimations()`；点击目标看自身尺寸或命中区域（离中心 21 px 处 `elementFromPoint` 仍落在它上面），被固定栏盖住的先滚到屏幕中间；横向溢出找到具体元素，绝对定位的元素按它的包含块判断是否被裁剪（`sr-only` 不受非定位祖先的 `overflow` 裁剪，会撑宽页面）。
  - 本机对开发服务器：`APP=http://localhost:5173 node web/e2e/pc-flows.mjs`（或在 `web/e2e` 里 `pnpm flows:pc`，另有 `flows:m`、`flows:admin`；手机站 5174；后台 5180，另给 `ADMIN_EMAIL`、`ADMIN_PASSWORD`、`AUDITOR_EMAIL`、`AUDITOR_PASSWORD`）；P8（要部署后 nginx 的按设备分流）与 M4（只有生产构建注册 service worker）在本机跳过。
- 人工检查清单：[ui-checklist.md](ui-checklist.md)。
