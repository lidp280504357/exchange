# 资产、网络与交易对运维

需求 §5.5、§11.2、§11.3；实现见 `internal/instrument`，契约 `api/proto/exchange/instrument/v1`（gRPC 与 `instrument.events`）、`api/openapi/market.yaml`（公开 REST）。

## 数据与规则

| 对象 | 关键字段 | 约束 |
|---|---|---|
| 费率档 `fee_schedules` | tier、maker/taker 费率 | 费率 ≥ 0 且 < 10%；默认档 `default` 为 0.1% / 0.1% |
| 资产 `assets` | 代码、名称、`decimals`、可充/可提/可交易、风险开关；`rank`（市值排名，0 为无）、`categories`（板块标签，如 `layer-1`、`defi`、`meme`，最多 8 个） | 代码 2–10 位大写字母数字；`decimals` 0–18，**设置后不可改**；标签 2–24 位小写字母数字或 `-`，不重复 |
| 网络 `networks` | 资产 × 链、合约地址、确认数、最小充/提、提现手续费、Memo；`display_name`（用户看到的名字，如 TRC20、ERC20）、`address_format`（`EVM`/`TRON`/`BTC`，缺省 EVM）、`eta_minutes`（通常到账分钟数）、`explorer_tx_url`/`explorer_address_url`（带 `{tx}`/`{address}` 占位的 https 链接） | 金额精度不超过资产 `decimals` |
| 交易对 `trading_pairs` | tick/lot、最小/最大数量、最小名义金额、价格保护带、费率档、状态；`reference_symbol`（跟随的币安符号，空表示不跟随）、`reference_multiplier`（价格倍数，缺省 1）、`listed_at`（上架时间，缺省为创建时刻，文件里不写就保持） | tick 精度 ≤ 报价资产，lot 精度 ≤ 基础资产；tick × lot 的精度 ≤ 报价资产（成交额、冻结额因此总是精确值，2026-09-28 起 BTC-USDT 的 lot 改为 0.0001、ETH-BTC 改为 0.001）；最小/最大数量是 lot 的整数倍；保护带 (0, 1]；倍数是 1 到 10^9 之间 10 的整数次幂 |
| 永续合约 `contracts`（阶段 3） | 代号 `BASE-QUOTE-PERP`、指数所用现货市场、tick/lot、数量与名义金额限制、限价偏离标记价的保护带、风险限额阶梯（每档最大名义价值、最高杠杆、维持保证金率）、资金费间隔/利率/上限、冲击名义金额、费率档、状态 | 同交易对的精度规则；阶梯按名义价值递增、杠杆不升、维持保证金率不降且低于 1/杠杆（否则开仓即强平）；资金费间隔 1/4/8 小时；状态机与交易对相同，状态只能经 `contract-status` 改 |

金额一律 `NUMERIC(38,18)` 与十进制字符串（ADR-0008）；超精度直接拒绝，不做舍入。每次变更版本号加一，并在 `config_history` 追加一行（值、操作者、原因），同事务经 outbox 发 `instrument.events`（`AssetUpserted`、`NetworkUpserted`、`TradingPairUpserted`、`TradingPairStatusChanged`、`ContractUpserted`、`ContractStatusChanged`、`FeeScheduleChanged`）。变更立即生效；预定生效时间留到管理后台（阶段 2）。

交易对状态（附录 B）：`PREPARE → TRADING ↔ HALT`；`TRADING/HALT → CANCEL_ONLY → DELISTED`，其他变更返回 409 `INSTRUMENT_STATUS_TRANSITION_INVALID`。

## 声明式同步（幂等）

`deploy/instruments/test.json` 是测试环境参考数据的来源，`deploy/server-update.sh` 每次部署都执行：

```bash
sudo docker compose ... exec -T instrument-service /app/exchangectl instruments apply --file - --reason "deploy <提交>" < deploy/instruments/test.json
```

- 整个文件在一个事务里生效：缺的创建，变了的升版本并发事件，相同的跳过——同一文件重复执行不产生任何变更。任何一项校验失败则全部回滚。
- 文件里没有的对象保留不动（不会删除）。
- 交易对的状态只在**创建时**取文件中的值（默认 `PREPARE`）；之后只能用 `pair-status` 改，部署不会把状态改回去。

改参考数据：改 `deploy/instruments/test.json` 并提交，下次部署生效；紧急情况可在服务器上手工执行同一命令。

### 主流 50 币（阶段 4 B4，设计稿 §8.5，ADR-0013、ADR-0014）

- 前 50 个币与 2026-10-02 的扩展名单（用户要求再上一批知名币，只做现货、站内资产：37 个，TON 因币安现货暂停交易而跳过；单价低于 0.001 USDT 的 BONK、FLOKI 以 1000 个计价）各有一个资产与一个 USDT 交易对，都跟随币安现货（`reference_symbol`），由 `go run deploy/instruments/gen-top50.go` 生成进 `test.json`（扩展名单里币安没有在交易的 USDT 对就跳过；已上架的交易对保留原有的 tick、lot 与数量上下限，免得挂单落在新步长之外）：它读一次币安的交易规则与价格，价格步长取币安的（乘以倍数，至少 0.000001）、数量步长取币安的（除以倍数）并加粗到 tick × lot 最多 6 位小数，最小名义金额 5 USDT、单笔最多值 100 万 USDT、价格保护带 10%、默认费率档；文件里的其他内容（费率档、USDT/BTC/ETH 与网络、ETH-BTC、SOL-BTC、合约）原样保留。
- 单价低于 0.001 USDT 的币按 1000 个计价（`1000SHIB` ↔ `SHIBUSDT`、`1000PEPE` ↔ `PEPEUSDT`、`1000BONK`、`1000FLOKI`，倍数 1000，ADR-0014）；0.001 到 0.01 之间的（ZIL、GALA、PENGU 等）按 1 个计价，tick 为 USDT 精度 0.000001。
- 除 USDT、BTC、ETH 外的资产（前 50 里 47 个，加扩展的 37 个）都是**内部资产**：没有网络、不能充提（ADR-0013），只能在平台上交易；精度 6 到 8 位，取决于数量步长。
- 设计稿名单里的 TON 在币安现货已停止交易（2026-10-01 查询为 `BREAK`），换成 HYPE（Hyperliquid）。
- 新交易对以 `PREPARE` 创建，HOUSE 流动性就绪后用 `scripts/ops/house.sh open` 统一开放（见 [market-maker.md](market-maker.md)）。`SOL-BTC` 故意一直保持 `PREPARE`，端到端 `trading.sh` 用它检查"未开放的交易对不能下单"。
- 币的介绍资料（前端详情页与字母图标的颜色）在 `web/packages/core/assets/coins/<代码>.json`（1000 倍币用去掉前缀的代码，如 `BONK.json`）；上新币时一起补上。
- `internal/instrument/application/testdata_test.go` 在不连库的情况下按 apply 的规则校验整个文件（至少前 50 个 USDT 交易对、交易对不重复、内部资产无网络、1000 倍币的倍数）。

## 常用命令

```bash
ssh exchange sudo docker exec exchange-infra-instrument-service-1 /app/exchangectl instruments list
ssh exchange sudo docker exec exchange-infra-instrument-service-1 /app/exchangectl instruments pair-status BTC-USDT --to HALT --reason "行情异常"
ssh exchange sudo docker exec exchange-infra-instrument-service-1 /app/exchangectl instruments contract-status BTC-USDT-PERP --to TRADING --reason "合约上线"
```

历史：

```sql
SELECT entity, key, version, actor, reason, created_at FROM instrument.config_history ORDER BY id DESC LIMIT 20;
```

## 接口

- 公开 REST（经网关，无需登录，`Cache-Control: public, max-age=10`）：`GET /v1/market/assets`（含网络）、`GET /v1/market/pairs`（不含已下线）、`GET /v1/market/pairs/{symbol}`（含已下线，大小写不敏感）、`GET /v1/market/contracts`、`GET /v1/market/contracts/{symbol}`（永续合约与风险限额阶梯）。交易对另带基础资产的 `base_name`、`rank`、`categories`，以及由 tick/lot 算出的显示位数 `price_decimals`、`qty_decimals`。
- gRPC `InstrumentService`（`instrument-service:9184`）：`GetAsset`、`ListAssets`、`GetTradingPair`、`ListTradingPairs`、`GetContract`、`ListContracts`，供账本、交易、合约等服务校验精度与状态；`SetPairStatus`、`SetContractStatus` 供管理后台。
- 测试服的两个合约 `BTC-USDT-PERP`、`ETH-USDT-PERP` 用费率档 `perp`（maker 0.02%、taker 0.05%），创建时为 `PREPARE`，合约服务上线后再改 `TRADING`。
- 端到端检查：`scripts/e2e/market.sh`（`task e2e` 一起跑）。
