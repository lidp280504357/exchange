# 资产、网络与交易对运维

需求 §5.5、§11.2、§11.3；实现见 `internal/instrument`，契约 `api/proto/exchange/instrument/v1`（gRPC 与 `instrument.events`）、`api/openapi/market.yaml`（公开 REST）。

## 数据与规则

| 对象 | 关键字段 | 约束 |
|---|---|---|
| 费率档 `fee_schedules` | tier、maker/taker 费率 | 费率 ≥ 0 且 < 10%；默认档 `default` 为 0.1% / 0.1% |
| 资产 `assets` | 代码、名称、`decimals`、可充/可提/可交易、风险开关 | 代码 2–10 位大写字母数字；`decimals` 0–18，**设置后不可改** |
| 网络 `networks` | 资产 × 链、合约地址、确认数、最小充/提、提现手续费、Memo | 金额精度不超过资产 `decimals` |
| 交易对 `trading_pairs` | tick/lot、最小/最大数量、最小名义金额、价格保护带、费率档、状态 | tick 精度 ≤ 报价资产，lot 精度 ≤ 基础资产；tick × lot 的精度 ≤ 报价资产（成交额、冻结额因此总是精确值，2026-09-28 起 BTC-USDT 的 lot 改为 0.0001、ETH-BTC 改为 0.001）；最小/最大数量是 lot 的整数倍；保护带 (0, 1] |
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

- 公开 REST（经网关，无需登录，`Cache-Control: public, max-age=10`）：`GET /v1/market/assets`（含网络）、`GET /v1/market/pairs`（不含已下线）、`GET /v1/market/pairs/{symbol}`（含已下线，大小写不敏感）、`GET /v1/market/contracts`、`GET /v1/market/contracts/{symbol}`（永续合约与风险限额阶梯）。
- gRPC `InstrumentService`（`instrument-service:9184`）：`GetAsset`、`ListAssets`、`GetTradingPair`、`ListTradingPairs`、`GetContract`、`ListContracts`，供账本、交易、合约等服务校验精度与状态；`SetPairStatus`、`SetContractStatus` 供管理后台。
- 测试服的两个合约 `BTC-USDT-PERP`、`ETH-USDT-PERP` 用费率档 `perp`（maker 0.02%、taker 0.05%），创建时为 `PREPARE`，合约服务上线后再改 `TRADING`。
- 端到端检查：`scripts/e2e/market.sh`（`task e2e` 一起跑）。
