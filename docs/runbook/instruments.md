# 资产、网络与交易对运维

需求 §5.5、§11.2、§11.3；实现见 `internal/instrument`，契约 `api/proto/exchange/instrument/v1`（gRPC 与 `instrument.events`）、`api/openapi/market.yaml`（公开 REST）。

## 数据与规则

| 对象 | 关键字段 | 约束 |
|---|---|---|
| 费率档 `fee_schedules` | tier、maker/taker 费率 | 费率 ≥ 0 且 < 10%；默认档 `default` 为 0.1% / 0.1% |
| 资产 `assets` | 代码、名称、`decimals`、可充/可提/可交易、风险开关；`rank`（市值排名，0 为无）、`categories`（板块标签，如 `layer-1`、`defi`、`meme`，最多 8 个）；`hidden`（隐藏测试资产，ADR-0017，迁移 00007：不进 `/v1/market/assets` 与图标接口，充提只对具备 `TEST_ASSETS` 资格的用户开放，内部 gRPC 照常列出；测试服只有端到端用的 TUSD） | 代码 2–10 位大写字母数字；`decimals` 0–18，**设置后不可改**；标签 2–24 位小写字母数字或 `-`，不重复；隐藏资产没有交易对与合约，有交易对或合约的资产不能设为隐藏 |
| 网络 `networks` | 资产 × 链、合约地址、确认数、最小充/提、提现手续费、Memo；`display_name`（用户看到的名字，如 TRC20、ERC20）、`address_format`（`EVM`/`TRON`/`BTC`，缺省 EVM）、`eta_minutes`（通常到账分钟数）、`explorer_tx_url`/`explorer_address_url`（带 `{tx}`/`{address}` 占位的 https 链接）；`provider`/`provider_coin`（托管方与它的币种编码：空为平台自建钱包，`UDUN` 为优盾，`UDUNMOCK` 为测试服模拟网关的第二个商户，见 [custody.md](custody.md)） | 金额精度不超过资产 `decimals`；`UDUNMOCK` 只能配给隐藏资产，配了它的资产不能取消隐藏 |
| 交易对 `trading_pairs` | tick/lot、最小/最大数量、最小名义金额、价格保护带、费率档、状态；`reference_symbol`（跟随的币安符号，空表示不跟随）、`reference_multiplier`（价格倍数，缺省 1）、`listed_at`（上架时间，缺省为创建时刻，文件里不写就保持） | tick 精度 ≤ 报价资产，lot 精度 ≤ 基础资产；tick × lot 的精度 ≤ 报价资产（成交额、冻结额因此总是精确值，2026-09-28 起 BTC-USDT 的 lot 改为 0.0001、ETH-BTC 改为 0.001）；最小/最大数量是 lot 的整数倍；保护带 (0, 1]；倍数是 1 到 10^9 之间 10 的整数次幂 |
| 永续合约 `contracts`（阶段 3；币本位 2026-10-06 G0，迁移 00011） | 代号 `BASE-QUOTE-PERP`、指数所用现货市场、tick/lot、数量与名义金额限制、限价偏离标记价的保护带、风险限额阶梯（每档最大名义价值、最高杠杆、维持保证金率）、资金费间隔/利率/上限、冲击名义金额、费率档、状态；`margin_type`（`USDT` 线性，缺省；`COIN` 币本位反向合约）、`settle_asset`（保证金与盈亏的资产：线性为计价资产，币本位为基础资产，缺省按此补上）、`contract_size`（币本位的面值，美元，BTC 100、其它 10；线性为 0）、`reference_symbol`（跟随的币安合约：U 本位 `BTCUSDT`、币本位 `BTCUSD_PERP`；ASTRA 没有） | 同交易对的精度规则；阶梯按名义价值递增、杠杆不升、维持保证金率不降且低于 1/杠杆（否则开仓即强平）；资金费间隔 1/4/8 小时；状态机与交易对相同，状态只能经 `contract-status` 改。币本位合约 `BASE-USD-PERP`：计价 `USD`（不是资产，价格精度按 USDT 校验）、指数用 `BASE-USDT`、数量为整数张（lot 为整数）、阶梯的名义价值以币计（张数 × 面值 ÷ 标记价）、冲击名义金额以张计。`margin_type`、`settle_asset`、`contract_size` 上架后不能改（已有仓位按它们记），`reference_symbol` 可改 |

金额一律 `NUMERIC(38,18)` 与十进制字符串（ADR-0008）；超精度直接拒绝，不做舍入。每次变更版本号加一，并在 `config_history` 追加一行（值、操作者、原因），同事务经 outbox 发 `instrument.events`（`AssetUpserted`、`NetworkUpserted`、`TradingPairUpserted`、`TradingPairStatusChanged`、`ContractUpserted`、`ContractStatusChanged`、`FeeScheduleChanged`）。变更立即生效；预定生效时间留到管理后台（阶段 2）。

交易对状态（附录 B）：`PREPARE → TRADING ↔ HALT`；`TRADING/HALT → CANCEL_ONLY → DELISTED`，`CANCEL_ONLY → TRADING`（下架前可重新开放，后台按币种关闭合约后再打开用它；`DELISTED` 不可逆；B122，币本位设计 2026-10-06 §3.5），合约同一张表，其他变更返回 409 `INSTRUMENT_STATUS_TRANSITION_INVALID`。

## 声明式同步（幂等）

`deploy/instruments/test.json` 是测试环境参考数据的来源，`deploy/server-update.sh` 每次部署都执行：

```bash
sudo docker compose ... exec -T instrument-service /app/exchangectl instruments apply --file - --reason "deploy <提交>" < deploy/instruments/test.json
```

- 整个文件在一个事务里生效：缺的创建，变了的升版本并发事件，相同的跳过——同一文件重复执行不产生任何变更。任何一项校验失败则全部回滚。
- 文件里没有的对象保留不动（不会删除）。
- 交易对的状态只在**创建时**取文件中的值（默认 `PREPARE`）；之后只能用 `pair-status` 改，部署不会把状态改回去。
- **后台改过的项保留**（2026-10-02 管理后台 C3）：`config_history` 多了 `source` 列（迁移 `00006`）：`FILE`（apply，即部署同步）、`CONSOLE`（管理后台）、`STATUS`（改状态）、`PROFILE`（资产资料），之前的行为空、按 `FILE` 算。文件 apply 遇到最后一次编辑（不算改状态与资料）来自后台的项时**不改**它，输出 `kept TRADING_PAIR LINK-USDT v4: changed in the admin console by ops@… at …`；部署日志逐条列出 `changed` 与 `kept` 行，最后一行 `== 参考数据：…` 是汇总（2026-10-03 起，以前只留汇总）。要让文件重新说了算：`exchangectl instruments apply --file … --reason … --force`，之后该项又跟随文件。`--dry-run` 只列出会改什么。

改参考数据：改 `deploy/instruments/test.json` 并提交，下次部署生效；紧急情况可在服务器上手工执行同一命令。也可以在管理后台「资产与交易对」里改（见下文「管理后台编辑」）。

## 管理后台编辑（2026-10-02 设计 §4.4，C3）

后台按 `test.json` 的格式读写参考数据：gRPC `ExportConfig` 导出整份配置文档（含状态与版本），`ApplyConfig` 应用一份只写要新增或修改的项的文档（来源 `CONSOLE`，`dry_run` 只算变化不写入）。规则与文件 apply 相同（同一份 `Service.ApplyWith`）：每项整体替换（漏写的字段变空）、状态不在这里改、不删除、校验失败全部回滚、每项变化升版本、记历史、发事件。后台的接口与页面见 [admin.md](admin.md#资产与交易对的编辑)。

- 交易对的新参考符号先经 market-data-service 的内部接口 `GET /internal/market/reference-symbols/{symbol}` 向币安核对（现货与 U 本位合约是否上架），币安现货没有的直接拒绝：写错的符号会让行情服务按批读取的全部交易对的参考行情失败。
- 加了参考符号的新交易对或合约会让行情服务在一分钟内重连币安的全部参考行情流（约 20 秒参考盘口为空）；HOUSE 只给开关 `market.house_liquidity` 名单里的交易对报价，名单要另外改（功能开关或 `exchangectl flags set`）。预览会提示这两点。
- 新交易对在服务里最多 5 秒（交易服务）到 30 秒（行情服务）后可用，不用重启；撮合引擎按需建盘口。
- 交易参数（状态、费率、费率档、参考符号与倍数、合约的风险阶梯）在后台另有护栏（2026-10-02 设计 §2 第 6 条，C3c）：只有 ADMIN，确认预览给的令牌，等设置里的时间（默认 5 分钟）后才由 admin-service 写入，双人审批时还要另一位 ADMIN 批准；HOUSE 正在报价或作为指数的交易对不能清空参考符号。见 [admin.md](admin.md#交易参数的护栏)。instrument-service 本身不区分，`exchangectl instruments apply` 不经过它。

### 主流 50 币（阶段 4 B4，设计稿 §8.5，ADR-0013、ADR-0014）

- 前 50 个币与 2026-10-02 的扩展名单（用户要求再上一批知名币，只做现货、站内资产：37 个，TON 因币安现货暂停交易而跳过；单价低于 0.001 USDT 的 BONK、FLOKI 以 1000 个计价）各有一个资产与一个 USDT 交易对，都跟随币安现货（`reference_symbol`），由 `go run deploy/instruments/gen-top50.go` 生成进 `test.json`（扩展名单里币安没有在交易的 USDT 对就跳过；已上架的交易对保留原有的 tick、lot 与数量上下限，免得挂单落在新步长之外）：它读一次币安的交易规则与价格，价格步长取币安的（乘以倍数，至少 0.000001）、数量步长取币安的（除以倍数）并加粗到 tick × lot 最多 6 位小数，最小名义金额 5 USDT、单笔最多值 100 万 USDT、价格保护带 10%、默认费率档；文件里的其他内容（费率档、USDT/BTC/ETH 与网络、ETH-BTC、SOL-BTC、合约）原样保留。
- 永续合约按币安的列表扩充（币本位设计 2026-10-06 §3.4，G1c）：`go run deploy/instruments/gen-contracts.go` 给 `test.json` 里每个有 USDT 交易对的币（ASTRA 除外，它的两个永续是平台自己的）加上币安有的 U 本位永续 `<BASE>-USDT-PERP`（跟随 `<BASE>USDT`，千倍币沿用 `1000…` 代码）与币本位永续 `<BASE>-USD-PERP`（跟随 `<BASE>USD_PERP`，面值取币安的：BTC 100 美元，其它 10）。它读一次币安的 `fapi`/`dapi` exchangeInfo 与币安网站公开的档位（`bapi/futures/v1/friendly/{future,delivery}/common/brackets`，不要签名）：tick 取币安的（至少 0.000001），lot 取币安的步长（币本位一张）并加粗到 tick × lot 最多 6 位小数，单笔最多取 `MARKET_LOT_SIZE`，最小名义金额 5 USDT（币本位一张的面值），价格保护带取 `PERCENT_PRICE`（最多 15%），风险阶梯照抄币安的档位（币本位按币计；最高杠杆就是币安第一档的，BTCUSDT、ETHUSDT 为 150 倍，B171；币安的阶梯若不合 instrument-service 的规则——1 到 20 档、上限递增、杠杆 1 到 150 且不升、维持保证金率大于 0、小于 1/杠杆且不降——生成时就报错退出、不写文件，B172），冲击名义金额 1 万美元（币本位折成张），资金费的周期与上限取币安的 `fapi/v1/fundingInfo`（没有条目的按 8 小时、上限 0.75%），利率每天 0.03% 按周期折算（8 小时 0.01%、4 小时 0.005%），费率档 `perp`；新合约默认 `PREPARE`（`-status TRADING` 则直接开放；`instruments apply` 从不改已上架合约的状态），已上架的合约整条保留（后台可能改过），只有资金费的周期、利率与上限，以及风险阶梯（B168，用户 2026-10-09「全部参考币安」：BTC、ETH 的 U 本位永续原来是手写的 7 档）每次重跑都按币安刷新（后台改过的仍以后台为准，apply 时保留；币安不再公布档位的合约保留原阶梯，ASTRA 的两个永续没有币安参考、不动）。快照 `binance-contracts.json` 同时存着档位（`future_brackets`、`delivery_brackets`），`-offline` 按它重放；2026-10-09 重取一次，档位与 10-07 的快照完全相同，只是 BTC、ETH 两个合约换成了币安的。重跑幂等。
  - 资金费按币安（2026-10-07 08:08 的快照）：合约跟随币安的预估与结算费率，只限两边周期同时结束的周期（见 [market-data.md](market-data.md)「标记价跟随币安」），而币安已把我们 87 个币里 23 个的 U 本位永续改成每 4 小时（HYPE、WIF、TAO、ENA、ONDO、JUP、TRUMP、1000BONK、PNUT、PENGU、CAKE、IMX、KAVA、FLOW、ZIL、PYTH、JTO、ENJ、AXS、POL、RENDER、TIA、XTZ），G1c 第一次生成时一律 8 小时、上限 0.75%。U 本位的 fundingInfo 也列出币本位永续（`BTCUSD_PERP` 等，都是 8 小时），币本位自己的接口目前是空的；上限取 `adjustedFundingRateCap` 与 `-adjustedFundingRateFloor` 中较大的（目前两者相等）：BTC 与 ETH 的 U 本位和 BTC 币本位 0.3%、ETH 币本位 0.375%、多数 2%。这次刷新了 102 个合约（ASTRA 的两个是平台自己的，不变），离线重放与联网一致、再跑不变；fundingInfo 不再列出的已上架合约（币安下架了）保留文件里原来的三项、记一行日志，不会被改回默认（审查 B142）；列出了、但规则装不下的（周期不是 1、4、8 小时，或上限超过 5%）按默认的 8 小时、0.75%，和新合约一样（审查 B145）；用还没读资金费的旧快照时全部保留，只记一行。周期从 8 小时改为 4 小时在一个 8 小时周期的前半段（UTC 0–4、8–12、16–20 点）生效最干净：行情服务提前存下原周期的样本、到点覆盖，合约服务那一刻的资金费轮次已经有了；后半段生效时合约服务会为刚过去的 4 小时整点建一个拿不到费率的轮次，2 小时后记 `funding round skipped` 跳过，不动钱。
  - 档位接口的应答带自己的 `code`：被拒时也是 HTTP 200、没有档位，生成器在 `code` 不是 `000000` 或一个档位都没有时报错退出，不会静默跳过全部合约（审查 EW，B126）。
  - 快照与离线重放（B126）：每次联网运行把读到的输入存进 `deploy/instruments/binance-contracts.json`——只留文件里的币可能上架的合约（两份 exchangeInfo 的条目与两份档位，按币安原文、每行一条，带 `taken_at`，约 340 KB），随 `test.json` 一起提交；`go run deploy/instruments/gen-contracts.go -offline` 不联网、按快照重放，用来核对某次上架的依据，或改了生成规则后重新生成。2026-10-07 02:40 的快照（UTC 18:40）联网与离线各跑一次，都生成与提交完全一致的 `test.json`，再跑一次不变。
  - 币本位档位的 `max_notional` 以币计（`BTC-USD-PERP` 首档 5 BTC、`SOL-USD-PERP` 400 SOL、`XRP-USD-PERP` 33,100 XRP），合约服务按 张 × 面值 ÷ 标记价 的币数取档（`domain.Contract.Value`），单位一致（B126 抽样）。U 本位的按 USDT 计；BTC、ETH 的 U 本位合约 2026-10-10 起也照抄币安（12 档、首档 150 倍，B168/B171；之前是 10-02 定的 125 倍七档），其余照抄币安（U 本位首档 75 倍的 74 个、50 倍 7 个、100 倍 2 个，币本位多为 20 倍），`contracts.sh` 只要求最高倍数等于首档且不超过 125。
  - 2026-10-07 的结果是 109 个合约（88 个 U 本位、21 个币本位，新增 103 个，没有币被跳过），在合约后端会话的分组订阅与权重保护（C40）部署后推送并部署：行情服务为所有未下架的合约（含 `PREPARE`）订阅参考行情，两站不显示 `PREPARE` 的合约（core `fetchContracts`）。
  - 分批开放：先 `scripts/ops/house.sh seed`（按合约给 HOUSE 补保证金与币本位保险基金）与 `scripts/ops/house.sh flags`（HOUSE 报价名单取当时上架的全部跟随币安的交易对与合约），再 `scripts/ops/house.sh open-contracts [N]`——跟随币安且仍为 `PREPARE` 的合约开 N 个（不带 N 全开），即逐个 `exchangectl instruments contract-status <合约> --to TRADING`；还没有标记价的合约留在 `PREPARE` 并列出（下单要标记价，审查 FC，B128）；开放后 HOUSE 的报价看 `market_house_active{symbol=…}`。
- 单价低于 0.001 USDT 的币按 1000 个计价（`1000SHIB` ↔ `SHIBUSDT`、`1000PEPE` ↔ `PEPEUSDT`、`1000BONK`、`1000FLOKI`，倍数 1000，ADR-0014）；0.001 到 0.01 之间的（ZIL、GALA、PENGU 等）按 1 个计价，tick 为 USDT 精度 0.000001。
- 除 USDT、BTC、ETH 外的资产（前 50 里 47 个，加扩展的 37 个）都是**内部资产**：没有网络、不能充提（ADR-0013），只能在平台上交易；精度 6 到 8 位，取决于数量步长。
- 设计稿名单里的 TON 在币安现货已停止交易（2026-10-01 查询为 `BREAK`），换成 HYPE（Hyperliquid）。
- 新交易对以 `PREPARE` 创建，HOUSE 流动性就绪后用 `scripts/ops/house.sh open` 统一开放（见 [market-maker.md](market-maker.md)）。`SOL-BTC` 故意一直保持 `PREPARE`，端到端 `trading.sh` 用它检查"未开放的交易对不能下单"。
- 币的介绍资料（前端详情页与字母图标的颜色）在 `web/packages/core/assets/coins/<代码>.json`（1000 倍币用去掉前缀的代码，如 `BONK.json`）；上新币时一起补上。
- `internal/instrument/application/testdata_test.go` 在不连库的情况下按 apply 的规则校验整个文件（至少前 50 个 USDT 交易对、交易对不重复、内部资产无网络、1000 倍币的倍数）。

## 资产资料（平台币设计稿 §5.3）

运营可以改资产在站点上的显示名、简介（简体、繁体与英文；繁体没写时繁体页面显示运营写的简体，运营也没写简体时显示仓库里生成的繁体）、链接（website/explorer/whitepaper，只收 https）与图标；资产代码不变。资料存在 `assets` 表的 `display_name`、`description`、`links`、`logo`/`logo_mime`、`profile_version` 列（迁移 `00005`），声明式同步（apply）从不碰它们。

- 规则（`internal/instrument/domain/profile.go`）：显示名 2–32 个可打印字符或留空（用资产名称）；简介每种语言最多 1,000 字；图标 PNG、SVG、WebP，正方形，最多 200 KB。SVG 上传时按白名单重建（只留图形、渐变、裁剪、文字等元素与外观属性），脚本、事件属性、外部引用、样式表都去掉。
- 每次修改 `profile_version` 加 1，`config_history` 记一行 `ASSET_PROFILE`：前后两份资料（图标记类型、大小与 sha256）、操作人与原因。
- 接口：gRPC `UpdateAssetProfile`（管理后台用；`GetAsset`/`ListAssets` 带 `profile`）；公开 `GET /v1/market/assets` 带 `display_name`、`description`、`links`、`logo_url`、`profile_version`，`GET /v1/market/pairs` 带 `base_display_name`、`base_logo_url`；图标 `GET /v1/market/assets/{code}/logo?v=<版本>`：版本是当前的就缓存一年（`immutable`），否则 60 秒，响应带 `nosniff` 与禁止执行的 CSP。
- 前端：`packages/core` 的 `markets/profiles` 把接口给的资料记下来，`coinProfile()` 把它盖在仓库的静态资料（`web/packages/core/assets/coins/`）上，显示名、简介、链接都优先用接口的；交易对与资产的查询每 60 秒刷新，所以改动一分钟内在三个站生效。
- 平台币：`scripts/ops/astra.sh profile` 写入默认资料与图标（`deploy/instruments/astra.svg`），`astra.sh open` 开放 ASTRA-USDT。ASTRA 是站内资产，不能充提；它的交易对不跟随币安（没有 `reference_symbol`），用户之间撮合（ADR-0015 第 6 条），HOUSE 不报价，机器人（market-sim）在批次 A2 加入。

## 平台资料（设计 2026-10-04 §4.1）

交易所自己的资料由后台改、站点运行时读取，上线只改配置、不重新构建前端。资料存在 `platform_profile`（迁移 instrument 00008，只有一行，迁移时写入当前的 Astras 默认值；00009 把学习模式改成测试模式；00010 把种子里「学习项目，资金为模拟」的页脚换成「© 2026 Astras」——页脚不随模式切换，上线后不能再这样说，运维自己改过的不动），上传的图片存在 `platform_images`。

- 字段：名称（2–32 字）、简称（2–12 字）、域名（PC 站主机名，未设为空）、主题色与品牌色（小写 `#rrggbb`）、页脚版权与合规文案、联系邮箱与客服链接（https）、社交链接（最多 10 个，种类见 `domain.SocialKinds`，只收 https）、默认语言、测试模式（开关、是否显示横幅、横幅文案，默认「测试模式」/ "Test mode"）、注册方式（`OPEN`/`CLOSED` 与关闭时的提示语）。文案一律按语言 `{"zh-CN", "zh-TW", "en"}` 存（繁体设计 2026-10-06：接口总带三种语言，繁体与英文空着时站点显示简体），默认语言可以是这三种之一（迁移 instrument 00012）。规则在 `internal/instrument/domain/platform.go`。
- 图片：`logo_light`、`logo_dark`（名称旁的标志，按主题选）、`favicon`、`apple_touch_icon`。都要正方形、最多 200 KB；标志可用 PNG、SVG、WebP，favicon 只收 PNG 或 SVG，苹果图标只收不小于 180 px 的 PNG。SVG 与资产图标一样按白名单重建。
- 每次修改（含换图、删图）版本加 1，`config_history` 记一行 `PLATFORM_PROFILE`：文字改动记前后两份，图片记种类、类型、大小、宽度与 sha256。删除一张不存在的图片什么也不改。
- 公开接口（`api/openapi/platform.yaml`，经网关）：
  - `GET /v1/platform/profile`：缓存 60 秒，`ETag` 随资料与赠送清单变化，带 `If-None-Match` 时返回 304。图片地址带版本号，没有上传的为 `null`，站点改用自带的图。
  - `GET /v1/platform/images/{kind}`：同资产图标，版本对得上缓存一年，否则 60 秒。
  - `GET /manifest.webmanifest`：手机站的 PWA 清单，按资料生成名称、简称、主题色与图标（用上传的 favicon 与苹果图标，没有时用站点自带的图标）。nginx 在上游不可用时退回构建产物里的静态清单。
- 内部接口（后台调用，网关不转发 `/internal`）：
  - `GET /internal/platform/profile`：同公开接口，多一个 `updated_by`。
  - `PUT /internal/platform/profile`：除图片与赠送外整体替换，带 `expected_version`、`actor`、`reason`；版本过期返回 409 `INSTRUMENT_PLATFORM_CHANGED`。
  - `PUT /internal/platform/images/{kind}`：`{data（base64）, mime, actor, reason}`。
  - `DELETE /internal/platform/images/{kind}`：`{actor, reason}`。
- 服务内缓存 5 秒，改动时立即清掉；站点每分钟读一次，所以改名、换图一分钟左右在三个站生效。
- `welcome_credits` 不由后台写：instrument-service 每分钟从 ledger-service 读一次（`LEDGER_SERVICE_URL`，见 [ledger.md](ledger.md#模拟资金阶段-1)），读失败就保留上次的值。
- 测试模式（`test_mode`，2026-10-04 用户决定由学习模式改名）：开着时站点只显示标为 TEST 或 BOTH 的内容、显示「测试模式」徽标，`banner` 为真时顶部显示横幅文案；关掉（上线）只显示 FORMAL 或 BOTH 的内容（设计 §4.4）。改名过渡期接口曾同时返回 `learning_mode`，后台改用 `test_mode`（a5a0d58）之后已去掉，`PUT` 必须带 `test_mode`。
- 读这份资料的还有：网关读注册方式，关闭时拒绝注册（见 [gateway.md](gateway.md#路由)）；notification-service 读名称，作为邮件与短信的署名（10 分钟缓存，读不到用 `Astras`），读测试模式，决定公开接口给站点哪种模式的文章（30 秒缓存，读不到沿用上次的，启动后没读到过时按正式模式）。

## App 下载（设计 2026-10-07 App 下载页，H1）

两站下载页读的 Android 与 iOS 安装方式，后台「平台设置 → App 下载」改（见 [admin.md](admin.md#app-下载)）。存在 `platform_apps`（迁移 instrument 00013，每个平台一行，初始为 `OFF`、版本 1）：模式 `OFF`/`LINK`/`FILE`、外部链接（https，最多 500 字符）、启用开关、三语版本说明（各最多 1000 字）、当前安装包 `current`、iOS 的配置描述文件 `mobileconfig` 与该平台保留的全部文件 `files`（最新在前，最多 10 个）。文件本身在服务器磁盘上（admin-service 写，nginx 以 `/downloads/` 提供），这里只记它们的 ID、原名、大小、SHA-256、在 `/downloads/` 下的路径（`stored_as`，.ipa 另有清单 `manifest`）、上传时的站点 `origin` 与包里读出的版本。规则在 `internal/instrument/domain/apps.go`。

- 每次改动（设置、记入文件、删除文件）版本加 1，`config_history` 记一行 `PLATFORM_APP`（键为平台，前后两份，文件按 ID）。
- 地址：文件的 `url` 为 `https://<平台资料的域名>/downloads/<stored_as>`，域名为空时用上传时的站点；iOS 的 `install_url` 为 `itms-services://?action=download-manifest&url=<清单地址>`（清单里写死上传时的地址，改域名后要重新上传 .ipa）。
- 下载入口开关（H5）：表 `platform_download_entry`（迁移 instrument 00014，一行，初始为开、版本 1）：两站的下载入口显示还是隐藏；切换时版本加 1，`config_history` 记一行 `DOWNLOAD_ENTRY`（键 `ENTRY`，前后两份），切到当前状态什么都不改。
- 公开接口（经网关）：`GET /v1/platform/apps` → `{android, ios, entry: {visible}}`，未启用、关闭、链接为空或安装包不在的平台为 `null`；缓存 60 秒，`ETag` 为 `"<Android 版本>-<iOS 版本>-<平台资料版本>-<下载入口版本>"`（文件地址随资料里的域名变；H5 之前是三个数；强标签，`If-None-Match` 带 `W/` 也认），没变时 304。
- 内部接口（后台调用，网关不转发 `/internal`）：
  - `GET /internal/platform/apps` → `{apps: [Android, iOS], entry}`，即后台的 `PlatformAppAdmin`（另带每个文件的 `stored_as`、`manifest`，供 admin-service 删除文件）与 `AppEntryAdmin`（`visible`、`version`、`updated_by`、`updated_at`）。
  - `PUT /internal/platform/download-entry`：`{visible, actor, reason}` → `{entry, previous}`：切换后与切换前（行锁下读到的）的下载入口开关，同状态时两者相同（后台按它审计，A89）。
  - `PUT /internal/platform/apps/{platform}`：`{mode, link_url, notes, enabled, expected_version, actor, reason}`；版本过期 409 `INSTRUMENT_PLATFORM_CHANGED`，`FILE` 没有安装包时 400。
  - `POST /internal/platform/apps/{platform}/files`：`{file: {file_id, kind, name, size, sha256, stored_as, manifest, origin, package, version, build, min_os, uploaded_at, uploaded_by}, actor, reason}` → `{app, replaced}`：安装包成为当前文件、模式改为 `FILE`；配置描述文件替换旧的（`replaced` 为被替换的，文件由调用方删除）；超过 10 个 409 `PLATFORM_APP_FILES_FULL`。
  - `DELETE /internal/platform/apps/{platform}/files/{file_id}`：`{actor, reason}` → `{app, removed}`：删的是当前安装包时有链接回到 `LINK`，否则 `OFF`；文件由调用方删除。
- 服务内缓存 5 秒，改动时立即清掉。

## 常用命令

```bash
ssh exchange sudo docker exec exchange-infra-instrument-service-1 /app/exchangectl instruments list
ssh exchange sudo docker exec exchange-infra-instrument-service-1 /app/exchangectl instruments pair-status BTC-USDT --to HALT --reason "行情异常"
ssh exchange sudo docker exec exchange-infra-instrument-service-1 /app/exchangectl instruments contract-status BTC-USDT-PERP --to TRADING --reason "合约上线"
# 看或改资产资料：只改给出的部分；图标从文件或标准输入（-）读
ssh exchange sudo docker exec exchange-infra-instrument-service-1 /app/exchangectl instruments profile ASTRA
ssh exchange sudo docker exec -i exchange-infra-instrument-service-1 /app/exchangectl instruments profile ASTRA --display-name Astra --logo - --logo-type image/svg+xml --reason "新图标" < astra.svg
# 简介：--zh 简体、--zh-tw 繁体（空字符串清除，繁体页面改显示简体）、--en 英文
ssh exchange sudo docker exec exchange-infra-instrument-service-1 /app/exchangectl instruments profile ASTRA --zh-tw "ASTRA 是 Astras 的平台幣……" --reason "繁体简介"
```

历史：

```sql
SELECT entity, key, version, source, actor, reason, created_at FROM instrument.config_history ORDER BY id DESC LIMIT 20;
```

## 接口

- 公开 REST（经网关，无需登录，`Cache-Control: public, max-age=10`）：`GET /v1/market/assets`（含网络）、`GET /v1/market/pairs`（不含已下线）、`GET /v1/market/pairs/{symbol}`（含已下线，大小写不敏感）、`GET /v1/market/contracts`、`GET /v1/market/contracts/{symbol}`（永续合约与风险限额阶梯；列表默认只有线性合约，`?margin_type=COIN` 列币本位、`ALL` 列全部，单个合约不分类型）。交易对另带基础资产的 `base_name`、`rank`、`categories`，以及由 tick/lot 算出的显示位数 `price_decimals`、`qty_decimals`。
- gRPC `InstrumentService`（`instrument-service:9184`）：`GetAsset`、`ListAssets`、`GetTradingPair`、`ListTradingPairs`、`GetContract`、`ListContracts`（`margin_type` 同上，缺省只有线性合约：币本位上线前的客户端不会读到它们，支持币本位的服务从 G1/G2 起显式要 `COIN` 或 `ALL`），供账本、交易、合约等服务校验精度与状态；`SetPairStatus`、`SetContractStatus`、`UpdateAssetProfile`、`ExportConfig`、`ApplyConfig` 供管理后台。
- 测试服的两个合约 `BTC-USDT-PERP`、`ETH-USDT-PERP` 用费率档 `perp`（maker 0.02%、taker 0.05%），创建时为 `PREPARE`，合约服务上线后再改 `TRADING`。币本位的 `BTC-USD-PERP`、`ETH-USD-PERP`、`ASTRA-USD-PERP`（2026-10-06 G0：面值 100/10/10 美元，tick 同 U 本位，BTC、ETH 的阶梯按币安 COIN-M 当时公示的 10 档，ASTRA 沿用其 U 本位阶梯、按 ASTRA 计）同用 `perp`，在 `PREPARE` 等合约服务（G1）、行情（G2）与两站（G4）落地后再开。
- 端到端检查：`scripts/e2e/market.sh`（`task e2e` 一起跑）。
