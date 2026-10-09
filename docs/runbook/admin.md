# 管理后台（admin-service 与 web/apps/admin）

实施计划 §6.3 任务 11，需求 §5.12（RBAC、双人审批、操作审计、提现审批、交易对管理、功能开关、用户处置）、§5.14；2026-10-02 起按 [设计-管理后台重构](../设计-管理后台重构-2026-10-02.md) 重做（批次 C1–C6）。本文按后台侧栏的分组组织（C6 重写）：先是组成、登录、角色与各页通用的规则，再按「概览 · 用户 · 资金 · 交易 · 市场 · 模拟市场 · 风控 · 运营 · 系统」逐页说明内容、接口、权限、审计与规则，最后是运维、测试与错误码。各条后面括号里的批次号（C1、C5.5 ⑥ 等）指引入它的批次，验收记录在设计稿 §10。

## 组成

```
浏览器 https://admin.astras.vip/ ──nginx──> 静态文件（web/apps/admin 构建产物，/opt/exchange/infra/nginx/sites/admin）
                     /admin/v1/* ──nginx──> admin-service:8093（不经用户网关）
https://astras.vip/admin/* ──301──> https://admin.astras.vip/*（旧后台的地址，阶段 4 B5 起）
admin-service ──gRPC──> auth-service（按邮箱/手机号找用户、账户安全）、user-service（账户与状态）、
                        ledger-service（余额、资金操作、风控冻结、系统科目与对账）、
                        instrument-service（资产、交易对与合约的配置与状态）、risk-service（风控评估）
              ──内部 REST──> wallet-service（提现、充值处置、托管方）、spot-trading-service（撤单）、
                             derivatives-service（合约、仓位、强平）、notification-service（文章与站内信）、
                             market-sim（模拟市场）、market-data-service（行情源状态）、
                             instrument-service（平台资料与图片，INSTRUMENT_SERVICE_URL）、
                             ledger-service（注册赠送，LEDGER_SERVICE_URL）
              ──config schema──> 功能开关（与 exchangectl flags 同一张表，变更与审计事件同一事务）
              ──ClickHouse──> 审计查询、订单/成交/充值读模型、报表
              ──admin schema──> 管理员、会话、资金操作、待生效修改、设置、幂等键；自己的 outbox 发 audit.events
```

- 服务：admin-service，HTTP 8093（nginx 转发 `/admin/v1/`），运维 9094，schema `admin`（`admins`、`admin_sessions`、`approvals`、`settings`、`instrument_changes`、平台表 `idempotency_keys`）。契约 `api/admin/admin.yaml`（不进公开 API 文档；改完 `task web:types`）。
- 前端：`https://admin.astras.vip`（`web/apps/admin`，浅色主题）。整站包含 `snippets/admin-access*.conf`，可以挂访问限制，见 [web.md](web.md)；用户 2026-09-30 决定暂不做访问限制，服务器上没有这个文件。阶段 2 的旧后台 `web/admin`（`https://astras.vip/admin/`）已删除，旧地址 301 到新后台的同一路径。本机 `task web:dev -- admin`（http://localhost:5180），`/admin/v1` 代理到测试服。
- 与需求的差异：需求要求独立域名与网关、仅办公网/VPN 访问。学习项目先用同域名的 `/admin/` 路径 + 独立服务（不经用户网关）+ 强制 TOTP；会话 Cookie 限定 `Path=/admin/`，与用户站的 Cookie 互不可见。阶段 4 起后台有了独立域名 `admin.astras.vip`；访问限制与 TOTP 目前按用户决定暂缓（见下文）。
- 请求体大小：后台 API 一般限 64 KB（nginx），文章与资产资料的接口放宽到 512 KB（长正文、base64 图标）。

## 登录与会话

- 没有注册入口：管理员由 ADMIN 在「系统 → 管理员与角色」新建（见下文「管理员与角色」）；没有 ADMIN 能登录时，运维用 `exchangectl admin create` 在 admin-service 容器里创建（它有 `ADMIN_SECRET_KEY`，见「运维」）。
- 登录 = 邮箱 + 密码（Argon2id，至少 12 位）+ 身份验证器 6 位码（RFC 6238，前后一步误差，每个时间步只能用一次）。连续 5 次失败锁定 15 分钟；同一 IP 每分钟最多 10 次登录请求。未知邮箱与已知邮箱耗时相同。
- **暂不校验验证码**（用户 2026-09-30 决定）：开关 `admin.login_without_totp` 打开时只凭邮箱与密码登录。
  - 验证码不要求也不校验；登录页通过 `GET /admin/v1/login-options`（`totp_required`）得知后隐藏验证码输入框（选项读到之前登录按钮等待，读不到时显示输入框）。
  - 登录审计的 `details` 带 `"totp_checked":false`，锁定与限流不变。
  - 测试服已打开这个开关。恢复要求验证码：`exchangectl flags set admin.login_without_totp --off --reason "..."`，5 秒内生效，不用重新部署；已绑定的身份验证器不受影响。
- 会话：随机令牌只存 SHA-256；Cookie `admin_session`，HttpOnly、Secure、SameSite=Strict、Path=/admin/；8 小时到期，1 小时无请求失效；退出或停用管理员时服务端撤销。
- 没有会话时打开控制台的任何地址都转到 `/login?next=<原地址>`，登录后回到原地址（只认控制台自己的路径，其他的回概览；人工检查清单 1）。
- CSRF：除 GET 外每个请求必须带 `X-Admin-CSRF: 1`（跨站表单无法设置自定义头；Cookie 又是 SameSite=Strict）。
- TOTP 密钥用 `ADMIN_SECRET_KEY`（AES-256-GCM，附加数据为管理员 ID）加密存放；换掉这个密钥会让所有管理员的 TOTP 失效，只能重建账号。
- **一次性设置链接**（C5.5 ⑪）：新建管理员与重置口令或身份验证器都不把口令或密钥交给操作的 ADMIN，只给一条设置链接（`https://admin.astras.vip/setup#token=…`，24 小时内有效、只能用一次）。
  - 链接里的令牌只在那一次响应里出现（`Cache-Control: no-store`），不写日志、不进审计，服务端只存它的 SHA-256；放在地址的 `#` 之后，不进任何服务器日志，页面读到后立即从地址栏去掉。
  - 本人打开 `/setup` 页（不用登录）：新建的链接设口令（至少 12 位，输两次）并绑定页面显示的身份验证器（二维码与密钥，输入当前 6 位码证明已绑好）；重置口令的只设口令（身份验证器不变）；重置身份验证器的只绑定（口令不变）。完成后链接作废，用新的口令与身份验证器登录。
  - 审计 `admin.setup_completed`（以本人名义，详情为种类与来源 IP）；新建与重置的审计详情有链接的种类与到期时间。
  - 链接没用就又重置时，新链接包含旧链接要设的部分（例如口令链接未用又重置身份验证器，新链接两样都设），不会留下谁都不知道的口令或密钥。服务端只存 Argon2id 哈希与用 `ADMIN_SECRET_KEY` 加密的 TOTP 密钥（等待绑定的另用一个附加数据加密）。
  - 接口（不用会话，与登录共用每个 IP 每分钟 10 次的限制）：`POST /admin/v1/setup/inspect`（`{token}` → 账号、种类、到期时间与要绑定的密钥）、`POST /admin/v1/setup`（`{token, password?, totp_code?}`，204）。
  - 威胁行（协调会话代用户决定）：取"可追溯而非阻止"——设置链接加审计；"决策人的口令或验证器来自请求人签发的链接且未自行更换时不得作第二人"记为后续行 ⑪b，等真有多名管理员时再做。
- **自己的口令与身份验证器**（右上角菜单「账号与安全」，`/account`，C5.5 ⑪）：
  - 改口令要当前口令，新口令至少 12 位；换身份验证器要当前口令，登录要验证码时（`admin.login_without_totp` 关闭）还要当前身份验证器的 6 位码（测试服开着这个开关，只凭口令就能换绑，C5.5 ㉒），页面给出新的二维码与密钥，10 分钟内输入新码完成绑定，之前旧的仍可登录。
  - 两种修改都结束自己在其它设备上的会话，审计 `admin.password_changed`、`admin.totp_changed`。每位管理员 15 分钟最多 10 次（`COMMON_RATE_LIMITED`）。
  - `exchangectl admin create` 交互生成的口令（不带 `--secrets-stdin`）标记为必须修改：登录后只能看自己（`GET /admin/v1/me`，带 `must_change_password`）、改口令与退出，其它请求都是 403 `ADMIN_PASSWORD_CHANGE_REQUIRED`，后台只显示改口令的页面。
  - 接口：`POST /admin/v1/me/password`（`{current_password, new_password}`）、`POST /admin/v1/me/totp/start`（`{current_password, totp_code?}` → `{totp_secret, totp_uri}`）、`POST /admin/v1/me/totp`（`{totp_code}`）。

## 角色与权限

| 角色 | 权限 |
|---|---|
| ADMIN | 全部（28 项），包括只有它有的 `settings.write`（后台设置：双人审批与单人限额）、`admins.manage`（管理员：创建、改角色、停用与启用、重置口令与身份验证器（都只给一次性设置链接）、结束会话）、`instruments.trading`（交易参数：状态、费率、风险阶梯、参考符号，见「交易参数的护栏」）与 `withdrawals.resume`（解除资产的提现暂停，C5.5 ⑯） |
| OPERATOR | 读 + 改账户状态、撤销用户挂单（全部或单笔）、编辑交易参数以外的参考数据与上架新交易对（`instruments.write`）、解除合约只减仓与强制平仓（`derivatives.write`）、切换功能开关（后台自己的 `admin.*` 开关除外）、备注与标签（`users.notes`）、账户安全操作与换绑审核（`users.security`）、查看完整联系方式（`users.contacts`）、风控冻结（`ledger.hold`）、公告与帮助（`content.write`）、站内信（`notices.send`）、模拟市场的价格事件与参数（`sim.control`，C5） |
| FINANCE | 读 + 提现审批与搁置、发起与审批资金操作（手动调账（现货或合约账户）、保险基金注资、增发、托管方手续费的入账与核销）、充值处置、补记与无主充值记给用户（`deposits.review`）、备注与标签、查看完整联系方式、风控冻结 |
| AUDITOR | 只读（用户（联系方式脱敏）、资产与交易对、合约（`derivatives.read`）、功能开关、提现、审计日志、报表），外加导出审计日志（`audit.export`：CSV 里有邮箱与 IP，只有 ADMIN 与 AUDITOR 能导出，C5.5 ⑪） |

- 越权返回 403 `ADMIN_FORBIDDEN`。前端按 `/admin/v1/me` 返回的权限列表显示菜单与按钮，但以服务端检查为准。`GET /admin/v1/roles`（任何管理员可读）给出角色与权限的矩阵。
- `admin.*` 开关（`admin.login_without_totp`、`admin.two_person_approval`）在开关页也要 `settings.write`，运营不能借开关页关掉双人审批。

## 各页通用的规则

- **外壳**（设计 §3、§6，C1 起）：浅色为主，顶栏菜单与「设置 → 外观」可切深色（存在本机 `admin.theme`；深色复用用户站令牌）。侧栏是 `data-theme="dark"` 的深色岛，按「概览 · 用户 · 资金 · 交易 · 市场 · 模拟市场 · 风控 · 运营 · 系统」分组折叠（记在本机），没有权限的项隐藏，可收成图标栏。
  - 顶栏：环境标识、审批方式（单人/双人，点开设置）、全局搜索（⌘K / Ctrl+K；用户 ID、邮箱、手机号打开用户页，订单号进入订单列表，交易哈希进入充值列表）、待办铃铛、主题、账户菜单（邮箱、角色、账号与安全、主题、退出）。
  - 动效：登录页左半屏网格与两团漂移光斑、字标描边绘制，表单淡入上移、输入框聚焦底线从中间展开、登录中进度条、成功打勾；验证码 6 格输入（粘贴自动填满）。进入控制台侧栏滑入、卡片间隔 40 ms 淡入；切换页面内容区淡入上移 160 ms；概览数字滚动、趋势图从左向右描画 600 ms、异常服务的状态点呼吸；提示条从右下滑入、成功打勾（设计 §6 写的是右上，放右上会盖住抽屉的按钮，C4b 改到右下，见设计稿 §0）。只动 transform/opacity（字标与对勾的描边除外），`prefers-reduced-motion` 时全部关闭：CSS 动画与 motion 的位移随系统设置实时变化；motion 的淡入与错开入场（`skipAnimations`）在登录后外壳打开时读一次系统设置（`layout/SignedIn.tsx`，A43 ⑧ 起不在入口），开着控制台改系统设置要刷新页面才对它们生效。
- **列表**（设计 §10.2，阶段 4 B1 起）：所有列表接口统一用不透明游标分页，返回 `{items, next_cursor}`；`next_cursor` 原样作为下一页的 `cursor` 传回，最后一页为 null；`limit` 为 1–200，默认 50（审计日志与强平记录最多 500，默认 100）。页面每页条数可在「设置」里按本浏览器改（20/50/100/200，存在本机 `admin.page_size`），滚到底自动加载下一页；筛选条件写在地址栏（可分享、后退可恢复），可另存为本机"视图"；文本筛选在停止输入 300 毫秒或回车后生效（A93）。「重置」只清筛选栏里的条件（用户页连同搜索框的关键字 `q`），不切换页签——充值的 `view`、杠杆账户的 `status`、利息报表的 `bucket`、仓位的 `view` 等保持不变；保存的视图同样只记筛选栏的条件（A95：A93 起如此，有意保留，页签是导航而不是筛选）。
- **确认框**：危险操作统一用确认框——显示对象、理由至少 10 个字（进审计）、手动输入确认词（ID 后 4 位、交易对代码、资产代码或金额），结果用提示条告知，失败时附可复制的追踪 ID。确认词只在页面上核对（服务端靠权限、幂等键、限额与双人升级把关，㉔ 评审接受）；对话框里要填的东西不完整时（例如用户 ID 不是 UUID、金额不是正数）确认按钮不可用（㉕）。
- **显示**：枚举都有中文标签，悬停显示原始代码；金额按十进制字符串原样显示并加千分位；时间按设置里的时区。
- **没有权限时**（人工检查清单 A3）：操作按钮一般直接隐藏（没有权限的分区不出现在侧栏，地址也打不开）；控件留在页面上但不可用的页（功能开关、设置、平台设置、价格控制的参数、机器人集群的开关、固定页面与文章编辑器）顶部有一行「只读」说明，写明当前角色与修改需要的权限（如 `flags.write`）。资产资料（含代币信息页）的「编辑」要 `instruments.write`。
- **幂等键**（C5.5 ⑥）：所有动钱的请求必须带请求头 `Idempotency-Key`（≤ 128 字，缺了 400）：调账两条路由、保险基金注资、补记、无主充值记给用户、增发、冻结与解冻、强制平仓、提现审核与批量审核、待处理充值入账、站内信。
  - 后台对话框每次打开生成一个键，结果未知（网络断、5xx、键冲突）时重试沿用，结果确定或关闭对话框后换新键。
  - 键归各管理员所有，存在 admin 库平台表 `idempotency_keys`（`scope` 为「管理员 ID + 动作」，`response` 是请求生成的 ID），保留 24 小时，admin-service 每小时清理一次。
  - 第一次请求按键生成 ID：资金操作、冻结 ID、平仓单的 `client_order_id`、站内信 ID 都取自它。同一请求再来，返回同一笔（资金操作原样返回，单人模式下结果未知的会被完成；冻结、平仓单、站内信由下游按 ID 去重；提现审核、解冻、入账已由本人做过的返回当前状态）；同一键换了内容返回 409 `COMMON_IDEMPOTENCY_CONFLICT`。
  - 站内信的审计 `admin.notices.sent` 与键在同一事务里先写，再交给 notification-service（它接受调用方给的 ID，同一 ID 再发返回原消息）。
  - 除资金操作外，键在执行之前单独占用；重放返回的是对象**现在**的样子（例如站内信已是 `SENT`），不是第一次请求时的响应（C5.5 ⑮，可接受）。
  - 不带幂等键的写操作靠状态防重：托管方手续费的入账与核销（处理过的再处理得到 409）、提现暂停的解除（已解除得到 404）。
- **待办与实时推送**（C1）：
  - `GET /admin/v1/todo`：`withdrawals` 待审核提现（最多数到 200）、`approvals` 待处理的资金操作、`identity_requests` 待审核的换绑申请（有 `users.security` 才数，最多数到 200，C2）、`deposits` 等人处理的充值（有 `deposits.review` 才数，最多数到 200，C2c）与 `instrument_changes` 待生效的交易参数修改（C3c），按角色返回（没有权限的为 0），读不到的记在 `partial`。
  - `GET /admin/v1/events`：Server-Sent Events。连上即发一次 `todo`，之后每 10 秒检查、变了才发；20 秒没有事件发一行注释保活（nginx 读超时 60 秒、Cloudflare 100 秒）；会话结束（退出、过期、停用）时发 `signed_out` 并关闭。流本身不算请求，不会让会话保持活跃。响应头 `X-Accel-Buffering: no` 让 nginx 不缓冲；服务端对这条连接取消读写超时。
  - 前端只开一条流，写进 Query 缓存；流断开时每 15 秒轮询 `/todo`。侧栏角标与顶栏铃铛来自它，数字增加时弹跳；铃铛与概览的各项进入对应列表。
- **读模型的时效**：订单、成交、充值、审计、报表与概览的交易部分来自 ClickHouse，比服务晚几秒。
- **首屏**（C6，Lighthouse）：后台的 CSP（`default-src 'self'`）不许内联脚本，所以不能像用户站那样由 index.html 按地址预加载页面块（`web/scripts/route-preload.mjs`）。改为入口模块一开始就调用 `preloadConsole`（`src/preload.ts`），在问 `/admin/v1/me` 的同时开始下载登录后外壳与当前页面的块（页面表在 `src/pageLoaders.tsx`）。index.html 的 `#root` 里有一个静态启动画面（只用主题令牌类），入口加载完之前就能画出。不要往 index.html 加内联脚本：浏览器会拒绝执行，冒烟测试会因控制台的 CSP 错误失败。回滚时不要停在 `8a796c8`（它试过内联预加载，被 CSP 挡住）；`167407d` 起是入口模块预加载（复审 ㉘）。
- **分块与预取**（A40，2026-10-05；用户反馈点子页面时内容区白屏十几秒）：
  - 分块（`vite.config.ts` 的 `codeSplitting.groups`，A43 ⑧ 起三组）：`vendor` 是初始块里的第三方代码（`node_modules` 与虚拟模块，部署之间内容不变、可长期缓存），`index` 是初始块里本应用的代码（登录页、`App.tsx` 静态导入的部分），这两组都**不递归带入依赖**（`includeDependenciesRecursively: false`，否则 motion 等会被拖进入口）；`kit` 是两个及以上块共用的代码，随登录后外壳一起加载；每个页面只剩自己一个文件。`<MotionConfig>` 在登录后外壳（`layout/SignedIn.tsx`）里，登录与设置页只用 CSS 动画；各页文案在 `src/pageMessages.ts`，由外壳的块加载时注册进 i18next，入口只带外壳、登录、账号与通用文案（`src/i18n.ts`）。此前打包器按"哪几页共用"切出 160 个小文件，第一次打开一页要再取 8–31 个，登录后外壳 33 个；A40 首版合成 76 个文件，但入口 233 KB gzip。现在 78 个 JS 文件：登录页 3 个（148.3 KB gzip：index 34.5、vendor 113.4、运行时 0.5，各项四舍五入；gzip -6，1 KB 按 1000 字节），外壳加 2 个（250 KB：kit 188、SignedIn 62），任一页面第一次打开加 1 个（0.2–6 KB）。文件数没达到设计要的 ≤ 60，偏差记在后台设计稿 §10。
  - 页面（`src/pageLoaders.tsx`）：每节的页面是可预载的组件——块的 promise 留着，到了就用 `use()` 同步渲染，`.preload()` 提前取。`React.lazy` 只在第一次渲染时解析自己的 import，提前取过的页面仍会闪一帧骨架并重放入场（A43 ⑨）。`sections.tsx` 与 `preload.ts` 都从这张表取页面。
  - 预取（`preload.ts`）：外壳出现后，侧栏把有权限的各节页面块在浏览器空闲时逐个加载（`requestIdleCallback`，浏览器要求省流量时不取）；标签页在后台时暂停，管理员点开的页面还在加载时让它先走；侧栏卸载（退出登录、开发模式 StrictMode 的第二遍）时停止。指针移到或焦点落到侧栏链接上时立即取该页。
  - 等待时不白屏：页面块未到时内容区显示页面骨架（`kit/PageSkeleton.tsx`，`data-testid="page-skeleton"`）；登录后外壳的块未到时显示与问会话时相同的加载圈。
  - 冒烟（`admin-smoke.mjs` 第 2b 步）：从侧栏第一次打开审计页时扣住它的块，确认骨架出现；放行后确认页面出现，且该块静态导入的文件都已随外壳加载（即第一次打开只取这一个文件）。第 2c 步：等浏览器空闲时按侧栏顺序把各节页面都取到（最后一节「设置」，30 秒内；实测 ADMIN 登录后约 16 秒），再打开没打开过的报表、系统健康、平台设置，不再请求任何 JS 文件。
  - 新页面：在 `pageLoaders.tsx` 的 `pages` 表与 `sections.tsx` 各加一行，文案放进 `pageMessages.ts` 合并的文件；分块不用改。

## 页面一览

| 分组 | 页面 | 内容 |
|---|---|---|
| 概览 | 概览 `/` | 待办（待审提现、待处理资金操作、身份变更申请、待处理充值、待生效修改）、24 小时指标（数字滚动，可点进对应列表）、近 7/30 天成交与新增用户图、18 个服务的就绪状态与耗时、HOUSE 库存估值与盈亏、托管方状态（可访问、短缺、待处理回调、处理中的提现） |
| 用户 | 用户 `/users`、用户页 `/users/<id>` | 按 ID/邮箱/手机号/用户名查找，查不到就按关键字筛列表（A93），按状态、地区、注册时间筛选，列表带标签；用户页见「用户页」 |
| 用户 | 身份变更申请 `/identity-requests` | 待审核的换绑（值脱敏），通过或拒绝 |
| 资金 | 充值 `/deposits` | 三个视图：全部（按用户、资产、网络、状态、交易哈希筛选）、待处理（入账给用户、驳回、无主充值记给用户）、补记待回调；「补记充值」抽屉 |
| 资金 | 提现审核 `/withdrawals` | 默认待审批队列（旧到新），可切换状态，按折合金额区间、风控分、是否搁置、网络筛选；详情抽屉（进度、地址簿记录、今日与本月已提、风控、审批人、托管方回调），批准/拒绝/搁置，批量审核；暂停的资产有横幅；有新的待审批提现时出现"有新数据"条，不整表轮询 |
| 资金 | 托管方 `/custody` | 优盾与替身两个托管方（页头切换）：状态与处理中的提现、币种与余额、对账、回调日志（筛选、原始请求、来源地址、重放）、托管方手续费（入账、核销） |
| 资金 | 资金调整 `/adjustments` | 审批方式与 24 小时已用额、查找用户、方向/账户/资产/数量/关联单号；单人模式下立即记账并显示分录号与两条分录，或显示待审原因；最近的资金操作 |
| 资金 | 审批 `/approvals` | 资金操作（默认待处理），方式与转审原因、申请人与审批人；批准、拒绝、完成（待核对的）、撤回（自己的） |
| 资金 | 对账与系统科目 `/ledger` | 每项对账检查的最近一次结果与最近的不一致；系统科目余额 |
| 交易 | 订单与成交 `/orders` | 两个标签；按用户、订单号、交易对、状态、方向、账户（用户或机器人）、时间筛选；HOUSE 一方显示为 HOUSE，机器人标出；导出已加载的行为 CSV |
| 交易 | 仓位 `/positions` | 全部用户的合约持仓（风险最高的在前，HOUSE 在最后），风险仓位筛选，强制平仓 |
| 交易 | 强平记录 `/liquidations` | 强平引擎的每一步，按环节、合约、用户、近 1/7/30/90 天筛选 |
| 交易 | 合约与保险基金 `/derivatives` | 合约状态、只减仓与解除、标记价、持仓量；保险基金与注资 |
| 交易 | HOUSE 敞口 `/house` | 合计、近 30 日盈亏、敞口、库存、各交易对、各合约净头寸（C6 重做） |
| 市场 | 资产与交易对 `/instruments` | 交易对、资产（含网络与资料）、合约（含风险阶梯）、费率档、上架向导、待生效修改六个标签，可搜索；先预览再生效 |
| 模拟市场 | 概览、价格控制、事件日程、机器人集群、代币信息 `/sim/*` | 见「模拟市场」 |
| 杠杆 | 杠杆账户 `/margin/accounts`、杠杆强平 `/margin/liquidations`、利息报表 `/margin/interest`、杠杆参数 `/margin/params` | 见「杠杆」；开关 `margin.enabled` 关着时不显示 |
| 风控 | 功能开关 `/risk` | 全部功能开关（说明、规则摘要、最后修改人），切换要确认；说明在中文界面显示后台自带的中文（`messages/flags.ts` 按开关键，新开关没翻译时显示后端 `flags.Known` 的英文原文），新增开关时在那里补一条 |
| 运营 | 公告 `/announcements`、帮助中心 `/help-articles` | 文章列表、中英文编辑器与预览、发布（立即或定时）与下线；站点自带文章可复制来编辑 |
| 运营 | 固定页面 `/pages` | 六个法律页与首页横幅：测试模式与正式模式下站点各显示覆盖稿还是默认稿，编辑、以默认稿发布、撤回 |
| 运营 | 站内信 `/broadcasts` | 发过的消息（对象、已收到、已读、失败轮次）、详情、继续发送与发送表单 |
| 系统 | 管理员与角色 `/admins` | 管理员列表与操作、角色权限矩阵 |
| 系统 | 审计 `/audit` | 按操作人、对象、事件、时间筛选；行详情与逐字段的变更；服务端导出 CSV |
| 系统 | 报表 `/reports` | 交易、充提、合约、用户增长、HOUSE 盈亏、持仓量 |
| 系统 | 系统健康 `/health` | 各服务就绪、版本、Kafka 滞后与死信，对账、行情源、托管方 |
| 系统 | 平台设置 `/platform` | 平台资料（品牌、域名、颜色、页脚、联系方式、测试模式与横幅、注册方式）、图片上传、注册赠送（提高需第二人） |
| 系统 | App 下载 `/platform/apps` | Android 与 iOS 的下载方式（关闭、外部链接、上传的安装包）、在两站显示、版本说明，分片上传与校验、保留的文件与删除、iOS 配置描述文件（见「App 下载」） |
| 系统 | 上线检查清单 `/launch` | 测试环境的开关与资料逐项对照上线要求，全部达标显示「可上线」；只读 |
| 系统 | 设置 `/settings` | 双人审批与限额、交易参数的等待时间（ADMIN 可改）；本浏览器的外观、语言与每页条数 |
| 账户菜单 | 账号与安全 `/account` | 改自己的口令与身份验证器 |

## 概览

- `GET /admin/v1/dashboard?days=7`（`reports.read`）：
  - 账户总数与 24 小时新增；
  - 24 小时成交笔数、活跃交易用户、按报价资产的成交额；
  - 待确认充值与待审核提现；
  - 24 小时风控事件；
  - 行情连接状态与因断流暂停的交易对（market-data 的 `/internal/market/feed`）；
  - 按日的新增用户、成交笔数与 USDT 成交额。

  哪一部分读不到就留空，并记在 `partial` 里。
- 页面另读待办（`/todo`）、服务健康（`/health`）、HOUSE（`/house`）与托管方（`/custody`）；「服务状态」卡片有「详情」进入「系统健康」。

## 用户

### 用户列表与查找

- `GET /admin/v1/users`：账户，新到旧，可按状态、地区、注册时间与关键字 `q` 过滤，数据来自 user-service 的 `ListUsers`；列表带标签。关键字（A93）：用户名、邮箱或手机号里含有它的账户（不分大小写；邮箱与手机号由 auth-service `SearchUsers` 找出、最多 500 个），最多 254 个字符、不能含控制字符，否则 400。
- 页面的搜索框（A93）：回车时先按用户 ID、邮箱、手机号或用户名精确查找（`lookup`），找到就打开该用户；查不到（404）就把输入当关键字筛选下方列表（地址栏 `q`，「重置」一并清掉，保存的视图也带上它），框下显示当前关键字；只有超长或含不可见字符才提示「输入有误」。关键字要 2–64 个字符（auth-service 与 user-service 的界限，B170；`GET /admin/v1/users` 的 `q` 超出即 400），查不到而输入只有 1 个或超过 64 个字符时提示「没有完全匹配的账户；按关键字筛选需 2–64 个字符」、列表不变。文本筛选（地区等）停止输入 300 毫秒后自动生效，回车立即生效；地区框写「如 SG」，大小写都行。
- 列表与用户页显示用户名与头像（设计 [用户头像与用户名](../设计-用户头像与用户名-2026-10-07.md)，I3）：上传的头像（列表用 64 px 缩略图，`/uploads/avatars/` 由 nginx 直出，后台站点同样可访问），没有上传时是与两站相同的内置头像（按用户 ID 在 12 个里选，`Avatar` 的 `seed`，A79）。用户页有「重置用户名」（重新随机一个 `user_` + 8 位）与「恢复默认头像」（`POST /admin/v1/users/{id}/username-reset`、`…/avatar-reset`，要 `users.status` 与理由，一人即可；审计 `admin.users.username_reset`（前后用户名）与 `admin.users.avatar_reset`，用户在站内收到通知）。本机开发时 Vite 把 `/uploads` 与 `/downloads` 也代理到测试服。
- 查找 `GET /admin/v1/users/lookup?q=`：按用户 ID、邮箱、手机号（`+` 开头的 E.164）或用户名（不分大小写，B167 起）找到账户；全局搜索也用它。认不出的格式与查不到一样答 404（以前非邮箱非手机号答 400）。按关键字筛列表：auth-service `SearchUsers(q)` 给出邮箱或手机号含它的用户 ID，连同 `q` 一起传给 user-service `ListUsers` 的 `user_ids`，列表就同时按用户名、邮箱、手机号子串匹配（控制台与 `admin.yaml` 的 `q` 是后台会话 A93）。
- 改账户状态（`users.status`；状态机见需求附录 B，原因为大写代码，例如 `SUSPICIOUS_LOGIN`、`REVIEW_CLEARED`；user-service 记审计，操作者为管理员邮箱）；强制撤销用户全部挂单（`orders.cancel`，撮合引擎异步完成）。

### 用户页

用户有自己的页面 `/users/<id>`（C2；原抽屉的地址 `?user=<id>` 跳到这里）：左侧是账户摘要与处置，右侧是标签页。

- **摘要**：UID、邮箱与手机号（脱敏；有 `users.contacts` 的可点「显示完整」，每次都审计 `admin.users.contacts_revealed`，只记显示了哪几种、不记值；离开页面即丢弃）、状态与标签、注册与最近登录、风险评分（风控规则最近一次命中的分数与动作）。
- **资料与身份**：基本资料、身份（类型、脱敏值、验证与绑定时间）、已同意的条款与风险披露版本、状态变更时间线（user-service gRPC `GetUserHistory`）。
- **安全**（auth-service 的 gRPC，见 [auth.md](auth.md#管理后台的安全操作grpcc2)）：身份验证器状态与重置、密码修改时间与登录锁定、生成临时密码、活跃会话（逐个或全部退出）、设备、登录记录（游标分页）。操作需 `users.security` 与理由，审计 `admin.users.totp_reset`、`admin.users.password_reset`、`admin.users.sessions_revoked`。
  - **临时密码**只显示一次（应答带 `Cache-Control: no-store`，不进日志与审计）：原密码失效、全部会话退出、登录锁定清除，用户收到密码重置邮件，24 小时内的提现转人工审核。通过可信渠道告知用户，并提醒用户登录后立即修改。用户站暂不强制"下次登录必须改密码"（见设计稿 §10 C2 的遗留）。
  - 用户的身份验证器被后台重置或用户自己解绑后，24 小时内的提现转人工审核（C5.5 ⑤，auth `totp_changed_at` 经 step-up 的安全上下文到钱包的风控 `SECURITY_CHANGE`）；用户收到的「已解绑身份验证器」通知与邮件写明这一点（⑭）。
- **风控**：风控规则对该用户的评估（触发事件、分数、动作、是否执行、命中规则及说明；risk-service 只读 gRPC `ListAssessments`），并可转人工审核（`RISK_REVIEW`）或审核通过后恢复（同「改账户状态」，需 `users.status`）。
- **余额与资金**：现货与合约账户每个资产的可用、冻结、合计与 USDT 估值（按该资产 USDT 交易对的最新价，没有价格的列出来、不计入总估值）；调整余额可选现货或合约账户（资金操作，见下文）。
  - **风控冻结**（`ledger.hold`，ADMIN、OPERATOR、FINANCE）：冻结现货可用余额的一部分或解冻，由账本记分录与审计（见 [ledger.md](ledger.md#接口)）。
  - **卡住的风控冻结**：别处多解冻了这部分资金时，后台解冻会一直报 `LEDGER_INSUFFICIENT_BALANCE`。运维用 `exchangectl ledger release-hold --id <冻结单ID> --reason "..."` 解冻冻结余额里属于这张冻结单的部分（扣掉其它冻结单、活动订单、待处理提现与 24 小时内还没结算的成交所占的冻结；有未结算成交时要加 `--force`；`--amount` 只能更少，见 [ledger.md](ledger.md)），审计 `ledger.hold_released` 带 `"forced": true` 与实际解冻数（C5.5 ⑧、⑯、⑳、㉒）。
  - **合约账户的扣减**：确认框显示扣减后的全仓权益与维持保证金（`GET /admin/v1/users/{id}/futures-margin?debit=`，按 derivatives-service 风控的口径），会进入预警或会被强平时标红（C5.5 ⑧）。
- **订单**：现货订单（可单笔撤单，`orders.cancel`，审计 `admin.orders.canceled`）与合约当前委托（可单笔撤单，审计 `admin.derivatives.order_canceled`）。
- **仓位**：合约持仓（开仓均价、标记价、预估强平价、未实现盈亏、保证金与模式），每 5 秒刷新。
  - **强制平仓**（`derivatives.write`）先撤该用户在这个合约上的全部挂单（含开仓单，免得平仓后又成交开回去；止盈止损单不撤，它们只会平仓），再以市价全部平掉，见 [derivatives.md](derivatives.md#管理后台与读模型)。撤单与下平仓单之间不锁用户，用户仍可能再开仓，平完再看一眼仓位。
  - 后台最多等约 5 秒看平仓单结束，提示是全部成交还是只成交一部分（盘口薄时剩下的仓位要再平一次）。还没结束时对话框留着（同一个幂等键），提示"已下单、未成交完"，再点确认会查到同一笔订单并记审计 `admin.derivatives.position_closed`（C5.5 ⑯）；改了理由再确认也一样（键不绑理由，C5.5 ⑱）。这只在对话框没关、24 小时内有效：关了对话框或过了 24 小时再平，是新的一笔 ADMIN 单，第一笔的 `position_closed` 不会补写（协调会话决定接受）；要追溯它，用请求审计 `admin.derivatives.position_close_requested` 里的 `client_order_id` 在合约订单里查。
  - 审计两条（C5.5 ⑧）：`admin.derivatives.position_close_requested`（与幂等键一起写，每个请求一次）和 `admin.derivatives.position_closed`（订单结束后，带 `status`、`filled_quantity`、`complete`）。
  - HOUSE 的仓位不能平（422 `DERIV_HOUSE_NOT_CLOSED`）。
- 成交、充值、提现、**备注与标签**（`users.notes`；备注只增不改，审计 `admin.users.note_added`；标签为大写代码，整体替换，审计 `admin.users.tags_changed` 带前后值）、审计。
- **接口**：`GET /admin/v1/users/{id}`、`/notes`（GET、POST）、`PUT …/tags`、`GET …/security`、`POST …/contacts/reveal`、`GET …/login-history`、`POST …/sessions/revoke`、`POST …/totp-reset`、`POST …/password-reset`、`GET …/history`、`GET …/risk`、`GET …/balances`、`GET/POST …/holds`、`DELETE …/holds/{hold}`、`POST …/orders/{order}/cancel`、`GET …/contract-orders`、`POST …/contract-orders/{order}/cancel`、`GET …/positions`、`POST …/positions/close`、`GET …/futures-margin`。与设计稿 §5 的差别：强制平仓按（用户、合约、持仓方向）指定仓位，不用仓位 ID（平仓后再开会换 ID）；解冻用 `DELETE …/holds/{hold}` 带理由的请求体。

### 身份变更申请

`/identity-requests`（侧栏「用户」组，C2）：只有一种身份的用户换绑邮箱或手机号要人工审核（auth-service 的 `auth.identity_rebind_requests`，见 [auth.md](auth.md)）。默认列出待审核的，值脱敏；有 `users.security` 的可通过（身份改为新值并通知用户）或拒绝，需理由，审计 `admin.users.identity_request_decided`。接口 `GET /admin/v1/identity-requests`、`POST /admin/v1/identity-requests/{id}/decide`。

## 资金

### 资金操作：单人与双人审批

资金操作（C1 起）= 手动调账（`LEDGER_ADJUSTMENT`：分录 `MANUAL_ADJUSTMENT`，对手方 `ADJUSTMENT`；现货或合约账户）、保险基金注资（`INSURANCE_FUND`：`INSURANCE_CONTRIBUTION`）、补记充值（`DEPOSIT_BACKFILL`，C2c）、无主充值记给用户（`DEPOSIT_ASSIGN`，C5.5 ㉑；这两种见「充值处置与补记」）、模拟市场的增发（`SIM_MINT`）与超出单人份额的价格事件与参数（`SIM_EVENT`、`SIM_PARAMS`，见「模拟市场」），以及提高注册赠送（`WELCOME_CREDIT`，D2，总要第二位 ADMIN，见「平台设置」）。每笔都是 `approvals` 表的一行（`mode` 为 `SINGLE` 或 `TWO_PERSON`），账本以幂等键 `approval:<id>` 只记一次（补记由 wallet-service 按托管方交易号只记一次，记给用户由账本按充值与用户只放行一次）。

- **开关 `admin.two_person_approval`**（未设置即关闭）。打开：每笔都要另一位管理员批准。关闭（单人模式，测试服现状，因为只有一位管理员）：有权限的管理员自己执行，但有护栏：
  - 单笔折合不超过 `single_max_usdt`（默认 100,000 USDT）；
  - 同一管理员 24 小时内单人操作合计（含结果未知的待处理项）不超过 `daily_max_usdt`（默认 500,000）；
  - 折合按该资产 USDT 交易对的最新价（market-data-service tickers），USDT 按 1；没有报价、或报价超过 60 秒没更新（参考行情停了）的不能单人执行（C5.5 ⑥）；
  - 超过任一限额、或没有报价时，自动转为待另一位管理员批准，`escalation` 写明原因：`SINGLE_LIMIT`、`DAILY_LIMIT`、`NO_PRICE`；双人模式下是 `TWO_PERSON_MODE`，明确要求审批的是 `REQUESTED`，模拟市场超出单人份额的是 `SIM_SHARE`，无主充值记给地址持有人以外的用户是 `NOT_ADDRESS_HOLDER`（不论金额，㉑）。
  - 三个限额各自最多是默认值的 10 倍（单笔 1,000,000、24 小时 5,000,000、提现 1,000,000 USDT），一次修改不能把护栏整个拿掉（C1 评审）。
  - 开关 `admin.two_person_approval` 只按全局判断：给它配了按维度的规则时，后台按"关闭"处理，不要给它配规则。双人模式打开后，申请人仍可完成自己那笔"结果未知"的单人操作——那是重放账本可能已经记过的一笔，不是新的执行。
- **待核对**（C5.5 ⑥）：资金操作执行前先记 `attempted_at`（单人模式在建档时、双人模式在批准时）。执行没有结束（账本或钱包超时、无响应，增发记了一部分后被拒）的操作保持 `PENDING`，`result` 写这次尝试怎么结束的（增发为 `booked n of m; <机器人>: <错误码>: <消息>`），同时审计 `admin.<种类>_unfinished`（如 `admin.ledger.adjustment_unfinished`，C5.5 ⑮），后台显示「待核对」。批准时 `attempted_at` 在决定事务之前单独提交：之间出错会留下「待核对但其实没执行」的操作，完成它照样安全。
  - 它可能已经记账，所以只能完成（再次批准；幂等键 `approval:<id>` 保证不重复记账），不能拒绝或撤回（409 `ADMIN_APPROVAL_ATTEMPTED`）。
  - 增发不要另发一笔新的：完成这一笔只补剩下的机器人。
  - 模拟市场的事件与参数审批不记 `attempted_at`（它们可以结束或改回）。
  - 同一位管理员重复同一个决定（批准或拒绝）返回操作的当前状态，不再执行；相反的决定仍返回 `ADMIN_APPROVAL_DECIDED`。审批期间申请行加锁，两人同时处理时后到者得到 `ADMIN_APPROVAL_DECIDED`。
- **自己的申请**：不能自己批准双人申请（`ADMIN_SELF_APPROVAL`），但可以自己拒绝（撤回）。
- **账本拒绝**（例如开关 `ledger.manual_adjustment` 关闭）时操作记为 `FAILED`；账本无响应时保持 `PENDING`（待核对），可再次批准或由申请人完成。
- **分录备注只取申请理由**（加 `[reference]`），不再拼审批理由：账本比对幂等请求时包括备注，重试换了理由会被当作冲突（修于 C1）。审批理由在审计里。资金操作的审计详情单列 `journal_id`，前后余额以账本分录为准（C1 评审）。
- **接口**：
  - `POST /admin/v1/users/{id}/adjustments`：用户页与「资金调整」页的调账，单人模式限额内立即记账（返回 `EXECUTED` 与 `journal_id`），否则返回 `PENDING`；
  - `POST /admin/v1/ledger/adjustments` 与 `POST /admin/v1/derivatives/insurance-fund/contributions`：不带 `direct` 时总是交给另一位管理员（端到端的双人流程用它），带 `"direct": true` 同上；
  - 可选 `reference`（工单号等，≤ 64 字）写进分录备注；
  - `GET /admin/v1/approvals`（`status` 筛选，游标分页）、`POST /admin/v1/approvals/{id}/decide`（`{approve, reason}`；单人模式的操作允许申请人自己完成）；
  - 账本无响应时返回 `COMMON_UNAVAILABLE`，详情 `approval_id` 指向那笔留在 `PENDING`（待核对）的操作。申请人用同一个幂等键再提交一次，或在「审批」里点「完成」，都不会重复记账。
- **审计动作**：`admin.ledger.adjustment_requested/executed/failed/approved/rejected/unfinished`、`admin.derivatives.insurance_*`、`admin.deposits.backfill_*`、`admin.deposits.assign_*`、`admin.sim.mint_*`（以及 `admin.sim.event_*`、`admin.sim.params_*`）。申请与执行的详情含 `mode`、`escalation`、`value_usdt`、`journal_id`（补记还有网络、交易号、哈希、地址与 `"custodian_checked":false`；记给用户还有充值 ID、所选的 `user_id` 与地址的持有人）；决定的审计带 `status`、`result`、`mode` 与 `journal_id`，记给用户的另带充值 ID、所选用户与持有人（㉕）。
- **页面**：「资金调整」（`/adjustments`：审批方式与 24 小时已用额、查找用户、方向/账户/资产/数量/关联单号；资产从参考数据的资产列表里选（读不到列表时才手填）；减币的确认词是金额本身，加币是用户 ID 后 4 位；单人模式下立即记账并显示分录号与两条分录，或显示待审原因；最近的资金操作）与「审批」（`/approvals`：默认待处理，显示方式与转审原因、申请人与审批人邮箱；待核对的标出；自己的单人操作可「完成」，自己的申请可「撤回」；模拟市场的申请显示变化与测得的幅度、过期标「已过期」；记给用户的申请在记给的不是地址持有人时标出持有人）。
- 设置（双人开关与三个限额）见「系统 → 设置」。

### 充值处置与补记

充值页有三个视图：全部充值（读模型 `GET /admin/v1/deposits`，按检测先后新到旧，可按用户、资产、网络、状态、交易哈希（`tx_hash`，不分大小写）筛选）、**待处理**（`?view=attention`）、**补记待回调**（`?view=manual`）；后两个直接读 wallet-service（`GET /admin/v1/deposits/review?attention=true|manual_pending=true`，`withdrawals.read`），详情 `GET /admin/v1/deposits/{id}`。处置、补记与记给用户要 `deposits.review`（ADMIN、FINANCE）。（C2c 起）

- **待处理的充值**：低于最小充值额、账户已关闭或不符合资格的（已记在系统科目 `UNCLAIMED_DEPOSIT`）、未支持的代币（没有记账）、补记后托管方回调与录入不一致的（`discrepancy`）、无主充值（见下）。
  - **入账给用户** `POST /admin/v1/deposits/{id}/credit`（理由，带 `Idempotency-Key`）：只限已记入 `UNCLAIMED_DEPOSIT` 的，按原币种、原数量由账本 `ReleaseUnclaimed` 转给用户现货账户（分录 `DEPOSIT_CREDIT`，幂等键 `deposit-release:<id>`，账本审计 `ledger.unclaimed_released`），充值变为 `CREDITED`、记下放行分录。不能改数量；未支持的代币与回调不一致的补记不能入账（409 `WALLET_DEPOSIT_NOT_RELEASABLE`），要补偿另做资金调整。理由只进审计，不进分录：账本已放行、钱包没记上时，换个理由再点「入账」也只是记下原来的放行（C5.5 ⑦）。
  - **驳回** `POST /admin/v1/deposits/{id}/reject`（理由）：只标记为已处理（`resolution = DISMISSED`），不动资金，wallet-service 审计 `wallet.deposit.dismissed`。处理过的再处理得到 `WALLET_DEPOSIT_RESOLVED`。账本已经放行、但钱包没记上的不能驳回（409 `WALLET_DEPOSIT_RELEASED`，详情 `journal_id`；钱包驳回前按 `deposit-release:<id>` 问账本 `GetUnclaimedRelease`），再点「入账」把放行记下来（C5.5 ⑦），不会再放行一次；记下来的那次 wallet-service 审计 `wallet.deposit.release_recorded`（带账本的 `journal_id`；放行本身由账本审计，C5.5 ⑰）。
- **无主充值**（B7a，C5.5 ㉑）：托管方报到账、但地址不属于任何用户（探测地址、退役的地址）的充值，`user_id` 是空 UUID、原因 `UNKNOWN_ADDRESS`，记在 `UNCLAIMED_DEPOSIT`，在「待处理」里用户一栏显示「无主」（不按用户去查）。详情列出地址现在或退役前的持有人（`address_owner`、`address_owner_retired`），只作参考：持有人不一定是付款人，先核实转账。
  - **记给用户** `POST /admin/v1/deposits/{id}/assign`（`{user_id, reason}`，带 `Idempotency-Key`，要 `deposits.review`）：一笔资金操作（`DEPOSIT_ASSIGN`，迁移 admin 00011），护栏与调账相同——单人模式不超过单笔限额时立即执行，超过限额、无报价或双人模式时等另一位管理员批准（`ledger.adjust.approve`）。
  - 地址现在或退役前有持有人、而记给的不是这个持有人时，不论金额都等另一位管理员（升级原因 `NOT_ADDRESS_HOLDER`，协调会话 10-04 的决定）；操作与审计记下原持有人 `former_holder`（退役的地址）或现持有人 `address_owner` 与所选的 `user_id`，弹窗与审批列表都提示。
  - 执行时 wallet-service 把持有人设为这个用户、由账本 `ReleaseUnclaimed` 按原币种原数量转入其现货账户（审计 `wallet.deposit.assigned`、`ledger.unclaimed_released`），操作带放行分录的 `journal_id`；后台审计 `admin.deposits.assign_requested/approved/executed/…`。确认词为用户 ID 的后 4 位；用户 ID 不是 UUID 时确认按钮不可用。
  - 一笔充值同时只有一个有效申请：已有等待中（含结果未知）或已执行的申请时，新的申请返回 409 `ADMIN_DEPOSIT_ASSIGN_OPEN`（迁移 admin 00012 的部分唯一索引，两个操作不会共用一次放行、重复计入限额，㉕）；驳回或撤回后可重新申请。充值 ID 与用户 ID 先转成标准写法（小写、带连字符）再记录与比较：大写、花括号、`urn:uuid:` 或不带连字符的写法都指同一笔充值、同一位用户（㉖）。
  - 账本已把这笔无主充值放给某位用户、钱包还没记上时，驳回与记给别人都得到 409 `WALLET_DEPOSIT_RELEASED_TO_USER`（详情 `user_id`、`journal_id`）：记给详情里的这位用户即补上记录，不会再放行。不是等待处理的无主充值返回 409 `ADMIN_DEPOSIT_NOT_UNOWNED`；用户现在不能充值时由 wallet-service 拒绝。
  - 结果未知（例如 wallet-service 已放行、答复丢了）时操作停在「待核对」，同一请求（同一键）再来：wallet-service 对已有持有人的充值答 409，后台读这笔充值，已记给同一用户就按已完成记录，不会再放行一次（账本的放行只取决于充值与用户）。
  - 无主充值不能直接「入账给用户」（409 `WALLET_DEPOSIT_NO_OWNER`）；也可以照常驳回。
- **补记充值**（托管方已到账、回调丢失）：优盾网关没有按交易号查询的接口，系统无法向托管方核对。管理员先在优盾商户后台或区块浏览器核对，再在充值页「补记充值」录入网络、托管方交易号（tradeId）、充值地址、交易哈希与数量：
  - `POST /admin/v1/deposits/manual/check` 只核对不记账：网络由托管方服务、地址是该网络上某个用户的充值地址、`UDUN:<tradeId>` 与（网络、哈希、地址）都没出现过（否则 409 `WALLET_DEPOSIT_KNOWN`，详情带已有充值的 ID）、数量不超过资产精度；返回入账用户、资产、是否低于最小额与折合 USDT。
  - `POST /admin/v1/deposits/manual`（再带理由与 `Idempotency-Key`）是一笔资金操作（`DEPOSIT_BACKFILL`），护栏与调账相同：单人模式限额内立即补记（`EXECUTED`，`result` 为 `deposit <id>`），超过限额、无报价或双人模式时等另一位管理员批准。确认框明示「托管方未核对」。
  - 补记走与回调相同的路径：充值记为 `CONFIRMED`、来源 `MANUAL`、录入人为申请人，由处理器交账本入账（低于最小额同样进 `UNCLAIMED_DEPOSIT`），wallet-service 审计 `wallet.deposit.backfilled`。同一笔补记同时提交两次，后到的撞唯一索引后按「同一补记再来」返回先到的那笔充值，第二笔资金操作也就完结（C5.5 ⑦）。
  - 托管方的回调晚到时按交易号找到这笔补记：地址、资产、数量一致即记「已核对」（回调日志 `APPLIED`，不再入账）；不一致时不更正、不再入账，回调日志记 `DISCREPANCY`、充值转为待处理并告警（`wallet_custody_deposit_discrepancies_total`，告警 `CustodyDepositDiscrepancy`）。查明后驳回或另做资金调整。回调比处理器先到时，这笔补记不再交账本入账，等人处理（C5.5 ⑦）；托管方重发同一回调，`DISCREPANCY` 保持不变（已是终态，不会被改写成 `IGNORED`）。
  - 「补记待回调」列出还没等到回调的补记；`exchangectl wallet reconcile --network UDUN`（或 `checks --network UDUN`）的报告在对账表后单列它们——托管方余额里没有对应的到账，就是录错了。

### 提现审核

- **列表** `GET /admin/v1/withdrawals`：按 `status`（`ALL` 为全部；默认 `PENDING_REVIEW`）、`user_id`、`asset`、`network`、`held=true|false`（是否搁置；只算待审批的，已决的不再列为搁置，C5.5 ⑦）、`min_value_usdt`/`max_value_usdt`（折合金额区间）、`min_risk`（风控分下限）过滤。审核队列默认从旧到新，其余状态从新到旧，`order=asc|desc` 可改。托管网络的提现带 `custody`、托管方状态 `provider_status` 与交给托管方的时间 `submitted_at`。显示风控分与命中规则。
- **审核**：批准/拒绝需理由（`withdrawals.review`，带 `Idempotency-Key`），审批人为管理员邮箱；超过 20,000 USDT 需两位不同审批人（规则在 wallet-service）。`exchangectl wallet approve|reject` 仍可用。
  - **单人模式下的提现**：需两人审核的提现折合不超过 `withdrawal_max_usdt`（默认 100,000）时一人批准即完成（admin-service 把 `sole_max_usdt` 传给 wallet-service 的内部审核接口，wallet 审计里记 `sole_max_usdt`）。批准时 admin-service 还按当前价重算一次（同样 60 秒新鲜度；平台自有交易对按最近一笔成交的时间算）：申请时的估值与当前估值都不超过才传 `sole_max_usdt`；超过、或没有新鲜报价时传 `approvals_at_least: 2`，wallet 把所需审核人数**提到** 2（风控原本只要 1 人的也一样），这次批准照常计数，要另一位管理员再批（C5.5 ⑥、⑮；wallet 审计记 `approvals_required`；`approvals_at_least` 最多算 2，⑰）。所以行情接口不可用、或报价超过 60 秒没更新时，单人模式下所有非 USDT 的提现都要两人审核；只有一个管理员的测试服可用 `exchangectl wallet approve <id> --reviewer <另一个名字> --reason "..."`（见 [wallet.md](wallet.md)）以另一审核人身份批完，审计里记的是命令行给的名字。
  - **批量审核** `POST /admin/v1/withdrawals/review-batch`：一次最多 50 笔，用同一个理由逐笔处理、逐笔审计，各自返回结果；审核队列可勾选（一键选中低风险的）后一起批准或拒绝。批量审核跳过搁置的提现（该条结果 `ADMIN_WITHDRAWAL_HELD`，要单独审核），「选中低风险」也不选它们（C5.5 ⑦）。
- **详情** `GET /admin/v1/withdrawals/{id}`（`withdrawals.read`，C2c）：提现本身、地址在用户地址簿里的记录（标签、添加时间、冷却期结束时间；已删除为 null）、用户今日与本月已提折合（UTC，含审批中与处理中，不含被拒与已撤销）、该用户最近的提现与托管方回调。页面上地址在提现前 72 小时内加入的标「新地址」（同风控规则 `NEW_ADDRESS`），冷却期未过的标「冷却中」。用户限额取决于提现时 step-up 带的身份数与是否绑定身份验证器，钱包不保存，所以只显示已提金额，不显示占用比例。
- **搁置** `POST /admin/v1/withdrawals/{id}/hold`（`withdrawals.review`，`{"hold": true, "note": "..."}`，取消时 `hold: false`）：只限待审批的提现（否则 409 `WALLET_WITHDRAWAL_NOT_IN_REVIEW`）；搁置的仍在队列里，列表标「已搁置」并带备注、搁置人与时间，批准或拒绝时自动取消。wallet-service 审计 `wallet.withdrawal.hold`、`wallet.withdrawal.unhold`（操作者为管理员邮箱）。
- **提现暂停**（C5.5 ⑯）：托管核对两次都短缺时，wallet-service 自动暂停该资产的提现，见 [custody.md](custody.md)。
  - 「提现」页顶部每个暂停的资产一条红色横幅：原因、开始时间与操作人、短缺数量。列表里等待解除的已批准提现标「暂停等待」。
  - 这种资产的提现照常批准，不拒绝。批准的响应带 `suspended_at` 与 `suspension_reason`，后台提示「已批准；该资产提现暂停中，解除后才发出」；批量审核的结果带 `suspended`。
  - ADMIN（`withdrawals.resume`）在横幅上「解除暂停」，要理由与确认词（资产代码），与 `exchangectl wallet withdrawals-resume` 同一套：已批准的提现一轮内发出，托管核对重新开始，wallet-service 审计 `wallet.withdrawals.resume`（操作者为管理员邮箱）。
  - 接口：`GET /admin/v1/withdrawals/suspensions`（`withdrawals.read`）、`POST /admin/v1/withdrawals/suspensions/{asset}/resume`（不在暂停中答 404）；wallet-service 的内部接口为 `GET /internal/wallet/suspensions` 与 `POST …/{asset}/suspend|resume`。
  - 后台不提供手动暂停与 `--accept`，这两样仍用 CLI。解除不带幂等键：已经解除后再点得到 404，就是已经解除了；钱包的暂停列表读不到时，批准的响应里不带暂停提示（日志 `withdrawals: read the suspensions`）（C5.5 ⑲，接受）。

### 托管方

`/custody`（阶段 4 B6，C6；[custody.md](custody.md)）。读要 `withdrawals.read`。

- **两个托管方**（C6，ADR-0017）：页头的切换在优盾 `UDUN` 与替身 `UDUNMOCK`（只服务端到端用的隐藏测试资产 TUSD）之间换，地址栏带 `?provider=UDUNMOCK`，标题随之变化。`GET /admin/v1/custody?provider=`、`GET /admin/v1/custody/callbacks?provider=` 只给这个托管方自己的核对与回调（另加平台自建钱包的核对）；不带时为 `UDUN`。
- **概况与币种**：托管方状态（可访问、未配置、读不到及原因）、处理中的提现（数量、折合、最早的时间，可跳到提现列表）、待处理回调数与最近回调；托管方的币种与余额、使用它的网络。
- **对账**（不变量 4，每个持有方与资产最近一次）：持有、其它持有方、在途提现、未入账手续费、账本应有、短缺；持有方显示「托管方 · 名字」或「自建钱包 · 网络」。切换真网关后出现「替身基线」一列（`baseline`：切换时从账本应有里扣除的替身模拟充值，不在任何托管方），全为 0 时不显示。
- **回调日志**：按结果（`result`）、类型（`kind`）、交易/提现 ID、哈希或地址（`q`）筛选（游标分页）；详情 `GET /admin/v1/custody/callbacks/{id}` 有原始请求（签名打码）与来源地址（`remote_ips`，最近 8 个）。验签通过且 `FAILED`、`UNMATCHED`、`RECEIVED` 的回调可以重放：`POST /admin/v1/custody/callbacks/{id}/replay`（理由，需 `withdrawals.review`，wallet-service 写审计 `wallet.custody.callback.replay`）。
- **托管方手续费**（C6，审查 ④ 的后台）：同页下方「托管方手续费」，只列页头所选托管方的，`GET /admin/v1/custody/fees?provider=&status=HELD|BOOKABLE|WRITTEN_OFF`（不带 `provider` 为全部托管方；默认先看待人工处理）。
  - 计费方式确认过的按报告从 `GAS_SUPPLY` 入账（`BOOKABLE`；`GAS_SUPPLY` 不足时分录为空、显示「等待 GAS_SUPPLY」）；未确认或看起来不对的为 `HELD`。
  - 有 `ledger.adjust.approve` 的管理员处理 `HELD`：「入账」`POST /admin/v1/custody/fees/{withdrawal_id}/book`（`{asset?, amount?, reason}`：留空按报告，填写则按实扣；平台须在该网络的托管方持有这个币种、小数位不超过其精度，否则 400，请核销；处理器一轮内记账）或「核销」`POST …/write-off`（`{reason}`，不记账；也用于等待 `GAS_SUPPLY` 的那笔）。确认词为提现 ID 后 4 位。
  - 按实扣入账最多是报告的 5 倍：同币种按数量，换了币种按现价折 USDT 比较（422 `ADMIN_FEE_ABOVE_REPORTED`；有一边没有新鲜报价时 422 `ADMIN_FEE_UNPRICED`），更多的由运维用 `exchangectl wallet custody-fee` 入账（一位管理员不能单独从 `GAS_SUPPLY` 记任意数额，㉖）。报告的数额从钱包的待处理列表里找，找不到这笔时按实扣入账一律拒绝（409 `ADMIN_FEE_NOT_HELD`，㉗）。列表按托管方每页 200 条最多翻 50 页：待处理的超过 10,000 笔、或翻页期间有人处理了前面的，可能误报 `ADMIN_FEE_NOT_HELD`，重试即可；钱包有按提现查一笔的接口后改用（复审 ㉘，待办）。
  - 与 `exchangectl wallet custody-fee` 走同一条路径，wallet-service 以管理员邮箱审计 `wallet.custody.fee.book` / `wallet.custody.fee.write_off`。不带幂等键：已处理的再处理得到 409 `WALLET_CUSTODY_FEE_NOT_HELD`（详情 `status`），刚被另一个决定处理的 409 `WALLET_CUSTODY_FEE_CHANGED`，不会重复入账；没有手续费的提现 404 `WALLET_CUSTODY_FEE_NOT_FOUND`。决定无论成败都刷新列表（答复丢了再点得到 409 时，行也会显示已处理，㉖）。

### 对账与系统科目

- `GET /admin/v1/ledger/reconciliation`：账本对账每项检查的最近一次结果与最近 50 次不一致（各带前 10 条差异），经 ledger-service gRPC `GetReconciliation` 读 `reconciliation_runs`。
- `GET /admin/v1/ledger/system-balances?asset=`：全部系统科目余额（留空为全部资产）。
- 页面 `/ledger` 两个标签：对账、系统科目。

## 交易

### 订单与成交

- `GET /admin/v1/orders`：现货订单的最新状态，读模型 `orders_current`，可按用户、订单号（全局搜索用）、交易对、状态、方向、账户、时间过滤。交易服务受理前就拒绝的订单没有方向与类型（`side` 为空）。
- `GET /admin/v1/trades`：现货成交，`user_id` 匹配买卖任一方；`house_side` 是 HOUSE 一方的方向，用户之间成交为空。
- **机器人筛选**（C5）：`?accounts=bots|users`。订单按下单账户；成交 `bots` 是两边都是机器人，`users` 是至少一边不是（用户与机器人之间的成交算用户的）。每行带 `bot`（成交为 `buyer_bot`、`seller_bot`）。筛选要 market-sim 的机器人名单，读不到时 503；不筛选时只是不标记。
- 页面两个标签，HOUSE 一方显示为 HOUSE，机器人标出；可导出已加载的行为 CSV。

### 仓位与强平记录

- **仓位**（`/positions`，C3）：全部用户的合约持仓，按保证金率（维持保证金 ÷ 保证金余额）从高到低、每 5 秒刷新。「风险仓位」只看被预警（含全仓账户被预警）、被接管或保证金率 ≥ 50% 的，不含 HOUSE（C5.5 ⑨）；可按合约、用户筛选。HOUSE 的仓位排在最后并标出（它的单按最高杠杆记保证金，零盈亏时保证金率约 50%，不排后会占满"最危险"；它是用户的对手方，不能在这里平）；标记价不新鲜的行在标记价下标「标记价过期」（C5.5 ⑨）；其余可强制平仓（`derivatives.write`，同用户页）。接口 `GET /admin/v1/positions`（最多 500 个，`truncated` 表示还有更多；derivatives-service 的内部接口 `/internal/derivatives/positions`）。
- **强平记录**（`/liquidations`，C3）：强平引擎的每一步（预警、接管、强平成交、自动减仓），按环节（WARNING/STARTED/FILLED/ADL/ENDED）、合约、用户、近 1/7/30/90 天筛选（读模型；`limit` 最多 500）。

### 合约与保险基金

`/derivatives`（阶段 3 任务 10，见 [derivatives.md](derivatives.md#管理后台与读模型)）：

- 每个永续合约的状态、只减仓（原因与时间、谁解除的）、标记价是否新鲜、持仓量与持仓数。
- 解除只减仓（`derivatives.write`，标记价恢复后才可操作，审计 `admin.derivatives.reduce_only_lifted`）；改合约状态（交易参数，只有 ADMIN，见「交易参数的护栏」；暂停立即生效）。
- 保险基金余额与 `PNL_CLEARING`；注资（资金操作 `INSURANCE_FUND`，金额为正；单人模式限额内立即记账，否则另一位管理员批准；批准后账本 `FundInsurance` 以幂等键 `approval:<id>` 记 `INSURANCE_CONTRIBUTION`（对手方 `ADJUSTMENT`），需开关 `ledger.manual_adjustment`），审计 `admin.derivatives.insurance_*`。
- 币本位合约（设计 2026-10-06 §2.7，G5 第一部分）：后台读 instrument-service 的合约时要两种保证金类型（`margin_type=ALL`），`/admin/v1/instruments` 与配置导出的合约都带 `margin_type`（`USDT`/`COIN`）、`settle_asset`、`contract_size`（币本位的面值，美元）与 `reference_symbol`；「交易品种」的合约页签显示类型、结算币与面值，合约编辑里的档位名义价值按结算币标单位（币本位以币计）；本页的合约表加结算币列。保险基金页签按资产列出（`GET /admin/v1/derivatives/insurance-funds`：USDT 在前，再是各未下架合约的结算币与基金里有余额的其他资产），注资可选资产，双人规则不变（估值用 `<资产>-USDT` 价）。合约状态、持仓等仍来自 derivatives-service，它在 G1 之前只列线性合约。
- 各行的结算币（审查 ER ①，G5 第二部分）：接口里合约的行都带可选的 `settle_asset`，页面"有则显示、空即 USDT"——仓位（持仓页、用户页、HOUSE 页）与合约委托取 derivatives-service 的（仓位另有 `contracts`、`value_coin`、`value_usd`）；成交记录取读模型 `trades` 的列（现货为空）；强平记录、合约日报与持仓量报表由后台按合约代码从合约列表补上（列表读不到时为空；全仓账户的预警不指合约，也为空）。盈亏、保证金、手续费、资金费与保险赔付按结算币标单位（USDT 到分、币全精度），币本位的数量标「张」，其成交额为美元；HOUSE 页的净头寸对币本位用面值（美元）作名义价值。用户页扣合约账户时的预览带 `?asset=`（按该资产的全仓权益，C39 ⑤）。
- 强平监控与强平记录 C3 起各有一页（见上）。

### HOUSE 敞口

`/house`（阶段 4 B5，C6 重做）。

- `GET /admin/v1/house`：HOUSE 的账（ADR-0013、0015）。库存是账本 `MARKET_MAKER` 各资产余额，按 USDT 交易对的最新价估值（可充提资产在前，站内资产卖出后为负）；各交易对的成交来自读模型 `trades` 的 `house_side`（买入、卖出、付出与收到），盈亏 = 净持有 × 现价 + 净收入；合约仓位是 `HOUSE_USER_ID` 在 derivatives-service 的持仓（admin-service 从 `apps.env` 读 `HOUSE_USER_ID`）。读不到的部分记在 `partial`。
- 页面（每 30 秒刷新）：四个合计（库存估值与资产数、可充提、站内（可为负）、按现价的交易盈亏与有成交的交易对数）；「近 30 日盈亏」（报表 `house-pnl` 按日：现货、合约、资金费的柱子与累计线，缺价格的交易对列出）；「敞口」（库存按现价折成 USDT、绝对值最大的 10 个，向右为持有、向左为卖出后为负，可充提的标点）；「库存」（按估值大小排序，按全部/可充提/站内与代码筛选，没有价格的标出）；「各交易对」（按盈亏大小排序，按代码筛选）；「各合约净头寸」（每个合约都列，无仓位为 0，方向多/空/无仓位，有仓位的在前，显示几个合约有仓位）。
- **额度**（用户 2026-10-07 决定，A69；06:0x 补充每项说明；market-maker 的运行时额度见 [market-maker.md](market-maker.md) 的「运行时额度」，C45/C47）：「额度」卡逐项显示 HOUSE 报价时用的六项——名称与接口名、单位、当前值、首次默认（第 1 版，即 market-maker 第一次启动时部署环境变量给的值；不在最近 100 次修改里时显示为未知）、允许范围、用途、调低与调高的影响：

  | 项 | 单位与范围 | 用途 | 调低 | 调高 |
  |---|---|---|---|---|
  | 每档上限 `level` | USDT，≥ 0，0 = 不限 | 每个盘口每一档最多报出的数量 | 大单要吃多档、分多次成交 | 价格突刺时单笔成交的损失更大 |
  | 单资产上限 `symbol` | USDT，> 0 | HOUSE 持有某一资产的市值上限（站内资产可为负，按绝对值），超过后所有交易对停止买入该资产 | 热门币很快买满、停止买入 | 库存风险集中在少数资产上 |
  | 总上限 `total` | USDT，> 0 | 除 USDT 外全部持仓的合计市值上限 | 全站更早停止买入 | 整体库存风险上升 |
  | 单合约上限 `contract` | USDT，> 0 | 每个合约净头寸的名义价值上限，到达后该方向只减仓 | 用户开仓被拒或只部分成交 | 单个合约的方向性风险增大 |
  | 安全边际 `safety` | USDT，> 0 | 可充提（背书）资产保留不卖的市值与买入时保留的报价资产余额 | 可卖出的背书资产更多，接近 0 时可能卖光 | 可报的数量减少 |
  | 合约杠杆上限 `contract_leverage` | 倍，1–125 | HOUSE 在每种结算币上的全部合约暴露不超过该币合约账户权益 × 倍数 | 报价量缩小，超限只报减仓方向 | 极端行情下合约权益可能被打穿 |

  USDT 金额都不超过 1e15；一次修改每项最多调到现值的 10 倍或十分之一，合约杠杆上限也一样（10 倍改到 125 倍要两次：10 → 100 → 125）；每档上限改到 0 或从 0 改起不算；与 market-maker 的 400 `HOUSE_CAPS_STEP` 相同，后台申请时就拦，申请框写明这条规则，超出时逐项提示这次可改的范围。单资产上限与总上限旁显示 HOUSE 当前的现货持仓（页面 `GET /admin/v1/house` 的库存按最新成交价折成 USDT，不含 USDT，站内资产卖出为负、按绝对值算，与 market-maker 对照额度的算法相同，它用参考盘口中间价，数值可能略有出入）：最大的单资产持仓与合计，括号里是占额度的比例，达到额度时标红；申请的新值不高于当前持仓时（等于时 market-maker 的买入额度已是 0，A80 ①）申请框红字提示（单资产：保存后 HOUSE 将在所有交易对停止买入该资产，卖出为负的站内资产则停止卖出；总上限：停止买入、只买回卖出为负的站内资产，也不再把站内资产卖到负数），审查 R18。卡上还有版本、最近一次修改人（「部署默认值」即 `environment`）与时间、待批准的修改、market-maker 记的最近 10 次修改（逐项旧值 → 新值及影响、申请人/批准人、原因；用 `ops` 键经 exchangectl 改的标「运维命令」，没有经过后台审批）与「全部记录」（审计日志按目标 `house:caps` 筛选）。测试服现值：前四项各 500,000,000、安全边际 1,000、杠杆 10。接口 `GET /admin/v1/house/caps`（`reports.read`；经 admin-service 读 market-maker 的 `GET /internal/house/caps` 与 `…/changes?limit=100`，`MARKET_MAKER_URL=http://market-maker:8091`；`initial` 为第 1 版）。修改：`POST /admin/v1/house/caps`（`{caps: {只填要改的}, version: 读到的版本, reason}`，`ledger.adjust.request`，与 HOUSE 资金操作相同；只收与现值不同的项，超出上表范围 400）建一条审批 `HOUSE_CAPS`（迁移 admin 00016），申请框逐项列出旧值 → 新值及其影响；**无论审批模式都要另一位管理员**（`ledger.adjust.approve`，申请人不能批自己的），同时只能有一条待批（409 `ADMIN_HOUSE_CAPS_PENDING`），读到的版本已旧 409 `HOUSE_CAPS_VERSION`；一天未决即过期。批准后 admin-service 以申请人的名义、带批准人与审批 ID 调 market-maker 的 `PUT /internal/house/caps`，下一轮报价（250 毫秒内）生效；按申请时的版本，期间有人改过则审批失败（结果为 `HOUSE_CAPS_VERSION`，重读后重新申请）；答复丢失时按 market-maker 修改历史里的审批 ID 认定已生效。审计 `admin.house.caps_requested`、`caps_approved`/`caps_rejected` 与逐项前后值的 `admin.house.caps_changed`；审批页逐项显示前后值与影响。签名（C47）：admin-service 用 `HOUSE_CAPS_ADMIN_API_SECRET`（服务器 `infra/house/admin.env`，部署脚本生成并挂给 market-maker 与 admin-service）以键 `admin` 签名 PUT（svcsign，与 market-sim 相同；只有这把键能带批准人）；没有这个变量时启动日志告警，额度在后台只读：批准时答 503、审批留在待批，补上变量重启后再批准。

## 市场

### 资产与交易对的编辑

「资产与交易对」页按 `deploy/instruments/test.json` 的格式读写参考数据（instrument-service 的 `ExportConfig`/`ApplyConfig`，见 [instruments.md](instruments.md#管理后台编辑2026-10-02-设计-44c3)）。需要 `instruments.write`（ADMIN、OPERATOR；交易参数另见下一节）。（C3）

- **页面**：交易对（参考市场与倍数、步长、费率）、资产（充提开关、网络、资料）、合约（风险阶梯）、费率档四个列表，可搜索，点行编辑，右上角新增；「上架向导」粘贴交易对的 CSV（第一行表头）或一份 JSON 配置文档；「待生效修改」见下一节。每次修改都先**预览**：逐项列出新增或修改、改了哪些字段（旧值划掉、新值）、后台的提示，填理由并输入第一项的代码后才生效。新交易对与合约处于「准备中」，确认无误后用列表里的「操作」开放。交易对与合约按状态机改状态（`TRADING ⇄ HALT`；`CANCEL_ONLY` 下线前可以恢复交易（B122），`DELISTED` 不可恢复）。
- **接口**：`GET /admin/v1/instruments`（资产、交易对与合约的现状，`instruments.read`）、`GET /admin/v1/instruments/config`（配置文档）、`POST /admin/v1/instruments/preview`（`{config}`，只算变化）、`POST /admin/v1/instruments/apply`（`{config, reason, confirmation?}`，审计 `admin.instruments.applied`，对象 `instruments`，详情为每项的实体、代码、动作与版本）。文档里每项整体替换（漏写的字段变空，所以页面总是从导出的整项开始改），状态不在这里改，不删除。
- **核对与提示**：交易对的新参考符号先向币安核对，币安现货没有的拒绝（422 `ADMIN_REFERENCE_UNKNOWN`）。提示（`warnings`，按代码本地化）：`HOUSE_NOT_LISTED`（不在 `market.house_liquidity` 名单里，HOUSE 不报价）、`HOUSE_QUOTES`（在名单上，生效后 HOUSE 按币安盘口报价，C5.5 ⑩）、`STATUS_IGNORED`（文档给已有交易对或合约写了别的状态，不生效）、`NO_FUTURES`（币安没有对应合约，HOUSE 不给合约盘口）、`NO_INDEX_REFERENCE`（合约的指数交易对不跟随参考行情）、`REFERENCE_UNCHECKED`（核对不了）、`STREAMS_RECONNECT`（行情服务会重连全部参考行情流，约 20 秒参考盘口为空）。
- **与部署的关系**：后台改过的项记为来源 `CONSOLE`，之后部署同步文件时保留它（输出里有 `kept …`）；`exchangectl instruments apply --force` 让文件重新说了算。要长期保留的改动也应改进 `test.json`。
- 与设计稿 §5 的差别：没有单独的 `POST/PUT /admin/v1/instruments/{assets,networks,pairs,contracts}` 与 `GET/PUT /admin/v1/fees`，都走配置文档的预览与应用（一条路径、同一套校验）；参考符号映射在交易对的编辑里。

### 产品线

（设计 [产品线开关](../设计-产品线开关-2026-10-07.md) §1 #5，K3）「资产与交易对」页顶部的「产品线」卡：币币交易、U 本位合约、币本位合约各一个开关（功能开关 `product.spot`、`product.usdt_m`、`product.coin_m`，config 00002 写入为开；没存的按开算），只能开或关、不能删除。每条显示开放/已关闭、最后一次切换的人与时间，以及关闭它现在会碰到的挂单数与持仓数——由运行这条线的服务数（`GET /internal/products/{线}`：现货为 spot-trading-service，合约为 derivatives-service，三条线并行读；合约的挂单含止盈止损，现货的挂单含杠杆账户的单、不含强平单、含关闭后保留的还款单，现货的「持仓」是欠款的杠杆账户数（最多 15 秒数一次），HOUSE 与做市账户 `MARKET_MAKER_USER_IDS` 不计）；服务读不到或还没有这个接口时那条线的数为空、列入 `partial`。

- 接口：`GET /admin/v1/products`（`instruments.read`）；`PUT /admin/v1/products`（`{product, enabled, reason}`，`instruments.trading`，即 ADMIN，一人即可、不涉及资金）。功能开关页不再切换 `product.*`（400，开关旁有链接到这张卡）。
- 关闭：先改开关（审计 `flag:product.<线>` 的前后值），再请该线的服务撤销挂单（`POST /internal/products/{线}/cancel-open`，契约 `api/internal/products.yaml`：合约含止盈止损，强平、ADL 与后台平仓单不撤；现货含杠杆账户的单，强平单与还款单不撤；每笔由服务审计为 `admin.orders.canceled`；开关还开着时服务答 409、读不到开关答 503；服务另有每 5 秒一轮的兜底，撤关闭前 30 秒起进来的单（开关时间取库的钟、订单时间取服务的钟，留出时钟差），审计人为 `system:spot-trading-service` 或 `system:derivatives-service`，关闭前几秒才下的单因此可能不在撤单数里）。后台等撤单 15 秒、每条线的计数 5 秒：一次切换要在后台自己 30 秒的写超时内答复（服务的写超时也是 30 秒）。应答带 `canceled_orders` 与 `cancel`（A85、A86、A87）：`status` 为 `DONE`；`UNAVAILABLE`（服务还没有这个接口）；`FAILED` 时 `reason` 说原因——`TIMEOUT`（15 秒内没答完，或请求在那之前已断开，服务可能还在撤）、`UNREACHABLE`（连不上）、`PARTIAL`（服务某个用户撤不了时撤完其余用户再答 503，`details` 带已撤数 `canceled` 与没撤成的用户数 `failed_users`，合约 C60、现货同样）、`REFUSED`（服务答了别的错）；`canceled` 是服务说已撤的数，`error` 只放服务答的错误码与说明，不含内部地址。没撤完时开关照样是关，审计里写明（`cancel: unavailable` 或 `failed`，另有 `cancel_reason`、`failed_users`，`cancel_error` 只记服务答的错误码与说明——审计详情在后台页面上照原样显示，所以连不上、超时的完整错误（带服务地址）只写进 admin-service 的日志，A88），页面不报成功、提示原因与重试；已关的线还有挂单或挂单数读不到时，卡上显示「撤销剩余挂单」，再关一次即只撤单、开关不变（审计 `admin.products.orders_canceled`，计数读不到时也照撤）；现货的还款单算在挂单数里但不撤，所以这个按钮可能一直在。确认框写明影响：两站一分钟内隐藏、新单一律拒绝（只放行撤单与只减仓的平仓单）、撤销挂单数、保留的持仓数；关币币交易另提示平台币 ASTRA 现货停牌、以它为指数的 ASTRA 永续进入只减仓。HOUSE 与做市账户照常报价（平仓与强平要对手方）。确认词为线的代号（`spot`、`usdt_m`、`coin_m`）。
- 打开：只改开关，不撤单；关闭期间撤掉的挂单不恢复。切换到它现在的状态不改不撤。审计 `admin.products.toggled`（对象 `product:<线>`，详情：线、开关、从/到、撤单数、撤单失败的原因；开关改了就一定有审计——请求在切换后断开时，审计在不随请求取消的上下文里写完，限 5 秒，A91）。
- 两站读公开的 `GET /v1/platform/products`（instrument-service，缓存 30 秒），一分钟内生效；新单的拒绝（`PRODUCT_CLOSED`）、划入合约账户的拒绝与模拟市场机器人的暂停由各服务按开关执行（K1a、K1b）。

### 交易参数的护栏

（设计 §2 第 6 条，C3c）一次调用就能把费率改到 10%、让高杠杆仓位在几秒内被强平、或掐断 HOUSE 的流动性，所以这些"交易参数"的修改另有一套规则：

- **哪些是交易参数**：交易对与合约的状态；费率档的 Maker/Taker 费率；交易对的费率档、参考符号与参考倍数；合约的费率档与风险阶梯（也就是各档的最高杠杆与维持保证金率）。新增的交易对、合约与费率档处于「准备中」或无人使用，不算（开放它是一次状态修改）；所以后台新建的交易对与合约只能是「准备中」，文档里写了别的状态直接拒绝（422 `ADMIN_NEW_ITEM_NOT_PREPARE`，部署同步的 `exchangectl instruments apply` 不受限）。资产、网络与交易对的其它字段照旧由 OPERATOR 改、立即生效。
- **只有 ADMIN**：权限 `instruments.trading`。OPERATOR 可以预览，确认按钮不可用（服务端 403）。
- **服务端二次确认**：预览（`POST /admin/v1/instruments/preview`，或状态的 `POST /admin/v1/instruments/pairs/{symbol}/status/preview`、`POST /admin/v1/derivatives/contracts/{symbol}/status/preview`）返回 `confirmation`：用 `ADMIN_SECRET_KEY` 封装的令牌，绑定这位管理员与这次修改的全部变化（各项的修改前后与版本），10 分钟有效。提交时带回它；没有、过期（`reason: expired`）或预览后数据变了（`reason: changed`）都返回 409 `ADMIN_CONFIRMATION_REQUIRED`，页面会让人重新预览。一个令牌只确认一次修改（C5.5 ⑩，修改行记着令牌的哈希 `confirmation_hash`，迁移 admin 00009）：同一令牌再提交（例如网络重试）返回它当初确认的那条修改，不会再建一条；那条修改已经生效、失败或取消了也一样，按它现在的样子返回（C5.5 ⑲）。
- **延迟生效**：确认后记为一条「待生效修改」（表 `instrument_changes`），设置里的等待时间（`change_delay_seconds`，默认 300 秒，最多 86400）之后由 admin-service 每 5 秒一轮执行，以提交人的名义写入 instrument-service；到点时再做一次空跑，与确认时不一致（期间有人改过同一项、状态已变）就记为失败（「请重新预览」），不会套用过时的整项。
  - **等待时间的下限**（C5.5 ⑩）：一位 ADMIN 不能把等待时间改到下限以下。下限由 admin-service 的配置 `ADMIN_CHANGE_DELAY_FLOOR` 决定，默认 10 分钟；测试服 compose 里设 60 秒，供 e2e 用。存着的值低于下限时，按下限等待。设置接口返回 `change_delay_floor_seconds`，设置页按它校验。
  - **执行不在后台的事务里**（C5.5 ⑩）：每一轮先认领到点的修改（`applying_at`），再调用 instrument-service，最后单独记结果与审计。认领期间这条修改不能取消（409 `ADMIN_CHANGE_APPLYING`，列表上标「正在生效」）。
  - 服务暂时不可用或出错（含 gRPC Internal/Unknown）时，修改留到下一轮，结果栏写原因。还没调用就失败的，放开认领（可以取消），等满 1 小时仍不行就记为失败。已经调用、但答复丢了的，保持认领；1 分钟后另一轮核对：已经生效就记为已生效，结果带「(in effect already)」；还没生效就照常执行。记结果失败时（日志 `instruments: change applied, unrecorded`），下一轮同样这样核对。认领后答复丢了的，之后各轮即使服务不可用也一直保持认领，不能取消，也不会因满 1 小时记为失败，直到核对出结果（C5.5 ⑲）。`Due` 跳过一分钟内认领的修改；现在只有一个 admin-service 实例、各轮串行，多实例且一轮超过一分钟时可能重选，到那时再改。
  - 风险阶梯到点时再量一次影响：量不了（没有新鲜标记价、服务不可用）就等下一轮；会被强平的仓位比确认时多，就记为失败（`ADMIN_IMPACT_GREW`），请重新预览。按个数严格比较，不看名义价值、没有容差：等待期间标记价一动、多一个账户会被强平就失败，行情波动时收紧阶梯可能要反复预览（协调会话决定先按规格保留，C5.5 ⑲）。
  - `admin.two_person_approval` 打开时先等另一位 ADMIN 批准（`POST /admin/v1/instruments/changes/{id}/decide`，不能批准自己的），批准后再等同样的时间。生效前任何 ADMIN 都可以取消（`…/cancel`）。审计：`admin.instruments.change_requested`、`change_approved`、`change_rejected`、`change_canceled`、`change_applied`、`change_failed`（对象为 `instruments`、`pair:<symbol>`、`contract:<symbol>` 或按币的 `coin:<币>`）。
- **暂停是急刹车**：改为 HALT 不需要确认令牌、立即生效（仍只有 ADMIN；审计 `admin.instruments.pair_status` 或 `admin.instruments.contract_status`，详情 `{from, to}`，合约服务约一分钟内按新状态处理）；恢复交易、只撤单、下线都按上面等待。暂停只拒绝新单，不撤用户挂单（需求：HALT 允许撤单）；HOUSE 收到 `instrument.events` 后立即撤出它的参考簿。状态预览里带这个交易对或合约现有的挂单数 `open_orders`（读模型 `orders_current`，几秒前，现货与合约同一张表；读不到为 null），确认框里显示。模拟市场的币对或永续正被进行中的停牌事件暂停时，恢复交易的确认框提示：交易对与永续会在 10 秒内被再次暂停，要先在「事件日程」结束那个事件（预览时读 `GET /admin/v1/sim`，只在确有进行中的 HALT 事件时提示，㉔）。
- **风险阶梯的影响**：预览里列出按新阶梯会被强平的仓位数、名义价值与账户数，另有新进入预警、超出其杠杆风险限额的数量（derivatives-service 的 `POST /internal/derivatives/contracts/{symbol}/tier-impact`，按保证金监控的同一规则计算：逐仓看仓位，全仓看整个账户；HOUSE 不计）。算不出来时提示 `IMPACT_UNKNOWN`，有仓位没量到（没有新鲜标记价，或全仓账户太多：derivatives-service 最多量 2000 个、8 秒，剩下的计为未测量）时提示 `IMPACT_UNMEASURED`（详情为个数），两种都不给确认令牌（C5.5 ⑩）。影响只给能确认的 ADMIN 算，OPERATOR 的预览不算。
- **文档里的状态不生效**：文档给已有交易对或合约写了和现状不同的 `status`，预览与应用都提示 `STATUS_IGNORED`（详情为文档里的状态）；状态只能经「状态修改」改变（C5.5 ⑩）。
- **参考符号**：HOUSE 正在报价（在 `market.house_liquidity` 的名单上）或有永续合约以它为指数的交易对，清空参考符号直接拒绝（422 `ADMIN_REFERENCE_IN_USE`，详情 `used_by`）。
- **页面**：「资产与交易对」的「待生效修改」标签（状态筛选、修改内容、提交人、生效时间与批准人、结果；ADMIN 可批准、驳回、取消），顶栏待办与概览也计数（`todo.instrument_changes`）。修改的预览里，交易参数单独列出，并说明多久后生效或需要谁批准；状态「操作」先向服务端预览再确认。接口 `GET /admin/v1/instruments/changes`、`POST /admin/v1/instruments/pairs/{symbol}/status`、`POST /admin/v1/derivatives/contracts/{symbol}/status`。
- **按币关闭与重新开放合约**（币本位设计 2026-10-06 §3.5，协调会话 22:50，A63 第二部分）：「合约」页的「按币」标签按币（合约的基础资产）列出未下线的 U 本位与币本位合约及其状态。关闭：该币交易中或暂停的合约一起改为 `CANCEL_ONLY`（只减仓；HOUSE 继续报价，持仓可平；`market.house_liquidity` 的名单不动，下线时再去掉，后台不改开关规则）；重新开放：`CANCEL_ONLY` 的回到 `TRADING`（暂停的要单独恢复，准备中的单独开放）；下线仍逐个合约操作。接口 `POST /admin/v1/derivatives/coins/{coin}/status/preview`（`{"to":"CANCEL_ONLY"|"TRADING"}`，列出要改的与不变的合约、一个确认令牌；没有可改的 409 `INSTRUMENT_STATUS_TRANSITION_INVALID`，没有这个币的合约 404）与 `POST /admin/v1/derivatives/coins/{coin}/status`（带令牌与理由，202）：一条 `COIN_CONTRACTS_STATUS` 修改（对象 `coin:<币>`，迁移 admin 00015 放开种类约束，`summary.params` 逐个列出合约的改动），同样的等待与双人批准、可取消。到点时先核对全部合约仍在确认时的状态（有一个被别处改过就整条失败，什么都不改，请重新预览；已在目标状态的算已生效），再逐个改，每个记审计 `admin.instruments.contract_status`（对象 `contract:<代码>`，详情带 `change_id` 与 `coin`）；暂时失败的留到下一轮（已改的届时算已生效），被拒绝的继续改其余的，结果栏逐个写明，整条记为失败。
- **运维**：`exchangectl instruments apply`（部署同步）与功能开关的自动暂停（行情断流、模拟市场心跳）不经过这里。

### 资产资料与图标

资产与交易对 → 资产 → 打开一个资产，「资料与图标」（C4c）：显示名（2–32 字符，留空用资产名称）、简体、繁体与英文简介（各 1,000 字以内；繁体可留空，显示简体，G7b）、链接（官网、区块浏览器、白皮书，https）与图标（PNG、SVG 或 WebP，正方形，200 KB 以内；页面先检查类型、大小与正方形并预览，instrument-service 再查一遍，SVG 按白名单重建）。保存要理由，确认词为资产代码的小写；审计 `admin.instruments.profile_updated`，对象 `asset:<代码>`，图标只记类型与大小。接口 `GET/PUT /admin/v1/assets/{code}/profile`（读要 `instruments.read`，改要 `instruments.write`）。新图标有新的地址（`/v1/market/assets/{code}/logo?v=<版本>`），三个站点 1 分钟内显示；后台域名也转发这个地址（nginx `site-admin.conf`），在后台的 CSP 下同源显示。

## 模拟市场

（ASTRA 设计 §6，C5）侧栏「模拟市场」五页，接 market-sim 的内部接口（`MARKET_SIM_URL`，见 [market-sim.md](market-sim.md)）。所有管理员可看（`reports.read`）；价格事件与参数要 `sim.control`（ADMIN、OPERATOR）。价格控制页另有任意币种的价格事件（J3，见下）。

- **概览**（`/sim`，`GET /admin/v1/sim`）：目标价与最近成交价、价格带（锚点、报价中心、走价中、离锚点几个带宽）、集群状态（开关、运行、参考价、永续、看门狗）、机器人库存与永续净仓位、近 24 小时目标价与成交价曲线（`GET /admin/v1/sim/history`，每 10 秒一个点），进行中的事件有横幅（进行中的阈值目标另显示相对计划的偏离、是否可能到不了）。
- **价格控制**（`/sim/control`）：八种事件（瞬时涨跌、目标价、插针、趋势、波动率、暂停、停牌、重新锚定）与全部参数（`POST /admin/v1/sim/events`、`PUT /admin/v1/sim/params`）。
  - 确认框显示按目标价估算的永续影响：多空仓位数、会被强平的仓位数与名义、穿仓额（`POST /admin/v1/sim/impact`，derivatives-service 的 `/internal/derivatives/contracts/{symbol}/price-impact` 按强平监控的同一规则计算，不改任何东西）。
  - **阈值目标与插针**（ASTRA 设计 §3、§6.2，A6；规则由 market-sim 定，见 [market-sim.md](market-sim.md)「价格事件」）：
    - 目标价是「N 分钟内到 ≥/≤ X」的表单：方向（自动按水平在当前目标价之上还是之下推断）、水平、分钟数（1–1440）、越过之后（跟随市场，或在水平上保持若干分钟），以及插针列表（开始后第几分钟、幅度 %、宽度秒）。插针的分钟数在提交时换成绝对时刻（开始时间加分钟数），必须在收口期之前（窗口最后 10%，至少 1 分钟，表单上写明从第几分钟起）。没填开始时间时以服务端的时间为准：预览的应答带服务端的 `now`，表单据此算出与浏览器时钟的差（㉙；浏览器慢了会被拒、快了会比显示的晚触发）。事件最多提前 24 小时安排：预览与创建都拒绝更远的开始时间（400 `ADMIN_SIM_TOO_FAR_AHEAD`，表单先就地提示）。
    - 表单下方是计划预览（`GET /admin/v1/sim/target-preview?price=&duration_seconds=&direction=&starts_at=`，`reports.read`，原样转 market-sim）：能否按时到达（到不了时给出最短分钟数）、从当前目标价起的幅度、是否超出单人份额，以及每分钟的计划价与上下限、水平和插针的位置。
    - 后台自己的检查（其余交给 market-sim）：方向只能 `ABOVE`/`BELOW`、`then` 只能 `FOLLOW`/`HOLD`，它们与插针列表只属于目标价；插针幅度非零且不超过 ±10%、宽度不超过 60 秒、时刻在将来（最多 50 个）；预览的窗口 60 秒到一天。market-sim 的 `SIM_TARGET_INFEASIBLE`（`details.min_duration_seconds`）与 `SIM_SPIKE_BEYOND_BAND`（`details.max`）在提示里带上这个数。
    - 插针的确认框按标记价估算强平：标记价跟随指数，指数是现货 60 秒 TWAP 与盘口中价的平均（中价在最近成交 1% 以内时，market-data-service 的 `PlatformPrice`），所以针尖时标记价约移动插针幅度的一半（`max((1 + 1.5/60)/2, (1.5 + 宽度/2)/60)`，宽度 20 秒时 51%），影响按那个价格算。设计稿写的是"按 TWAP 估算"（只按 TWAP 宽 20 秒的针只有约 19%），A4 审查后指数加入了中价，后台按实际规则估算。带插针的目标另列出各插针处（从当前目标价与水平中较不利的一侧起算）的影响。
    - 有进行中或排队的目标时，页面顶部有横幅：目标的文字、计划包络与实际目标价、成交价（`GET /admin/v1/sim/events/{id}/plan` 每 5 秒、`/history` 每 15 秒）、相对计划的偏离（`ln(目标价/计划价)` 折成百分比）与"可能到不了"（market-sim 的 `at_risk`），可直接结束（越过前结束记为已取消，排队的插针一起取消）。目标在跑时新建跳涨或趋势会被 market-sim 拒绝（409 `SIM_TARGET_RUNNING`）。
  - market-sim 管单人份额（单次 30%、任一小时合计 50%，按事件开始时间计；参数里 `p0`、`max_minute_move`、`floor`、`ceiling`、`daily_volume` 按影响计入）。超出时它返回 `SIM_EVENT_NEEDS_APPROVAL` / `SIM_PARAMS_NEED_APPROVAL`，后台把这次改动存成资金操作（`SIM_EVENT`、`SIM_PARAMS`，`escalation` 为 `SIM_SHARE`，`payload.move` 是 market-sim 估算的幅度），接口返回 202。
  - 另一位有 `sim.control` 的管理员在「审批」里批准后，admin-service 用自己的键（`admin`，`SIM_ADMIN_API_SECRET`，在服务器 `sim/admin.env`）签名调用 market-sim：`actor` 是申请人、`approved_by` 是批准人，两个名字都取自后台会话，不取自浏览器。批准人以当前登录的会话为准，不再要身份验证器。申请人不能批准自己的（`ADMIN_SELF_APPROVAL`）。
  - 这类申请一天未决、或事件到了开始时间、或目标的第一个插针到了时间就过期（market-sim 不收已过时刻的插针，A6）：批准过期的只会记为失败（结果 `expired at <时间>`），不发给 market-sim；列表按服务端时钟标「已过期」（审批列表的 `expired`，⑭）。开始时间已过的事件按"立即开始"处理（`starts_at` 去掉），申请因此一天后才过期，不会一建就过期（⑭）；原填的时间记在审计详情与申请的 `payload.asked_starts_at` 里（㉘）。批准框里有按现在的目标价重新测的幅度（与申请时 market-sim 的估算并列）和对永续的影响（`GET /admin/v1/approvals/{id}/sim-preview`；跳涨、插针（针尖，影响按上面的标记价估算）、目标价事件与锚定价修改能直接算出价格，其它只显示申请时的估算），目标的插针逐个列出；带插针的目标另按两个方向最深的插针算出标记价与影响（`spike_impacts`，与创建者确认框的算法相同，㉙）。
  - 没有 `SIM_ADMIN_API_SECRET` 时 admin-service 启动时告警，模拟市场在后台只读。
- **任意币种价格事件**（`/sim/control` 顶部的卡片；设计 [通用价格控制](../设计-通用价格控制-2026-10-07.md) §4，J3；market-sim 一侧见 [market-sim.md](market-sim.md)「任意交易对的价格事件」）：
  - 表单：交易对可多选（搜索框，最多 10 个；选项是跟随币安、正在交易的交易对，以及平台币的交易对，后者标「按模型跳变」）；目标按百分比（相对币安价，±90% 以内、不为 0）或按价格（只能选一个交易对，同样不超过平台现价的 ±90%，A81）；上升、保持、恢复三个秒数（上升 ≥ 1、恢复 ≥ 3、合计 ≤ 600）；开始时间可选；「不连带合约与杠杆」复选框默认不勾（即连带，用户 2026-10-07 03:0x 决定），下面常驻提示：连带时永续的指数价与标记价、杠杆估值跟着变，会像真实行情一样触发预警、强平与资金费变化；勾选后提示现货价与合约标记价会不一致。每个选中的交易对显示平台现价与目标价、幅度（`GET /admin/v1/sim/prices`，`reports.read`，market-data 的行情最新价，跟随币安的就是币安价；每 5 秒）；选了平台币时注明它按价格模型跳变（`JUMP`，用时为上升秒数，保持、恢复与连带不适用）。
  - 提交 `POST /admin/v1/sim/events`（`type: OVERLAY`，`symbols`、`target_pct` 或 `target_price`、`ramp_up_seconds`、`hold_seconds`、`ramp_down_seconds`、`risk`），确认框列出各交易对的现价 → 目标与三段秒数、是否连带，确认词 `overlay`。后台先查表单（交易对 1–10 个且不重复、目标二选一且按价格时只有一个交易对、秒数下限与合计 600 秒；这些字段只属于 `OVERLAY`，别的事件带上就 400），其余由 market-sim 判定。一次请求的事件全部建成时答 201 `{items}`（每个交易对一项：事件 ID、类型、状态、目标乘数、起点价），提示「已生效：N 个事件」；超出单人份额（按交易对：单次 30%、任意一小时 50%）答 202，存成 `SIM_EVENT` 申请，`payload.symbol` 是超出的交易对、`payload.move` 是幅度，批准后以申请人名义、带批准人发给 market-sim，结果写 `events <交易对> <事件 ID>, …`；批准框不按平台币的模型测算，逐个交易对列出平台现价、目标价、幅度与 HOUSE 估算的最坏损失（`sim-preview` 的 `overlay`，A81；算法同 market-sim 开始前的估算：market-maker `GET /internal/house/rooms/{symbol}` 的可买或可卖数量 × 单位价值 × 乘数偏离，连带时加上 X-USDT-PERP 与 X-USD-PERP；HOUSE 不报价或读不到时注明），并注明目标乘数在执行时按当时的币安价算、超过上限由 market-sim 拒绝。market-sim 的拒绝在表单里带上交易对与数字：`SIM_OVERLAY_LOSS_CAP`（HOUSE 估算最坏损失与上限，或读不到额度的原因）、`SIM_OVERLAY_RUNNING`（该交易对已有事件）、`SIM_OVERLAY_TOO_FAR`、`SIM_NOT_OVERLAYABLE`；开关 `market.overlay` 关着时 403 `SIM_OVERLAY_OFF`。
  - 卡片上方列出进行中与排队的价格事件（每 2 秒）：状态、事件文字、进度条与实时乘数，「立即恢复」（`POST /admin/v1/sim/events/{id}/end`：乘数 3 秒内线性回到 1，结果记为已取消）；排队的可取消。「事件日程」里的价格事件同样显示进度与乘数，以及起点（币安）、峰值（平台）、结束时的币安价与平台价。审计 `admin.sim.event_created` 的详情是 `items`。
  - 测试：admin.sh 的「price events on any pair」一段（表单校验、FINANCE 无权、35% 进入审批并被拒、BTC-USDT +0.5% 不连带的事件跑起来、同交易对第二个 409、立即恢复后 DONE 且记下结束时的币安价；期间打开 `market.overlay`，结束后放回原样）；后台冒烟在价格控制页选 BTC-USDT、检查连带提示与默认不勾、打开确认框后取消。
- **事件日程**（`/sim/events`）：排队、进行中与结束的事件，发起人与批准人；排队的可取消，进行中的可结束（结束停牌即恢复交易），都要理由（`POST /admin/v1/sim/events/{id}/end`）。目标价事件显示结果（已达到 `HIT`、未达到 `MISSED`、已取消 `CANCELED`）、越过时刻与到期时刻，「计划」打开抽屉看计划包络与实际走势（结束的也能看，market-sim 只留最近 200 个事件）；目标的插针标出属于哪个目标。
- **机器人集群**（`/sim/bots`）：
  - 集群开关：`sim.enabled`、`sim.perp`、`sim.events`、`sim.halt_on_loss`，经功能开关接口切换（`flags.write`，规则保留）。market-sim 没有单个机器人的启停接口，开关作用于整个集群。
  - 各角色的数量与库存合计、每个机器人的余额、永续仓位、最近一次被拒与重试时间，可按角色与"只看被拒"筛选；参数只读，到价格控制修改。
  - 单个机器人的余额用「资金调整」修改（行上的「调整余额」带上它的用户 ID）。机器人之间不划转（用户 2026-10-03 决定），要挪就做两笔调整。
  - **补充库存**：资金操作 `SIM_MINT`（`POST /admin/v1/sim/mint`，要 `ledger.adjust.request`；批准要 `ledger.adjust.approve`）。总数平均分给全部或某个角色的机器人（两位小数，余数给第一个），每个机器人一笔现货账户的人工调整，幂等键 `approval:<id>:<用户>`，重试只补没记上的；单人模式的限额与双人模式同其它资金操作，折合按币的 USDT 交易对最新价。单次最多 10,000,000 个币或 1,000,000 USDT，谁批准都不能越过（422 `ADMIN_SIM_MINT_CAP`）。第一个机器人就被拒时记为失败；记上几个之后被拒时保持待处理（计入申请人的 24 小时累计，错误的详情有 `approval_id`、`bot`、`booked`、`of`），排除原因后在「审批」里完成，幂等键只补剩下的。`payload.bots` 是每个机器人的份额（JSON）。审计 `admin.sim.mint_requested/executed/failed/approved/rejected`，对象 `sim`；账本另记每笔 `ledger.manual_adjustment`。运维脚本 `scripts/ops/astra.sh mint` 仍可用（不经审批）。
- **代币信息**（`/sim/token`）：币的资料（与资产抽屉里的同一编辑器，要 `instruments.write`），以及持有分布（`GET /admin/v1/sim/token`）：发行总量（`ADJUSTMENT` 账户的借方）、市值（按最近成交价）、机器人与用户各自持有多少、多少个账户持有、平台账户（手续费等）与最大的 20 个持有者。数据来自 ClickHouse 的账本流水（`ledger_entries FINAL`），晚几秒；机器人名单来自 market-sim。
- **审计**：对象 `sim`，动作 `admin.sim.event_created/event_ended/params_changed`，审批的 `admin.sim.event_requested/approved/rejected/failed`、`admin.sim.params_*`、`admin.sim.mint_*`；market-sim 自己另记 `market.sim.*`（含 `approved_by`）。

## 杠杆

（杠杆交易设计 2026-10-06 §8，批次 E5）侧栏「杠杆」四页，开关 `margin.enabled` 关着时不显示、地址回到概览。admin-service 经 margin-service 的内部接口读写条款与账户（`MARGIN_SERVICE_URL`，`/internal/margin/*`，只在 compose 网络：网关与 nginx 不转发，带 `X-User-Id` 的请求被拒；写操作带 `X-Admin-Id`，即管理员邮箱，margin-service 记为条款的 `updated_by` 与账户的 `frozen_by`）；强平记录与利息报表来自 ClickHouse 读模型。权限沿用现有：读参数 `instruments.read`、读账户 `derivatives.read`、读强平与利息 `reports.read`；改参数 `instruments.trading`（仅 ADMIN），冻结与手工强平 `derivatives.write`（ADMIN、OPERATOR）。

- **杠杆参数**（`/margin/params`）：资产（`GET /admin/v1/margin/assets`）、交易对的逐仓条款（`/margin/pairs`）、全仓条款与各逐仓倍数的建议阈值（`/margin/settings`）。修改（`PUT …/assets/{asset}`、`…/pairs/{symbol}`、`…/settings`）带读到的版本（`expected_version`）与理由，每个字段都要给：
  - 只停止新借款的改动立即生效（200，审计 `admin.margin.asset_changed`、`admin.margin.pair_changed`，带前后值与改动的字段）：关闭借币、调低池上限或单用户上限、关闭逐仓。已有借款不受影响。
  - 其余一律等第二位有 `instruments.trading` 的 ADMIN（202，审批 `MARGIN_PARAMS`，升级原因 `MARGIN_RISK`，单人与双人模式都一样，协调会话 2026-10-06 03:24 ② 的严版）：计息方式与利率、折扣、保证金资格（两个方向）、开启借币、调高上限、倍数、阈值、强平费、开启逐仓；混在一起的整体等批准；全仓条款的任何改动都等批准。
  - 同一对象（资产、交易对、全仓条款）对同一版本一次只有一条待审，不论谁申请（409 `ADMIN_MARGIN_CHANGE_PENDING`，详情 `approval_id`）；停止新借款的改动不受它挡。版本已变时 409 `MARGIN_PARAMS_CHANGED`，批准时遇到则申请失败。一天未决即过期（批准只记失败）。
  - 批准后 margin-service 以申请人的名义设置（`updated_by` 是申请人，批准人在申请与审计里）。应答丢失后再批准时，条款若已在下一版本、由申请人设置且与申请一致，按"已由前一次设置"记为执行；读不到条款时申请保持待处理。
  - 倍数集合（逐仓 3/5/10、全仓 3/5）只在 margin-service 定义与校验（批准时不合规则申请失败），后台不重复；后台在提交审批前只查倍数在 2–10 之间（条款表的列约束，A58），以及阈值、强平费、折扣、利率的范围与小数位（400），没有改动也是 400。页面改倍数不改动已填的阈值，只给出该倍数的建议阈值供采用（A57）。
  - 列表与全仓条款上有待审申请时显示「待审批」（`pending_approval_id`，点进审批页）。
- **杠杆账户**（`/margin/accounts`）：一次查询（类型、交易对、用户交给服务端，状态页签在返回的列表上筛），按风险率从低到高、最多 500 个（`truncated` 提示还有更多；margin-service 最多扫 5000 个账户，逐个用户取持仓估值，账户多时一次约 10 秒）；`frozen_reason` 不是管理员冻结时为空串；每个账户带待审的手工强平（`pending_approval_id`）。详情（`GET …/accounts/{user_id}/{account}`，`account` 为 `MARGIN_CROSS` 或 `MARGIN_ISOLATED:<交易对>`）：余额与估值、借款、借还流水（`journal_key` 是账本分录的幂等键，如 `margin-interest:<资产>:<整点 Unix 秒>`）、计息、最近 20 次强平（margin-service 自己的记录，带步骤与说明；`SHORTFALL` 表示保险基金缺某个资产，负债保留到补足后的下一轮）。
  - 冻结与解冻：`POST …/freeze`、`…/unfreeze`（带理由，冻结理由最多 500 字节），立即生效，审计 `admin.margin.account_frozen`、`admin.margin.account_unfrozen`（对象 `user:<id>`，详情有账户）。冻结时 margin-service 撤销账户的全部挂单（E3；撤单失败只记日志、冻结照样生效，可再撤），冻结后不能下单、借币或划出，可以还款，利息照常计、到强平线仍会强平。已冻结或强平中 409 `MARGIN_FROZEN`；同一管理员以同样理由再冻结（应答丢失后的重试）返回账户并补记审计。不是管理员冻结的（强平中）不能解冻：409 `MARGIN_NOT_FROZEN`。
  - 手工强平：`POST …/liquidate`（带 `Idempotency-Key`），一律等第二位有 `derivatives.write` 的管理员（202，审批 `MARGIN_LIQUIDATE`，`value_usdt` 为总负债，载荷保留申请时的状态、风险率与资产负债）。只有 margin-service 在强平时（开关 `margin.liquidation`，按账户所属用户判断，与 margin-service 的监控相同；它的手工强平路径本身不查开关）才能申请和执行：申请时关着 409 `ADMIN_MARGIN_LIQUIDATION_OFF`（开关整个关着时页面按钮置灰并说明，按规则打开时以服务端对该用户的判断为准）；批准时关着，申请记为失败（结果为 `ADMIN_MARGIN_LIQUIDATION_OFF`），不发给 margin-service；没有负债 409 `ADMIN_MARGIN_NOTHING_OWED`；强平中 409 `MARGIN_FROZEN`；同一账户一次一条待审。批准后 margin-service 以申请人的名义、按审批 ID 只启动一次（`POST /internal/margin/accounts/{user}/{account}/liquidate`，请求体只有 `approval_id`，触发方式 `MANUAL`），申请结果写强平 ID；一天未决即过期。
- **杠杆强平**（`/margin/liquidations`）：读模型 `margin_liquidations`（按 `liquidation_id` 合并开始与完成两条事件，`anyLast` 跳过空值），近 1/7/30/90 天，按类型、交易对、触发方式、用户筛选，游标分页。只见到完成事件的行没有开始的字段，ClickHouse 00010 之前的行没有触发方式与审批号，页面显示「—」。
- **利息报表**（`/margin/interest`）：按天、周或月与资产：计息与已还（账本 `ledger_entries` 的全仓/逐仓利息行：计息使其减少、还款使其增加）、期末未还（加上期初以前的累计）、平均本金与小时利率、计息账户数（`margin_interest` 的逐小时计息），折合 USDT 按该资产 USDT 交易对在桶内最后一笔成交价（没有则为空，USDT 按 1）。
- **上线检查清单**：`margin` 项（见「上线检查清单」的表）——开关关着为达标；开着时要求 `margin.liquidation` 也开着，且 `margin.enabled` 与 `margin.auto_borrow` 都不对所有人全局打开。测试服三个杠杆开关自 2026-10-06 13:25 起对所有人打开，所以这一项在测试服为未达标；上线前三个开关回到按用户或地区的规则。
- **审批页**：`MARGIN_PARAMS` 显示对象与每个改动字段的前后值，`MARGIN_LIQUIDATE` 显示用户、账户与申请时的风险率、负债；决定分别要 `instruments.trading` 与 `derivatives.write`；一天后标「已过期」。（`HOUSE_CAPS` 见「HOUSE 敞口」的额度一条。）
- **审计**：立即生效的 `admin.margin.asset_changed`、`admin.margin.pair_changed`（对象 `margin:asset:<资产>`、`margin:pair:<交易对>`）；申请与决定 `admin.margin.params_requested/approved/rejected/failed`（对象 `margin:<目标>`，目标为 `asset:<资产>`、`pair:<交易对>` 或 `cross`）与 `admin.margin.liquidation_requested/approved/rejected/failed`（对象 `user:<id>`）；冻结与解冻见上。

## 风控

### 功能开关

`/risk`：列出全部已知开关（从未设置的显示为关闭、版本 0；说明、规则摘要、最后修改人），切换启用状态要理由与确认（`flags.write`；`GET /admin/v1/flags`、`PUT /admin/v1/flags/{key}`，审计对象 `flag:<key>`，含前后值）。地区、账户状态、白名单等规则保持不变（改规则用 `exchangectl flags set`，见 [feature-flags.md](feature-flags.md)）。服务 5 秒内生效。`admin.*` 开关要 `settings.write`（见「角色与权限」）。

## 运营

（C4b）存放在 notification-service（迁移 notify 00002：`articles`、`article_texts`、`broadcasts`；00003、00004 加了群发的轮次、邮件队列与留存）。admin-service 经它的内部接口 `/internal/notification/{articles,broadcasts}` 读写（`NOTIFICATION_SERVICE_URL`），检查权限、要理由并审计；notification-service 把后台管理员记为修改人。

### 公告与帮助

`/announcements`、`/help-articles`；任何管理员可读，写要 `content.write`（ADMIN 与 OPERATOR）。

- 一篇文章 = 栏目、slug（小写字母、数字、连字符）、适用模式、分类、置顶（公告）、排序（帮助）与简体、繁体、英文三种文本（简体必填；繁体与英文可选，写了就要标题与正文都写；没有该语言时读者看简体，G7b）。固定页面的默认稿也有繁体（由简体生成的 `*.zh-TW.md`），「复制默认稿」三种一起带上。
- 适用模式（设计 2026-10-04 §4.4，D4）：`TEST`（测试模式）、`FORMAL`（正式模式）或 `BOTH`（通用，新建时的默认；修改时不填保持原样）。平台资料的测试模式开着时站点只出 TEST 与 BOTH 的文章，关掉后只出 FORMAL 与 BOTH，切换模式即自动换稿。同一栏目的一个 slug 可以并存一篇 TEST 与一篇 FORMAL，或只有一篇 BOTH，重叠的返回 409 `NOTIFY_ARTICLE_EXISTS`。编辑器顶部选模式，列表里测试稿与正式稿带标记；列表上方可按模式筛选（全部、测试模式、正式模式、通用，在地址栏 `?modes=`，A43 ⑪），「站点自带的文章」也标出各文件 front matter 的模式并跟着筛选。正文里 `:::test` 与 `:::` 之间的段落只在测试模式显示，`:::formal` 与 `:::` 之间的只在正式模式显示（站点渲染时过滤，服务端原样保存）。正文是 Markdown，编辑器可切「预览」，按站点的排版显示，站内链接指向用户站；通用稿的预览先按平台现在的模式显示（读平台资料的测试模式，A43 ⑫），可切换。
- 状态：草稿 → 发布（立即，或填一个时间定时发布）→ 下线（可再发布）。每次保存带读到的版本，期间别人改过返回 409 `COMMON_CONFLICT`（重新打开再改）。审计 `admin.content.created`、`updated`、`published`、`archived`，对象 `announcement:<slug>` 或 `help:<slug>`，详情带 `modes`（同一 slug 可有测试稿与正式稿）。
- 站点读取：公开接口 `GET /v1/announcements[/{slug}]`、`GET /v1/help[/{slug}]`（`?locale=en`，`Cache-Control: public, max-age=15`）。列表分页（`limit` 默认 20、最多 100，`cursor` 用上一页的 `next_cursor`），只回摘要：没写摘要的从正文前 4,000 个字符里取第一段，列表从不读整篇正文（C5.5 ⑫）；两个站点读第一页 100 篇，更早的仍可凭链接打开。两个站点把接口里的文章叠加在仓库自带的 Markdown 之上，同 slug 以接口为准（与设计 §4.5"替代仓库内 Markdown"的偏差，见设计稿 §0）；页面数据 30 秒内视为新鲜、45 秒重取一次，所以发布、修改、下线都在 1 分钟内到达两端。接口不可用时只显示自带文章。
- 下线的文章：列表接口的 `withdrawn` 带上它的 slug，单篇返回 404 `NOTIFY_ARTICLE_WITHDRAWN`，站点因此连同同 slug 的自带文章一起隐藏（未发布的草稿不影响自带文章）。
- 页面下方「站点自带的文章」列出还没被后台接管的仓库文件，「复制到后台编辑」把中英文本带进编辑器，保存为草稿、发布后替换原文件。
- 接口：`GET/POST /admin/v1/articles`（`?section=ANNOUNCEMENT|HELP|LEGAL|HOME`）、`GET/PUT /admin/v1/articles/{id}`、`POST …/{id}/publish`（`{version, publish_at?, reason}`）、`POST …/{id}/archive`。

### 固定页面（法律页与首页横幅）

`/pages`（设计 2026-10-04 §4.4，D2）；任何管理员可读，写要 `content.write`。栏目 `LEGAL` 只有 `terms`、`privacy`、`risk`、`fees`、`about`、`contact` 六个 slug（站点地址 `/legal/<slug>`），`HOME` 只有 `home-hero`（首页横幅：标题、副标题即摘要，按钮取正文里第一个 Markdown 链接，只认站内路径或 https 地址）；别的 slug 被 notification-service 拒绝（400）。站点自带每一页的中英文默认稿（`web/packages/core/content/{legal,home}/`）。

- 每一页两栏（D4）：测试模式与正式模式下站点各显示什么——覆盖稿（已发布且已到时间）、默认稿（没有覆盖稿、只有草稿，或覆盖稿定时发布、未到时间）、不显示（法律页的覆盖稿被撤回，默认稿也隐藏）、站点内置文字（首页横幅被撤回，用站点代码里的文字），以及用的是哪篇覆盖稿：那种模式的稿，没有时用通用稿（标「通用稿」），与它的状态、版本、最近修改时间。站点现在用的那一栏（平台资料的测试模式）在表头标「当前」，表格上方写明平台现在是哪种模式。
- 「编辑」：那一栏有覆盖稿时打开它（通用稿两栏都会打开同一篇），没有时「新建」从默认稿复制一份、模式预设为这一栏；slug 固定，没有分类、置顶与排序。保存是草稿，发布（立即或定时）后才在它的模式下替换默认稿。
- 「以默认稿发布」（正式模式一栏）：把默认稿（中英）原样发布为正式模式的覆盖稿；已有正式稿（草稿或已撤回）时先用默认稿改写再发布；已有的是通用稿时，它改为只用于正式模式并换成默认稿（测试模式下站点改显示默认稿），确认框写明。上线检查清单的「法律页」= 用户协议、隐私政策、风险提示在正式模式下都有已生效的覆盖稿（FORMAL 或 BOTH），这一步就是为它准备的（协调会话决定 ②，不引入新状态）。
- 「撤回」= 下线那一栏的覆盖稿：法律页在它的模式下从站点消失（默认稿一并隐藏），首页横幅改用站点内置文字；撤回通用稿两种模式都受影响（确认框写明）；要恢复就再发布或以默认稿发布。审计与公告、帮助相同（`admin.content.*`，对象 `legal:<slug>`、`home:<slug>`，详情带 `modes`）。

### 站内信

`/broadcasts`；任何管理员可读，发送要 `notices.send`（ADMIN 与 OPERATOR）。

- 对象：单个用户（用户 ID，确认词为 ID 后 4 位）、按标签（带这个标签的账户，最多 10,000 个，没有账户带它返回 422 `ADMIN_TAG_EMPTY`；确认词为标签的小写）、全体用户（确认词 `all`）。中文标题与正文必填，繁体与英文可选（各要标题与正文都写，没写的语言其读者收到简体，G7b）；跳转路径只能是站内路径（如 `/assets`，`//` 与反斜杠开头的拒绝）；可勾选同时发邮件。带 `Idempotency-Key`（消息 ID 取自它）。
- 送达：notification-service 分批写进每位用户的通知（类型 `BROADCAST`，实时推送到 `notifications` 频道），见 [accounts.md](accounts.md)「用户通知」。列表与详情显示对象人数、已收到、已读与完成时间，发送中每几秒刷新。发出后不能撤回。审计 `admin.notices.sent`，对象 `broadcast:<id>`。
- 失败（C5.5 ⑫）：某条消息的一轮出错（例如 user-service 不可用）只影响它自己，其它消息照常送达；它下一轮等 3 秒、6 秒……翻倍，最多 10 分钟，连续 10 轮（约半小时）失败后标为「发送失败」（`FAILED`）。列表标出「已连续失败 n 轮」，详情显示最近一次的错误与下一轮时间；排除原因后有 `notices.send` 的管理员在详情里「继续发送」（理由，确认词为 ID 后 4 位），从中断处接着发，已收到的用户不会再收到。审计 `admin.notices.resumed` 在请求 notification-service 之前写；对方拒绝（例如已不是 `FAILED`）时另记 `admin.notices.resume_failed` 与错误（C5.5 ㉓）。
- 邮件排队（C5.5 ⑫、㉓）：勾选了邮件的，每位用户一封进 `deliveries` 队列，按轮次重试（1、5、15、60 分钟，第 5 轮失败记 `FAILED`），超过 24 小时放弃；发出的不再重发。
- 留存（C5.5 ⑫）：通知与群发保留 180 天、投递记录 90 天，每批 5,000 行清理；配置的天数不能低于 7 天（notification-service 启动时校验）。
- 接口：`GET/POST /admin/v1/broadcasts`、`GET /admin/v1/broadcasts/{id}`（带 `failures`、`last_error`、`retry_at`）、`POST /admin/v1/broadcasts/{id}/resume`（`{reason}`，只有 `FAILED` 的能继续，其它 409 `COMMON_CONFLICT`）。

## 系统

### 管理员与角色

`/admins`（`admins.manage`，只有 ADMIN；C4a）：

- 列表显示角色、状态（停用、锁定到何时、连续失败次数）、最近登录、进行中的会话数。
- 「新建管理员」填邮箱、姓名、角色与理由，确认词为角色的小写代码；成功后弹窗显示**一次性设置链接**（见「登录与会话」），关闭后无法再看，经安全渠道交给本人。
- 行内「操作」：修改角色（下一个请求起生效）、重置口令（旧口令立即失效、结束对方全部会话，给设置链接）、重置身份验证器（旧的立即失效、会话结束，给设置链接）、查看会话（抽屉，可结束全部会话）、停用（会话立即结束）/启用（同时清除锁定与失败次数）。每项都要理由与确认词（ID 后 4 位），审计 `admin.created`、`admin.role_changed`、`admin.password_reset`、`admin.totp_reset`、`admin.sessions_revoked`、`admin.disabled`、`admin.enabled`，对象 `admin:<id>`。
- 下方「角色权限」矩阵只读（来自 `GET /admin/v1/roles`）。
- 规则：不能在这里改自己的账号（403 `ADMIN_SELF`；退出登录结束自己的会话，口令与身份验证器在「账号与安全」改）；最后一位启用的 ADMIN 不能被停用或降级（409 `ADMIN_LAST_ADMIN`；检查与修改在同一个事务里，事务先取咨询锁 `pg_advisory_xact_lock(7331001)`，两位 ADMIN 同时互相降级也会留下一位，C5.5 ⑪）。没有 ADMIN 能登录时仍用 `exchangectl admin create`。
- 接口：`GET /admin/v1/admins`、`POST /admin/v1/admins`（201，`{admin, setup: {token, kind, expires_at}}`）、`POST /admin/v1/admins/{id}/status`（`{enabled, reason}`）、`…/role`（`{role, reason}`）、`…/password-reset`、`…/totp-reset`（`{reason}`，返回 `{setup}`）、`GET …/sessions`（最多 50 个进行中的会话）、`POST …/sessions/revoke`（204）。
- 与设计稿 §5 的差别：管理员接口没有删除（停用即可，审计需要保留账号）。

### 系统健康

`/health`（`reports.read`，C4a）：

- `GET /admin/v1/health`：各服务运维端口 `/readyz` 的就绪状态与耗时（2 秒超时，并发）。目标默认是 compose 网络里的 18 个服务（2026-10-06 起含 margin-service），可用 `HEALTH_TARGETS`（`名称=http://主机:端口,...`）覆盖。
- `?details=true` 还读各服务的 `/metrics`：版本（`exchange_build_info` 的 `version`，部署后应全部一致，不一致时标出几个版本）、Kafka 消费滞后（`kafka_consumer_lag` 求和，> 1000 标黄）、启动以来转入死信的条数（`kafka_consumer_records_total{result="dlq"}`，> 0 标红，用 `exchangectl dlq` 查看与重放），以及行情源状态（market-data 的 `/internal/market/feed`）；没有 Kafka 消费者的服务这两列为空。
- 页面另有对账（每项检查最近一次）与托管方状态，每 15 秒刷新。
- 带 details 时还读 `exchange_config_present{item}`（各服务用到的第三方是否已配置：auth 的 `turnstile`、notification 的 `mail`、wallet 的 `alchemy`，只有是/否，不含值），供「上线检查清单」用（`config_present`）。

### 平台设置

`/platform`（读：所有角色，`reports.read`；改：`settings.write`，只有 ADMIN；设计 2026-10-04 §4.1、§4.2、§5，D2）：

- **平台资料**（instrument-service 的 `platform_profile`，经 `INSTRUMENT_SERVICE_URL` 的 `/internal/platform/profile`）：交易所名称与简称、域名（PC 站主机名，手机站 m.<域名>、后台 admin.<域名>）、浏览器主题色与品牌主色、默认语言、测试模式（开关；开着时三端顶部是否显示横幅与横幅的中英文文字，开着且显示横幅时文字必填；D4 起取代学习横幅 `learning_mode`）、注册方式（开放/关闭与关闭时的中英文文字）、页脚版权与备案合规文字（中英文）、联系邮箱、客服链接、社交链接（最多 10 个，https）。`GET/PUT /admin/v1/platform/profile`：保存整体替换（图片与注册赠送除外），带读到的 `expected_version`，期间有人保存过 409 `INSTRUMENT_PLATFORM_CHANGED`；三端 1 分钟内显示，不重新构建。单人即可，审计 `admin.platform.updated`（对象 `platform`，详情 `changes` 为改动字段的前后值与新版本）。
- **图片**：浅色与深色背景的 logo、favicon、Apple 触摸图标。`PUT/DELETE /admin/v1/platform/images/{kind}`（`logo_light`、`logo_dark`、`favicon`、`apple_touch_icon`）：正方形、最大 200 KB；logo 收 PNG、SVG、WebP，favicon 收 PNG、SVG，触摸图标只收 PNG 且至少 180 px（instrument-service 再查一遍，SVG 按允许名单重建）。上传前在确认框里预览；删除后恢复内置图片。审计 `admin.platform.image_updated`（类型、大小、SHA-256，不含内容）与 `admin.platform.image_removed`。图片地址带版本，nginx 让 admin.<域名> 同源代理 `/v1/platform/images/*`（后台的 CSP 不放第三方图片）。
- **注册赠送**（ledger-service 的设置，经 `LEDGER_SERVICE_URL` 的 `/internal/ledger/settings/welcome-credits`；页面标「上线应为 0」）：新账户注册时得到的资金列表（资产与数额），总闸开关 `ledger.welcome_credit` 也开着才发。`PUT /admin/v1/platform/welcome-credits` 带 `expected_version`（过期 409 `LEDGER_SETTINGS_CHANGED`）：
  - 降低或清空（数额 0 即不发，`[]` 全部不发）立即生效（200），审计 `admin.platform.welcome_changed`（新旧列表）；账本另记 `ledger.settings.welcome_credits`。
  - 提高任何一项、或从 0 变为非零，一律等另一位 ADMIN 批准（202，资金操作 `WELCOME_CREDIT`，模式 TWO_PERSON，`escalation` 为 `WELCOME_RAISE`，单人模式也一样）。提高部分按各资产 USDT 交易对的新鲜价格折算合计，每次最多 10,000 USDT，谁批准都不能越过（422 `ADMIN_WELCOME_RAISE_CAP`）；没有新鲜价格的资产不能提高（422 `ADMIN_WELCOME_UNPRICED`）。申请前按账本的规则先查一遍（资产代码 2–12 位大写字母与数字、资产存在、数额不超过资产的小数位，否则 400），不会等到批准时才失败（复审 ㉚）。同一位管理员对同一版本再提一次同样的提高，在第一条还在等批准时被拒（409 `ADMIN_WELCOME_RAISE_PENDING`，附那条申请的 ID；复审 ㉛）。数额全为 0 等于不赠送：后台去掉为 0 的项，交给账本的是空列表 `[]`（账本不收 0）。
  - 批准要 `settings.write`（ADMIN），不能批准自己的；批准时按申请时的版本设置，期间赠送被改过则这次申请失败（结果 `LEDGER_SETTINGS_CHANGED: …`），不会覆盖别人的修改。申请人可以撤回。申请一天内有效（折算用的是申请时的价格），过期后列表标「已过期」，批准只会记为失败（`expired at …`），不设置任何东西（复审 ㉚）。这类申请不记 `attempted_at`（重复设置会被版本拒绝），不计入单人模式的 24 小时累计。审计 `admin.platform.welcome_requested/approved/rejected/changed/failed`，对象 `platform`，详情有新旧列表、版本与折算金额。
  - 账本的应答丢失时（已设置、但后台没收到回复）申请仍是待批；再次批准时账本按版本返回 409，后台随即重读设置：版本正好是申请时的下一版、列表与申请一致、修改人是申请人，就是上一次的设置，记为已执行（结果带 `set by an earlier attempt`），否则记为失败（复审 ㉚）；重读本身失败时结果仍未知，申请保持待批（503，可再批准；复审 ㉛）。

### App 下载

设计稿 [设计-App下载页-2026-10-07.md](../设计-App下载页-2026-10-07.md)（§7 为 H0 契约），后端 H1，后台页面 H2（「平台设置 → App 下载」）。读：所有角色（`reports.read`）；改：`settings.write`（只有 ADMIN），单人，有审计（对象 `app:ANDROID` / `app:IOS`）。

- **页面**（`/platform/apps`，侧栏「系统 → App 下载」，H2）：顶部说明 OTA 只适用于企业签名或 Ad Hoc 签名的 .ipa、App Store 与 TestFlight 用外部链接；Android 与 iOS 各一张卡片——是否在两站显示（徽标）、版本与最近修改人、该平台的审计记录链接；设置（方式「关闭 / 外部链接 / 上传的安装包」（没有安装包时最后一项不可选）、链接、「在两站显示」开关、三语版本说明）一起保存，确认框要输入平台名（`android` / `ios`）与原因；当前安装包的原名、包名或 Bundle ID、版本与构建号、最低系统、大小、SHA-256、上传时间与人、下载地址（iOS 另有安装清单与 `itms-services` 安装链接）；「上传 .apk / .ipa」选文件后在浏览器里分段算 SHA-256（显示进度，不把整个文件读进内存），确认（输入平台名与原因）后按 10 MiB 分片上传、显示进度、合并校验；未完成的上传记在浏览器里（刷新后仍在，打开页面时向服务器核对，已不存在就忘掉），再选同一文件时只续传到服务器上同一文件（SHA-256、大小与种类都一致）的那次上传，选了别的文件则先删掉记住的那次再重新开始（不占同时未完成的名额），也可以「放弃」；iOS 另有「配置描述文件（可选）」的上传；「服务器上保留的文件」逐个删除，确认框写明删的是当前安装包、配置描述文件还是旧文件以及删除后的状态（确认词为文件 ID 的后 4 位）。

- **设置**：每个平台（Android、iOS）的模式——关闭（`OFF`）、外部链接（`LINK`，https，最多 500 字符：应用商店、TestFlight、其他页面）或上传的文件（`FILE`）——启用开关与三语版本说明（`zh-CN`、`zh-TW`、`en`，各最多 1000 字）。instrument-service 的 `platform_apps`（迁移 instrument 00013）保存，`GET /admin/v1/platform/apps`、`PUT /admin/v1/platform/apps/{platform}`（带读到的 `expected_version`，期间有人改过 409 `INSTRUMENT_PLATFORM_CHANGED`；`FILE` 要有当前安装包），审计 `admin.platform.app_updated`（改动字段的前后值）。站点读公开的 `GET /v1/platform/apps`（缓存 1 分钟，ETag 为 `"<Android 版本>-<iOS 版本>-<平台资料版本>"`：改域名会改文件地址，ETag 随之变）：关闭、未启用、链接为空或安装包不在的平台为 `null`。
- **上传**（分片，避开 Cloudflare 单请求 100 MB 的上限）：`POST …/{platform}/uploads`（`kind` 为 `APP` 或 iOS 的 `MOBILECONFIG`、文件名（扩展名对应 .apk / .ipa / .mobileconfig）、大小（最多 500 MiB，配置描述文件 1 MiB）、SHA-256）→ 201 上传（`upload_id`、`part_size` 10 MiB、`parts`、已收的 `received`、24 小时后过期）；`PUT …/uploads/{id}/parts/{n}`（`application/octet-stream`，正好一片的字节数，最后一片为余数；可乱序、可重传；nginx 只给这条路由 16m）；`GET …/uploads/{id}` 续传时看已收的片；`DELETE …/uploads/{id}` 放弃；`POST …/uploads/{id}/complete`（带原因）合并、校验并启用。同时最多 3 个上传（409 `PLATFORM_APP_UPLOADS_FULL`）；每个平台最多保留 10 个文件（409 `PLATFORM_APP_FILES_FULL`，先删旧的；替换 iOS 配置描述文件的上传不算）；磁盘剩余不到文件两倍加 2 GiB 时拒绝（409 `PLATFORM_APP_DISK_FULL`）；合并进行中、或另一片正在写入时，再传分片、完成或放弃都 409 `PLATFORM_APP_UPLOAD_BUSY`（每次写一片都占住这次上传，合并不会读到写了一半的分片，放弃后也不会再落下分片；分片要一片一片地传）；缺片 409 `PLATFORM_APP_UPLOAD_INCOMPLETE`（`details.missing`）。上传状态在 admin 库的 `app_uploads`（迁移 admin 00017），分片在磁盘，admin-service 重启后能续传。
- **校验与存放**：合并后核对开始时声明的大小与 SHA-256，再按包读（`internal/admin/adapters/apppkg`）：.apk 是 zip 且有编译后的 `AndroidManifest.xml`（包名、versionName、versionCode、minSdkVersion），.ipa 是 zip 且有唯一的 `Payload/<名>.app/Info.plist`（bundle id、版本、build、最低 iOS），.mobileconfig 是 `PayloadType` 为 `Configuration` 的 plist（可带 CMS 签名）；不合格 422 `PLATFORM_APP_FILE_INVALID`（`details.reason`），这次上传随即作废。存为 `downloads/<android|ios>/<file_id>.<扩展名>`（先写临时文件再改名，目录 755、文件 644）；.ipa 旁边生成 `<file_id>.plist`（OTA 安装清单，`url` 指向 `https://<平台资料的域名>/downloads/ios/<file_id>.ipa`，域名为空时用后台所在站点 `admin.<站点>` → `<站点>`；改域名后要重新上传 .ipa）。安装包上传完即成为该平台的当前文件、模式改为 `FILE`（启用开关不动），之前的文件保留到手动删除；配置描述文件替换旧的并删掉旧文件。审计 `admin.platform.app_file_uploaded`（平台、种类、文件 ID、原名、大小、SHA-256、包里读出的版本）。文件的地址按平台资料的域名生成（为空时用上传时的站点）。
- **下载入口**（H5，用户 2026-10-07 19:3x，设计 §1.2 #8）：页面顶部「下载入口」卡片——两站的下载入口（PC 顶栏「下载」与二维码面板、页脚「下载 App」、手机「我的」与设置里的「下载 App」）显示还是隐藏，版本、最近修改人与时间、审计链接（对象 `app:download_entry`）。默认开：开着时入口一直显示，两个平台都没配也显示，下载页与二维码面板写「暂未提供」；关着时入口隐藏，`/download` 仍可直接打开，两个平台的设置不变。`PUT /admin/v1/platform/download-entry`（`{visible, reason}`，`settings.write`，确认词 `entry`）经 instrument-service 的 `PUT /internal/platform/download-entry` 写表 `platform_download_entry`（一行，迁移 instrument 00014，`config_history` 记 `DOWNLOAD_ENTRY` 的前后）；切到当前状态不改版本、不审计；审计 `admin.platform.download_entry`（from、to、版本：取自 instrument-service 在行锁下答的 `{entry, previous}`，两位管理员同时切换也各记各的，A89）。两站读公开 `GET /v1/platform/apps` 的 `entry.visible`（缓存 1 分钟，ETag 第四个数是它的版本），一分钟内生效；`GET /admin/v1/platform/apps` 带 `entry`；上线检查清单的 App 下载一项另写 `entry_visible`。
- **删除**：`DELETE /admin/v1/platform/apps/{platform}/files/{file_id}`（带原因）：先从平台的文件列表去掉（删的是当前安装包时，有链接回到 `LINK`，否则 `OFF`），再删磁盘上的文件（.ipa 连同清单）；审计 `admin.platform.app_file_deleted`。
- **清理**：admin-service 每 10 分钟清掉过期的上传（连同分片）与没有对应记录、写入已超过 1 小时的上传目录，并删除 `downloads/` 下哪个平台都不再保留、且写入已超过 1 小时的文件（完成时应答丢失、删除时磁盘没删掉的）。
- **目录与部署**：admin-service 挂 `infra/downloads`（`/srv/downloads`，`APP_DOWNLOADS_DIR`）与 `infra/app-uploads`（`/srv/app-uploads`，`APP_UPLOADS_DIR`），读写；nginx 只读挂 `infra/downloads`（`/usr/share/nginx/files/downloads`），三个站点的 `/downloads/` 只认 `<平台>/<UUID>.<扩展名>`（.apk 与 .ipa 带 `Content-Disposition: attachment`，安装清单不缓存，其余 1 小时），其他路径 404。目录由部署脚本建好、交给容器用户 uid 10001（[server-deploy.md](server-deploy.md)）。

### 上线检查清单

`/launch`（所有角色可看，`reports.read`；设计 2026-10-04 §4.6，D2）：只读，不改任何值，每 30 秒刷新。`GET /admin/v1/launch-checklist` 逐项从来源读取当前值，状态为达标 `OK`、未达标 `FAIL`、待接入 `PENDING`（来源尚未上线）或读不到 `UNKNOWN`（来源没有应答），全部 `OK` 时 `ready` 为真、页面显示「可上线」，否则列出未达标的项。只覆盖后台能改和能看的项，部署侧（域名、TLS、密钥、第三方账号）见上线手册。

页面（A94）每项一行：名称与来源（开关键、科目名为等宽小标签）、状态徽章（`OK` 已就绪 绿、`FAIL` 阻塞 红、`PENDING` 待处理 黄、`UNKNOWN` 读不到 黄）、一句话的当前值（例如保险基金「开放中 U 本位 88、币本位 21；基金 22 种结算币，全部 > 0（最低 BTC 4）」），点行或箭头展开才显示「上线应为」与原始值（开关键与规则、按资产的金额表）。开关写作「开/关」加灰字「对所有人/带规则」，红色只留给状态徽章与真正的问题。系统健康的对账与行情源、对账页、HOUSE 额度、平台设置的注册赠送用同一组件（ui 包 `SummaryTable`/`SummaryRow`）；全后台的数字用正文字体的等宽数字，等宽字体只给 ID 与键名。

| 项 | 来源 | 上线应为 | 去修改 |
|---|---|---|---|
| 注册赠送 `welcome_credits` | 账本设置（并显示总闸） | 全部为 0 | 平台设置 |
| 测试模式 `test_mode` | 平台资料 | 关（正式模式） | 平台设置 |
| 注册方式 `registration` | 平台资料 | 开放或按运营决定（只显示，不影响可上线） | 平台设置 |
| 后台登录需验证码 `admin_totp` | 开关 `admin.login_without_totp` | 关 | 功能开关 |
| 双人审批 `two_person` | 开关 `admin.two_person_approval` | 开 | 功能开关 |
| 测试资产资格 `test_assets` | 开关 `wallet.test_assets` | 关（带地区规则也算开） | 功能开关 |
| 托管方 `custodian` | wallet-service `/internal/wallet/custody` 的 `configured` 与 `gateway_host`（UDUN） | 已配置且不是 `udun-mock`；没有 `gateway_host` 字段时待接入 | 上线手册 |
| 提现总闸 `withdraw` | 开关 `wallet.withdraw` | 开 | 功能开关 |
| 品牌 `brand` | 平台资料 | 名称不是初始的 `Astras`、至少一张 logo、favicon 已上传 | 平台设置 |
| 平台币资料 `coin_profile` | 模拟市场的币（默认 ASTRA）的资产资料 | 名称与 logo 已设置 | 代币信息 |
| 法律页 `legal` | 内容 LEGAL 分区 | `terms`、`privacy`、`risk` 在正式模式下有已生效的覆盖稿（FORMAL 或 BOTH，已发布且不是定时到以后；「以默认稿发布」也算；只有测试稿不算）；分区没上线时待接入 | 固定页面 |
| 第三方 `third_party` | 各服务的 `exchange_config_present` | 人机验证、邮件、链服务都为是；有一项上报为否即未达标，有一项没人上报（那个服务的指标读不到）为读不到；没有服务上报时待接入 | 上线手册 |
| 管理员 `admins` | 后台名册 | 至少 2 名启用的 ADMIN，全部绑定身份验证器 | 管理员与角色 |
| 域名 `domain` | 平台资料的 `domain` 与访问后台用的主机名（nginx 转来的 Host） | 后台在 admin.<资料里的域名> | 平台设置 |
| HOUSE 报价与资金 `house` | 开关 `market.house_liquidity` 与 HOUSE 库存（MARKET_MAKER 账户） | 开，且每个组成交易对的背书资产（有充值或提现的资产，如 USDT、BTC、ETH；没有交易对的托管方测试资产不算）余额大于 0（复审 ㉚ 补的第 15 项，设计 §3 E 行） | HOUSE 敞口 |
| 杠杆交易 `margin` | 开关 `margin.enabled`、`margin.liquidation`、`margin.auto_borrow` | 关；或开着且强平开着，杠杆与自动借款都按用户或地区规则开放（对所有人全局打开即未达标，测试服现在如此，协调会话 2026-10-06 07:40 ③；杠杆设计 §8，E5） | 功能开关 |
| 合约保险基金 `insurance` | 账本 `INSURANCE_FUND`（各资产）与交易中的合约 | 每个 TRADING 合约的结算币（U 本位为 USDT，币本位为该币）保险基金余额大于 0（币本位设计 2026-10-06 §2.7，协调会话 20:45，G5） | 合约与保险基金 |
| 币本位合约 `coin_m` | 开关 `derivatives.coin_m` | 关；或按用户或地区规则开放（对所有人全局打开即未达标，测试服现在如此） | 功能开关 |
| App 下载 `app_downloads` | 平台设置 → App 下载（instrument-service 的 `platform_apps` 与 `platform_download_entry`） | 仅供参考：显示两站对 Android、iOS 各提供什么（外部链接、上传的安装包或不提供）与下载入口显示还是隐藏（`entry_visible`，H5），都不提供也算达标；读不到时未知（App 下载设计 2026-10-07 §5，H4） | App 下载 |
| 产品线 `products` | 资产与交易对 → 产品线（开关 `product.spot`、`product.usdt_m`、`product.coin_m`） | 仅供参考：列出三条产品线开着还是关着（关着的带关闭时间），关着也不算未就绪 | 资产与交易对 |

### 审计

`/audit`：

- `GET /admin/v1/audit-logs`：按操作者（`actor`：管理员邮箱、`cli:<用户名>`）、对象（`target`：`user:<id>`、`pair:<symbol>`、`contract:<symbol>`、`asset:<代码>`、`insurance:<asset>`、`flag:<key>`、`approval:<id>`、`admin:<id>`、`withdrawal:<id>`、`broadcast:<id>`、`announcement:<slug>`、`sim`、`instruments`；充值的补记、入账、驳回与记给用户记在 `user:<id>` 上）、`event_type`、`from`、`to` 查询 ClickHouse `audit_logs`，写入后几秒可查；`limit` 最多 500。后台的每个动作都有审计事件（登录/登录失败/退出、账户状态（user-service 记，操作者为管理员邮箱）、撤单、交易参数、开关（含前后值）、资金操作、提现审批与充值处置（wallet-service 记）……，各节已列出）。
- 点行打开详情：时间、操作人、对象、动作、理由、事件 ID，以及逐字段的变更表（配置变更比较前后值；`from/to`、`before/after` 与上架的 `changes` 逐项列出；未变的字段折叠），原始事件可展开。
- **导出 CSV**：服务端按当前筛选导出（`GET /admin/v1/audit-logs/export`，要 `audit.export`，只有 ADMIN 与 AUDITOR 有、其他角色看不到按钮）。最新的最多 10,000 条，先按筛选计数、超过时响应头 `X-Truncated: true`，再一页页边查边写，不在内存里攒满（C5.5 ⑪），页面提示缩小时间范围。UTF-8 带 BOM，列为 `occurred_at,event_type,actor,target,action,reason,details,event_id`（配置变更的 `details` 为 `{"old","new"}`），以 `= + - @` 开头的文本前加单引号，防止表格软件当公式执行。导出本身记审计 `admin.audit.exported`（对象 `audit`，详情为筛选条件与行数）。
- 与设计稿 §5 的差别：没有 `GET /admin/v1/audit-logs/{id}`，详情直接用列表里的事件。

### 报表

`/reports`（任务 12 起，所有角色可读，`reports.read`）：来自 ClickHouse 读模型（[analytics.md](analytics.md)），比服务晚几秒。图表与表格两种视图（切换标签时保持），近 7/30/90 天或自定日期，按日、周或月汇总（C4c）。

- **时间范围**：交易、充提、合约、用户与 HOUSE 报表都接受 `days`（近 N 天，最多 90）或 `from`/`to`（UTC 日期，含两端，`to` 默认今天），以及 `bucket=day|week|month`（周从周一起；一行的 `day` 是它所在区间的第一天）。按日最长一年，按周或按月最长三年；`bucket` 不对、开始晚于结束、结束晚于今天都是 400。
- **交易**：按交易对与 UTC 日的成交笔数、成交量、成交额、受理与被拒订单（含合约）；任意交易对 1m/5m/15m/1h/4h/1d K 线。
- **充提**：按资产与日的入账充值（不含未认领）与完成提现（金额、手续费）。
- **合约**：按合约与日的成交（双边笔数、成交量与成交额按买方算一次、手续费、已实现盈亏）、资金费付出与收到、强平数、ADL 数、保险基金垫付；当前各合约持仓量（多头、空头、持仓数）。
- **用户增长**（`GET /admin/v1/reports/users`）：每个区间的注册数与登录人数（auth 事件）、交易人数（现货任一方或合约成交）、充值到账人数，各自按人去重；`total` 是区间结束时的累计注册数。HOUSE 与模拟市场的机器人不计（机器人名单读 market-sim 的 `GET /internal/sim`，`MARKET_SIM_URL`；读不到时 `partial` 含 `bots`，页面提示数字包含机器人）。每个区间都有一行，没有数据的也是 0。
- **HOUSE 盈亏**（`GET /admin/v1/reports/house-pnl`，USDT）：
  - 现货按天估值：每个交易对上 HOUSE 收到减付出的计价资产，加上它因交易持有的基础资产按当天最后成交价计；非 USDT 计价的交易对按其计价资产的 USDT 交易对当天收盘折算。`spot_pnl` 是区间内的变化，`spot_result` 是开始以来到区间结束时的累计结果（与 HOUSE 页的交易盈亏同口径）。需要价格时没有价格的交易对整段不计，列在 `unpriced`。
  - 合约：HOUSE 用户（`HOUSE_USER_ID`）成交的已实现盈亏减手续费（`contracts_pnl`），以及它收到的资金费（`funding`，付出为负）；未实现盈亏不计。币本位合约的这些数以币计，按该笔成交价、资金费按结算时的标记价折成 USDT（美元按 USDT 计；读模型里 G1 之前的行没有结算币，按 USDT）。
  - `total` 是三项之和，`cumulative` 是期间内的累计。图表的柱子可以向下（亏损），零线标出。HOUSE 页的「近 30 日盈亏」用同一个报表。

### 设置

`/settings`：

- **资金操作的审批**：双人审批开关与三个限额（单笔、24 小时、提现），见「资金操作」。`GET /admin/v1/settings`（所有管理员可读，含调用者 24 小时已用额 `daily_used_usdt`），`PUT /admin/v1/settings`（ADMIN，理由必填；限额审计 `admin.settings.changed`，双人开关经开关表审计 `flag:admin.two_person_approval`，本实例立即生效，其它 5 秒内）。也可以用 `exchangectl flags set admin.two_person_approval --on --reason "..."` 打开。三个限额各自最多是默认值的 10 倍。
- **交易参数的等待时间**：`change_delay_seconds`（不低于 `change_delay_floor_seconds`），见「交易参数的护栏」。
- **本浏览器**：外观（浅色、深色）、语言、每页条数（20/50/100/200）。其他人只读。

## 运维

创建管理员（交互使用：不带 `--secrets-stdin` 时随机生成密码与 TOTP 密钥并只显示一次，把 otpauth 链接或密钥录入身份验证器 App；这样生成的密码首次登录后必须先改掉，C5.5 ⑪。平时由 ADMIN 在后台新建，只给一次性设置链接）：

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
- 锁定：等 15 分钟自动解锁，或由 ADMIN 在「管理员与角色」页启用（同时清除锁定）；忘记密码或丢失 TOTP：另一位 ADMIN 在同一页重置（对方会话结束，得到一次性设置链接交给本人，本人自己设新的；C5.5 ⑪）。不能重置自己的；唯一的 ADMIN 丢失时，用 `exchangectl admin disable` 停用后以新邮箱 `admin create` 重建（命令行没有重置入口，避免成为绕过 TOTP 的后门）。
- IP 白名单（可选）：在服务器建 `/opt/exchange/infra/nginx/snippets/admin-access.local.conf`，内容如 `allow 203.0.113.7; deny all;`，`task deploy` 或 `nginx -s reload` 后对 `admin.astras.vip` 整站生效（真实客户端 IP 由 Cloudflare real-ip 配置还原）。目前按用户决定不设。部署同步不会覆盖或删除这个文件。
- 服务地址（compose）：`DERIVATIVES_SERVICE_URL`（`http://derivatives-service:8095`）、`NOTIFICATION_SERVICE_URL`、`MARKET_SIM_URL` 等；`HOUSE_USER_ID` 从 `apps.env` 读；`ADMIN_CHANGE_DELAY_FLOOR`（测试服 60 秒）；`SIM_ADMIN_API_SECRET` 在 `sim/admin.env`；`HOUSE_CAPS_ADMIN_API_SECRET` 在 `house/admin.env`（HOUSE 额度的 `admin` 键，C47）与 `MARKET_MAKER_URL`（`http://market-maker:8091`）。
- 指标：运维端口 9094（`outbox_pending`、`http_server_*`）；Prometheus 任务 `admin-service`。

## 测试

- **单元与集成测试**：`internal/admin/...`（应用层用内存仓库与各服务的假实现；`adapters/postgres` 的集成测试连测试服 `exchange_test` 库，`task test:integration`）；迁移 `migrations/admin` 由 `./migrations/` 的集成测试逐个上下回滚。
- **端到端** `bash scripts/e2e/admin.sh`（对 `https://admin.astras.vip`，每次创建 4 个随机管理员、结束时停用；脚本里的 curl 一律走 HTTP/1.1（`$CURL_HOME/.curlrc`，连公共的 `call` 打用户站的也算；因此这次运行不读 `~/.curlrc`）——本机 curl 8.7.1 走 HTTP/2 时，2026-10-07 有三个 nginx 已答 200 的回答以帧错误 exit 16 失败，10-09 用户站的 `GET /v1/platform/apps` 也一样，公共的 `call` 不重试这种；上传安装包的分片在传输断开或路上答 5xx 时重传，同一分片重传即覆盖，A90）。覆盖：
  - 页面与安全头、旧地址的跳转、登录与 Cookie、角色、备注与标签、批量审核提现、冻结/解冻、撤单、开关往返、待办与事件流、报表、资产资料与其审计、分页列表与概览、用户页的估值余额、单笔撤单。
  - 交易参数的护栏（C3c：等待时间临时设为 60 秒、结束时恢复，测试服的下限是 60 秒；OPERATOR 不能改状态与参考倍数；ETH-BTC 只预览暂停（带挂单数）与只可撤单；没有确认令牌 409；LINK-BTC 的参考倍数修改经 ADMIN 确认后排期再取消，同一令牌再提交返回同一条修改；ETH-USDT-PERP 立即暂停、确认的恢复一分钟后由后台执行；LINK-BTC（C5.5 ⑩ 起跟随币安 LINKBTC、由 HOUSE 报价（第一次运行把它追加进 `market.house_liquidity` 的名单，名单其余不变），文档每次相同、只第一次创建；已有交易对写了别的状态时提示 `STATUS_IGNORED`）立即暂停、确认开放、一分钟后在币安买一价下 5% 挂单并撤掉，运行结束时仍在交易；更严的风险阶梯预览列出影响；BTC-USDT 的参考符号不能清空；按币关闭 BTC 的合约（A63）：OPERATOR 403，预览列出两种保证金类型的合约与一个令牌，没有可重新开放的 409、`DELISTED` 400、没有合约的币 404、不带令牌 409，确认后得到一条 `COIN_CONTRACTS_STATUS` 修改并立即取消（退出时再取消本次运行留下的），BTC 的合约状态不变）。
  - 资金操作：双人调账、设置的权限与校验、单人模式（ADMIN 直接 +2.5/−2.5 USDT，超过单笔限额的转审并撤回；双人模式时跳过）、幂等键（不带键 400；同一键的调账、冻结、解冻与站内信各发两次只生效一次，站内信只审计一次、用户只收到一条；同一键换金额 409；决定者重复批准返回原操作）、合约（状态、只减仓、强平监控与记录、双人保险基金注资 1 USDT）、风控冻结与解冻（用户资金流水里看得到 `ADMIN_FREEZE`；运维的 `exchangectl ledger release-hold` 超过冻结单的数量被拒、默认解冻冻结单的全部，审计带 `forced`）、合约账户调账（单人模式时）、强制平仓（用户市价买入 0.1 ETH-USDT-PERP，HOUSE 的仓位排在用户之后，扣 1 USDT 前的全仓保证金预览（扣后权益少 1），后台平掉后仓位为空；合约交易关闭时跳过）。
  - 充值处置与补记（用托管方替身 udun-mock：回调推迟 600 秒的 2 USDT 由 FINANCE 补记、用户余额 +2、同一交易号再补记被拒、晚到的回调记为已核对且不再入账、`exchangectl` 报告里不再列出；低于最小额的 0.5 USDT 入账给用户、0.25 USDT 驳回后不能再入账；补记的检查做完才把延迟改回 0、放出扣住的回调——56e491b 起替身对已扣住的回调也按新设的延迟）。
  - 无主充值（C5.5 ㉑，在替身托管方 `UDUNMOCK` 的隐藏测试资产上）：一个 AQ 用户取充值地址、`exchangectl wallet retire-addresses --provider UDUNMOCK` 退役替身的地址后向它打入 1 与 2 个，两笔都是无主、原持有人是这个用户；OPERATOR 不能记、不带键 400、直接入账 409；FINANCE 把 1 记给原持有人（按常规限额，测试资产没有报价时由 ADMIN 批准），同一键再来是同一笔操作，再记一次 409，原持有人多 1；把 2 记给另一个用户时不论金额都等第二人（`NOT_ADDRESS_HOLDER`），申请人自己批不了，同一笔充值的第二个申请 409 `ADMIN_DEPOSIT_ASSIGN_OPEN`，ADMIN 批准后入账，审计带原持有人；`UDUNMOCK` 还没有网络时跳过。
  - 托管方手续费（C6：AUDITOR 读替身的待处理手续费、只有 `UDUNMOCK` 的；未知状态 400；OPERATOR 不能处理、没有理由 400、没有手续费的提现 404 `WALLET_CUSTODY_FEE_NOT_FOUND`；已入账的不能核销，409 `WALLET_CUSTODY_FEE_NOT_HELD`。编码会话的 `scripts/e2e/lib/held-fees.sh` 在替身上造两笔待处理手续费（报 999 TUSD、不扣余额）：按实扣 5000 超过 5 倍被拒（422），按实扣 1 TUSD 入账、再入账 409，另一笔核销，wallet-service 以 FINANCE 的名义审计）。
  - 提现详情与搁置（对已完成的提现搁置得到 409）、提现暂停（C5.5 ⑯：用 `exchangectl wallet withdrawals-suspend` 暂停 ETH，后台列出原因，FINANCE 不能解除，ADMIN 带理由解除，再解除得到 404，wallet-service 以 ADMIN 的名义审计）。
  - 用户的安全/历史/风控与完整联系方式、换绑审核（用户换绑唯一的邮箱 → 后台通过 → 按新邮箱能查到）、重置身份验证器、全部会话退出（用户令牌立即失效）、临时密码（旧密码失效、临时密码可登录、审计里没有它）。
  - 后台建管理员（C4a、C5.5 ⑪：ADMIN 建 OPERATOR，响应 `no-store`、只有一次性设置链接；设置前登录不了；不带会话打开链接看到账号与要绑定的密钥，短口令与错的验证码被拒，设好后链接作废、用自己设的登录；改为 AUDITOR 后下一个请求即生效；重置口令与身份验证器都结束会话、旧的立即失效，各自的链接设好后可登录；自己改口令时错的当前口令被拒、改后用新的登录；结束会话；停用后不能登录、启用后可以；不能改自己的账号；链接、口令与密钥不出现在任何输出与审计里）。
  - 模拟市场（不改动线上价格：两个事件都排在明天并取消，其中 35% 的那个由 OPERATOR 申请、ADMIN 批准，检查 market-sim 记下的发起人与批准人；参数改了再改回，超出份额的参数改动被拒绝；每个机器人增发 0.01 USDT，20 万 USDT 的增发被拒绝）。
  - 运营：固定 slug `e2e-console` 的公告（文章不删除，第一次运行新建，以后改写）——定时发布前站点看不到，立即发布后 PC 站与手机站的接口 1 分钟内列出，发布中修改 1 分钟内更新，旧版本的修改被拒，下线后从列表消失、slug 进 `withdrawn`；给本次的测试用户发一条站内信，用户在通知里看到并读过后，后台显示已收到 1、已读 1；列表一页一页给、只有摘要，坏游标 400；这条消息没有失败的轮次，「继续发送」只对 `FAILED` 的有效（409），AUDITOR 不能（C5.5 ⑫）。另有固定 slug `e2e-console-test` 的公告只用于测试模式（D4）：发布后测试模式下站点接口一分钟内列出它，ADMIN 在平台资料里关掉测试模式后一分钟内不再列出，打开后又列出，最后下线；测试模式按运行前的样子恢复（运行中断时由退出钩子恢复）。
  - 平台设置与上线检查清单（D2）：清单 20 项（K3 加产品线一项、H4 加 App 下载一项，都只作信息，不算未就绪；G5 加合约保险基金与币本位合约两项，E5 加杠杆交易一项；更早的后台为 16 或 15 项；来源读不到的项为 UNKNOWN，不按失败算）、测试服未就绪（后台免验证码登录与测试资产开着）；OPERATOR 改不了平台资料，ADMIN 改名为 `E2E Exchange <运行编号末 4 位>` 后按读到的版本改回（旧版本 409，运行中断时退出钩子改回；名称里不放 6 位以上的数字：邮件带着平台名称、notification-service 缓存 10 分钟，而 e2e 取验证码取邮件里第一个 6 位数）；注册赠送：提高超过 10,000 USDT 422、旧版本 409、提高 1 USDT 进入 `WELCOME_CREDIT` 审批（申请人与 FINANCE 批不了），ADMIN 撤回后版本不变；审计里有改名与申请；LEGAL 栏目可读，六个固定 slug 以外的被拒（400，什么都不写）。
  - 杠杆（E5；后台答 404 时跳过）：三类条款的读取与约束；OPERATOR 改不了参数，ADMIN 改 USDT 的利率得到 `MARGIN_PARAMS` 申请，同一资产再改 409 `ADMIN_MARGIN_CHANGE_PENDING`，自己不能批准、撤回后版本不变；账户列表按风险率排序、冻结理由为文本。本次的测试用户划入 10 USDT 开全仓账户（`margin.enabled` 不对它开放时跳过这一段）：列出与详情，FINANCE 冻结不了，OPERATOR 冻结（`frozen_by` 是它的邮箱、理由照记）、冻结中划不出、同样理由再冻结返回账户、ADMIN 再冻结 409、冻结审计在用户上，解冻、再解冻 409 `MARGIN_NOT_FROZEN`；手工强平 FINANCE 403，`margin.liquidation` 开着时没有负债 409 `ADMIN_MARGIN_NOTHING_OWED`、关着时 409 `ADMIN_MARGIN_LIQUIDATION_OFF`；结束时解冻并划回。强平与利息报表可读。
  - 系统健康（全部就绪且带版本，消费者的滞后与死信数，行情源）、审计查询（含充值处置的四个动作与管理员的七个动作）与 CSV 导出（BOM、表头、`X-Truncated: false`，导出本身被审计）、退出与停用。
- **浏览器冒烟** `web/e2e/admin-smoke.mjs`（`scripts/e2e/web.sh` 运行，每次建一个临时 ADMIN、结束停用；1440 × 900）：登录、概览、从侧栏第一次打开审计页（块扣住时显示骨架、只取一个文件，A40）、空闲时取完各节页面后再打开三节不再请求 JS（A43 ⑩）、用户页与各标签、身份变更申请、搜索、订单与成交、充值（待处理、补记待回调、补记抽屉，不提交）、提现队列（带筛选）与一笔提现的详情（地址簿、该用户最近的提现）、托管方（优盾的页面能打开，不论连着哪个网关；替身 `UDUNMOCK`：可访问、TUSD 币种、它的对账行、一条回调的原始请求与来源地址、托管方手续费，C6 起按协调会话 ⑤ 离开优盾）、交易对改状态的确认框（取消，不真的改）、资产的资料与图标、合约、仓位、强平记录、HOUSE（近 30 日盈亏、敞口、各交易对、各合约净头寸）、开关、对账、审计（一条的详情、CSV 导出）、报表（含用户增长与 HOUSE 盈亏）、管理员与角色（新建表单打开后取消）、系统健康、上线检查清单（结论与各项状态）、平台设置、固定页面（七行在两种模式下各自的站点显示，恰好一栏按平台资料的测试模式标「当前」，首页横幅的编辑器从默认稿打开、不保存）、公告列表按模式筛选（地址栏带 `modes=TEST`、每行都标测试模式）、公告编辑器（新稿默认通用，选测试模式后预览只留 `:::test` 段落，不保存）、帮助中心、站内信与发送表单（不发送）、模拟市场五页（价格控制的确认框显示影响后取消，不发起事件）与机器人的订单、杠杆四页（开关 `margin.enabled` 开着时：资产参数的编辑器打开后不改、交易对改倍数不覆盖阈值而给出建议值；`web.sh` 先建一个用户、把 10 USDT 欢迎资金划入全仓账户，冒烟打开这个账户的详情与四个页签，结束后划回；关着时检查侧栏没有杠杆入口）、资金调整页、审批、设置（含每页条数）、事件流、账号与安全（不修改）、从账户菜单退出、不带链接的设置页；所有 `/admin/v1` 响应按 `api/admin/admin.yaml` 校验。本机：`ADMIN_EMAIL=… ADMIN_PASSWORD=… APP=http://localhost:5180 node web/e2e/admin-smoke.mjs`。
- **Lighthouse**：`task web:lighthouse` 跑登录页（`web/lighthouse/admin.json`，性能 ≥ 90）与登录后的页面（`web/lighthouse/console.sh`：经 ssh 建一个临时 ADMIN（口令与密钥从标准输入传入、不打印），会话以请求头文件交给 Lighthouse，结束时退出并停用；设计 §6 要求性能 ≥ 85；C6 收尾时登录后的十一页为 90–95，见上文「首屏」）。报告在 `.lighthouseci/`。
- **视觉检查**：各批次的页面在本机开发服务器上用契约样例数据截图核对（脚本不入库，结果写在设计稿 §10 的验收记录里）。

## 常见错误码

| 错误码 | 含义 |
|---|---|
| `ADMIN_LOGIN_FAILED` | 邮箱、密码或验证码错误，或验证码已用过（`admin.login_without_totp` 打开时只看邮箱与密码） |
| `ADMIN_LOCKED` | 连续失败 5 次，锁定 15 分钟 |
| `ADMIN_UNAUTHORIZED` | 没有会话或会话已过期/撤销 |
| `ADMIN_FORBIDDEN` | 角色没有该权限 |
| `ADMIN_CSRF` | 写请求缺少 `X-Admin-CSRF: 1` |
| `ADMIN_SELF_APPROVAL` | 不能批准自己的双人申请（可以撤回；单人模式下结果未知的操作可以自己完成） |
| `ADMIN_APPROVAL_DECIDED` | 申请已处理（同一位管理员重复同一个决定时返回操作本身，不报这个错） |
| `ADMIN_APPROVAL_ATTEMPTED` | 这笔资金操作的执行没有结束、可能已经记账（待核对）：只能完成，不能拒绝或撤回 |
| `COMMON_IDEMPOTENCY_CONFLICT` | 同一个 `Idempotency-Key` 用于另一个请求（内容不同）；先在「审批」核对结果未知的那笔，新的操作换新键 |
| `ADMIN_EXISTS` | 新建管理员（后台或 `admin create`）的邮箱已存在 |
| `ADMIN_SELF` | 不能在后台修改自己的管理员账号 |
| `ADMIN_LAST_ADMIN` | 最后一位启用的 ADMIN 不能被停用或降级 |
| `ADMIN_SETUP_INVALID` | 设置链接不存在、已经用过或已过期（24 小时）：请 ADMIN 重新重置 |
| `ADMIN_PASSWORD_CHANGE_REQUIRED` | 命令行生成的口令要先改掉（`POST /admin/v1/me/password`） |
| `ADMIN_PASSWORD_WRONG` | 改自己的口令或身份验证器时，当前口令不对 |
| `ADMIN_TOTP_CODE_WRONG` | 设置链接或自己的身份验证器：验证码不对或已用过（核对手机时间） |
| `ADMIN_REFERENCE_UNKNOWN` | 交易对的参考符号币安现货没有（详情 `symbol`、`reference_symbol`） |
| `ADMIN_REFERENCE_IN_USE` | HOUSE 正在报价或有永续合约以它为指数，交易对的参考符号不能清空（详情 `symbol`、`used_by`） |
| `ADMIN_CONFIRMATION_REQUIRED` | 交易参数的修改要带预览给的确认令牌；`reason` 为 `expired`（超过 10 分钟）或 `changed`（预览后数据变了）时重新预览 |
| `ADMIN_CHANGE_CLOSED` | 这项待生效修改已经生效、取消、驳回或失败 |
| `ADMIN_CHANGE_APPLYING` | 这项修改已被执行的一轮认领（可能已经生效），不能取消；稍后看它的结果 |
| `ADMIN_IMPACT_GREW` | 风险阶梯到点时再量，会被强平的仓位比确认时多（详情 `symbol`、`liquidated`、`confirmed`）；这条修改记为失败，重新预览 |
| `ADMIN_NEW_ITEM_NOT_PREPARE` | 后台新建的交易对或合约写了「准备中」以外的状态（详情 `symbol`、`status`）；先建再经状态修改开放 |
| `ADMIN_SIM_MINT_CAP` | 增发超过单次上限（10,000,000 个币或 1,000,000 USDT），谁批准都不行 |
| `ADMIN_SIM_TOO_FAR_AHEAD` | 价格事件（或目标的预览）的开始时间超过 24 小时之后（market-sim 的 MaxLead） |
| `ADMIN_WELCOME_RAISE_CAP` | 一次提高注册赠送超过 10,000 USDT 等值，谁批准都不行 |
| `ADMIN_WELCOME_UNPRICED` | 要提高的资产没有新鲜的 USDT 价格，无法折算 |
| `ADMIN_MARGIN_CHANGE_PENDING` | 同一资产、交易对、全仓条款或账户已有一条待审的杠杆申请（409，`details.approval_id`）；先批准、拒绝或撤回它 |
| `ADMIN_MARGIN_NOTHING_OWED` | 手工强平：账户没有负债 |
| `ADMIN_MARGIN_LIQUIDATION_OFF` | 手工强平：开关 `margin.liquidation` 对该账户的用户关着，margin-service 不强平。申请时为 409；批准时遇到，申请记为失败，结果写这个码 |
| `MARGIN_PARAMS_CHANGED` | 杠杆参数的版本已变（期间有人改过），刷新后再改；批准时遇到则申请失败 |
| `MARGIN_FROZEN` | 杠杆账户已冻结或在强平中（margin-service 返回） |
| `MARGIN_NOT_FROZEN` | 解冻：这个杠杆账户不是管理员冻结的 |
| `MARGIN_ACCOUNT_NOT_FOUND` | 用户没有这个杠杆账户 |
| `ADMIN_WELCOME_RAISE_PENDING` | 同一位管理员对同一版本的同样提高已在等第二人批准（409，`details.approval_id` 是那条申请） |
| `INSTRUMENT_PLATFORM_CHANGED` | 保存平台资料时版本已过期（期间有人保存过），刷新后再改 |
| `LEDGER_SETTINGS_CHANGED` | 修改注册赠送时版本已过期；批准时遇到它，申请记为失败 |
| `ADMIN_TAG_EMPTY` | 按标签发的站内信：没有账户带这个标签 |
| `NOTIFY_ARTICLE_EXISTS` | 同一栏目的这个 slug 在所选模式下已有文章（测试稿与正式稿可以并存，通用稿与两者都冲突；notification-service 返回，后台原样转出） |
| `NOTIFY_ARTICLE_WITHDRAWN` | 公开接口：文章已下线（站点不再用同 slug 的自带文章顶替） |
| `WALLET_DEPOSIT_KNOWN` | 补记的托管方交易号或（网络、哈希、地址）已有充值，详情 `deposit_id` |
| `WALLET_DEPOSIT_NOT_RELEASABLE` | 只有记入 `UNCLAIMED_DEPOSIT`、有币种、回调没有不一致的待处理充值才能入账给用户 |
| `WALLET_DEPOSIT_NO_OWNER` | 无主充值不能直接入账，用「记给用户」（C5.5 ㉑） |
| `ADMIN_DEPOSIT_NOT_UNOWNED` | 只有等待处理的无主充值才能记给用户（C5.5 ㉑） |
| `ADMIN_DEPOSIT_ASSIGN_OPEN` | 这笔无主充值已有等待中或已执行的「记给用户」申请，先决定或撤回它（㉕） |
| `WALLET_DEPOSIT_RESOLVED` | 这笔充值已经处理过 |
| `WALLET_DEPOSIT_RELEASED` | 账本已经把这笔待处理充值放行给用户、钱包没记上：不能驳回，再「入账」一次把放行记下（详情 `journal_id`） |
| `WALLET_DEPOSIT_RELEASED_TO_USER` | 账本已把这笔无主充值放给一位用户、钱包没记上：记给详情里的 `user_id` 即补上记录 |
| `WALLET_CUSTODY_FEE_NOT_FOUND` | 这笔提现没有托管方手续费 |
| `WALLET_CUSTODY_FEE_NOT_HELD` | 这笔手续费不在等人处理（已入账、已核销或按报告入账；详情 `status`） |
| `WALLET_CUSTODY_FEE_CHANGED` | 这笔手续费刚被另一个决定处理了 |
| `ADMIN_FEE_ABOVE_REPORTED` | 按实扣入账超过报告的 5 倍（换币种按现价折 USDT）；确实更多时用 `exchangectl wallet custody-fee` |
| `ADMIN_FEE_UNPRICED` | 换了币种的实扣要按 USDT 比较，有一边没有新鲜报价；用 `exchangectl wallet custody-fee` |
| `ADMIN_FEE_NOT_HELD` | 只有等人处理的手续费才能按实扣入账 |
| `ADMIN_WITHDRAWAL_HELD` | 批量审核跳过了搁置中的提现，要单独审核 |
| `WALLET_WITHDRAWAL_NOT_IN_REVIEW` | 只有待审批的提现可以搁置 |
| `DERIV_HOUSE_NOT_CLOSED` | HOUSE 的仓位不能强制平仓 |
