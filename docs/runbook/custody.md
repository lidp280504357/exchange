# 托管钱包（优盾）：充值、提现、回调与对账

ADR-0011，设计稿 `docs/设计-体验重构与市场钱包扩展-2026-09-30.md` §9，阶段 4 B6。自建钱包（Sepolia、signer、扫描、归集）见 [wallet.md](wallet.md)，两种模式按网络并存。

## 组成

```
instrument-service 网络行：provider=UDUN，provider_coin=主链编码:币种编码
        │
        ▼
wallet-service ──请求（信封 timestamp/nonce/sign/body，sign=md5(body+key+nonce+timestamp)）──> 优盾网关（测试服：udun-mock）
   │  建地址 /mch/address/create、提现 /mch/withdraw、地址校验 /mch/check/address、币种与余额 /mch/support-coins
   │
   │<──回调（表单或 JSON，同一签名）── POST https://astras.vip/v1/wallet/callbacks/udun（网关公开路由，不需登录）
   ▼
custody_callbacks（原文、验签、结果、次数）──> 充值：deposits CONFIRMED ──> DepositConfirmed ──> 账本 DEPOSIT_CREDIT
                                          └─> 提现：SUBMITTED ──> CONFIRMED（结算 WITHDRAW_SETTLE）或 FAILED（解冻）
```

- 代码：协议 `internal/platform/udun`（信封、签名、客户端、回调解析，wallet-service 与模拟网关共用）；端口 `internal/wallet/ports.Custody`；适配器 `internal/wallet/adapters/custody`；应用 `internal/wallet/application/{custody.go,custodyprocessor.go}`；模拟网关 `cmd/udun-mock`。
- 接口与字段按优盾官方 Go SDK（github.com/0xcregis/udun-sdk-go）：请求体是 JSON 字符串，信封里 `timestamp`（秒）和 `nonce` 是数字；建地址、提现、地址校验的 `body` 是数组，`support-coins` 是对象；应答 `{code, message, data}`，`code` 200 为成功，4165 非法地址，4288 重复的 `businessId`。
- 回调：`tradeType` 1 充值、2 提现；`status` 0 待审核、1 审核通过、2 审核拒绝、3 成功、4 失败；`amount`、`fee` 是整数，实际值 = 值 ÷ 10^`decimals`。时间戳秒或毫秒都认（13 位按毫秒），与当前相差超过 5 分钟拒绝。应答正文 `success`；其它应答（含 401/500）托管方会重试。

## 配置

| 变量（apps.env） | 说明 |
|---|---|
| `UDUN_GATEWAY_URL` | 网关地址；不设则托管网络既不能充值也不能提现（页面照常显示网络，申请地址返回 `WALLET_UNAVAILABLE`） |
| `UDUN_MERCHANT_ID` | 商户号 |
| `UDUN_API_KEY` | 签名密钥（请求与回调共用），只在本地 `.env` 与服务器 `apps.env` |
| `UDUN_WALLET_ID` | 可选，商户下的钱包 |
| `UDUN_CALLBACK_URL` | 托管方回调地址：真网关用 `https://astras.vip/v1/wallet/callbacks/udun`；测试服模拟网关用 `http://api-gateway:8080/v1/wallet/callbacks/udun`（内网，经网关） |
| `UDUN_CALLBACK_ALLOWED_IPS` | 可选，托管方出口 IP（逗号分隔，可写 CIDR）；空则不限来源，只靠签名 |
| `WALLET_CUSTODY_INTERVAL` | 托管方处理周期，默认 5 秒 |

网络在 `deploy/instruments/test.json`（`exchangectl instruments apply` 同步）：

| 资产 | 网络 | 显示名 | provider_coin | 确认数 | 最小充 | 最小提 | 手续费 |
|---|---|---|---|---|---|---|---|
| USDT | TRON | TRC20 | `195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t` | 20 | 1 | 10 | 1 |
| USDT | BSC | BEP20 | `9006:0x55d398326f99059fF775485246999027B3197955` | 15 | 1 | 10 | 0.5 |
| USDT | ETH | ERC20 | `60:0xdAC17F958D2ee523a2206206994597C13D831ec7` | 12 | 5 | 20 | 5 |
| BTC | BTC | Bitcoin | `0:0` | 2 | 0.0001 | 0.001 | 0.0002 |
| ETH | ETH | Ethereum | `60:60` | 12 | 0.002 | 0.005 | 0.001 |

同一网络的资产共用一个地址（每用户每网络一个，托管方按主链生成）：USDT-ERC20 与 ETH 都在网络 `ETH`。ETH 另有自建的 `ETH-SEPOLIA`。

**BSC 的主链编码 9006 未经托管方确认**（公开资料没有，取自 SLIP-44）。拿到商户号后在后台「托管方」页看托管方返回的币种（`support-coins` 原样列出），按它改 `provider_coin` 再部署。

## 流程

**充值**：首次申请地址时，wallet-service 在事务里加咨询锁、向托管方建地址（`alias` = `用户ID:网络`）并落库 `deposit_addresses`（`provider=UDUN`，没有派生序号），发 `DepositAddressAssigned`。托管方确认后回调 `status=3`：按 `provider_coin` 找网络、按地址找用户，记一笔 `CONFIRMED` 充值（键 `provider_tx_id = UDUN:<tradeId>`，同一笔交易付给多个地址也各记一笔），发 `DepositDetected`；处理器（5 秒一轮）照常检查资格后发 `DepositConfirmed`，账本记 `DEPOSIT_CREDIT`。低于最小额的记 `UNCLAIMED_DEPOSIT`；按资产精度截断后为 0 的记 `REJECTED`。`status` 不是 3 的充值回调只记录（`IGNORED`）。

**提现**：申请、地址簿、冷却、限额、风控、审批与自建模式相同；地址按网络格式校验（TRON Base58Check、比特币 bech32/Base58、EVM 校验和），再问托管方 `/mch/check/address`（托管方不可达时只用本地规则）。批准后处理器先把提现改为 `SUBMITTED`（从此不能撤销，发 `WithdrawalSubmitted`），再调 `/mch/withdraw`（`businessId` = 提现 ID）：
- 成功或 4288（托管方已有）→ 托管方状态 `ACCEPTED`；
- 4000–4999 的业务拒绝 → `FAILED`（原因 `CUSTODY_REFUSED: code …`），立即解冻；
- 网络错误等结果未知 → 保持 `SUBMITTED`/`SUBMITTED`，1 分钟后重交（重复的 `businessId` 被托管方拒绝，所以重交无害）。

回调 `status` 0/1 只更新托管方状态（`REVIEW`/`APPROVED`）；3 → `CONFIRMED`、记交易哈希，并立即结算 `WITHDRAW_SETTLE`（处理器兜底重试）；2 或 4 → `FAILED`（原因 `CUSTODY_REJECTED`、`CUSTODY_FAILED: <txId>`），处理器解冻。托管方结算前资金一直冻结在用户账户，失败时不需要冲正。回调里的 `fee`（托管方向平台收的费）记入 `chain_fees`，像自建模式的 gas 一样从 `GAS_SUPPLY` 入账；`fee` 按提现币种计，以正式文档为准。

**回调**：先把原文（最多 16 KiB）、验签与时间检查结果记入 `custody_callbacks`，验签通过的同一（provider, tradeId, status）只记一行，托管方重试只加 `attempts`；再在一个事务里应用并记结果：

| 结果 | 含义 |
|---|---|
| `APPLIED` | 已入账 / 已改状态 |
| `IGNORED` | 无需处理（充值的审核中状态、重复、晚到的审核回调） |
| `UNMATCHED` | 找不到网络、地址或提现，回 `success` 停止重试，等人工处理（告警） |
| `REJECTED` | 验签失败、时间超窗或格式不对，回 401/400，不处理 |
| `FAILED` | 应用时出错，回 500 让托管方重试，也可在后台重放 |

后台「托管方」页可以重放 `FAILED`、`UNMATCHED`、`RECEIVED` 的回调（重新验签但不查时间，写审计 `wallet.custody.callback.replay`）。

## 对账（不变量 4 的托管版）

每小时（`exchangectl wallet reconcile --network UDUN` 立即）按资产比较：

```
短缺 = 账本应有 −(DEPOSIT_PENDING + WITHDRAWAL_PENDING) − 托管方余额 − 其它持有方 − 在途提现 − 未入账手续费
```

- 托管方余额：`/mch/support-coins` 各币种余额之和（USDT 三条链合计）。
- 其它持有方：资产的另一种保管方式此刻持有的数额。测试服的 ETH 同时在 Sepolia 自建钱包与托管方：托管方的检查把 Sepolia 热钱包与充值地址的链上余额算作"其它持有方"，Sepolia 的链上检查（[wallet.md](wallet.md)）把托管方余额算作"其它持有方"，两边都看整笔资产。
- 在途提现：`SUBMITTED` 的提现金额（托管方可能已发出、回调未到，账本还没结算）。
- 结果写 `chain_checks`（`network = UDUN`，多了 `elsewhere`、`in_flight` 两列），`exchangectl wallet checks --network UDUN` 或后台「托管方」页查看。

## 测试服的模拟网关 udun-mock

容器 `udun-mock`（REST 8097、运维 9097），与 wallet-service 共用 `UDUN_MERCHANT_ID`、`UDUN_API_KEY`（服务器 `apps.env`），状态在 `/opt/exchange/infra/udun-mock/state.json`（地址、余额、提现、待发回调；属主 uid 10001，部署脚本创建）。行为：
- 地址按链生成（BTC bech32 主网、TRON Base58Check、其余 EVM 校验和），本地格式校验，余额按充值加、提现减，失败的提现退回余额；
- 提现在 `UDUN_MOCK_STEP`（2 秒）后回调审核通过、再过 2 秒回调成功；回调没收到 `success` 时按 5 秒、10 秒、20 秒……最长 10 分钟重试；
- 只在内网，nginx 不转发；控制命令在容器里执行：

```bash
ssh exchange
cd /opt/exchange/infra
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T udun-mock /app/udun-mock state
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T udun-mock /app/udun-mock deposit --address T... --coin 195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t --amount 25
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T udun-mock /app/udun-mock outcome --address T... --status 4
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T udun-mock /app/udun-mock delay --seconds 600
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T udun-mock /app/udun-mock replay --trade TRADE_ID --age 600
```

`deposit` 模拟一笔到账（地址必须是模拟网关生成的），`outcome` 设置发往某地址的提现结局（2 拒绝、3 成功、4 链上失败），`delay` 让回调延后（故障演练），`replay` 重发一条已送达的回调（`--age` 签成若干秒前、`--forge` 用错误密钥签）。**不要删 state.json**：模拟网关余额与账本对不上，对账会一直报短缺。

## 换成真网关

1. 托管方后台登记回调地址 `https://astras.vip/v1/wallet/callbacks/udun`，把服务器出口 IP 加白名单。
2. `apps.env` 改 `UDUN_GATEWAY_URL`、`UDUN_MERCHANT_ID`、`UDUN_API_KEY`、`UDUN_CALLBACK_URL`，填 `UDUN_CALLBACK_ALLOWED_IPS`；重启 wallet-service。
3. 后台「托管方」页核对托管方返回的币种编码，改 `deploy/instruments/<环境>.json` 的 `provider_coin`。
4. 先关 `wallet.withdraw`，小额充值每个"资产·网络"一笔、对账通过后再逐个开提现；回调日志保留全量。
5. 去掉 compose 里的 `udun-mock`。

## 指标与告警

- `wallet_custody_up`、`wallet_custody_balance{coin}`（5 分钟）、`wallet_custody_held/expected/shortfall{asset}`（每次对账）、`wallet_custody_submitted`、`wallet_custody_submitted_oldest_seconds`、`wallet_custody_callbacks_attention`、`wallet_custody_deposits_held`、`wallet_custody_fees_unbooked`，常量标签 `provider`。
- 告警（`deploy/observability/alerts.yml`）：`CustodyShortfall`（短缺 15 分钟，严重）、`CustodyUnreachable`（10 分钟）、`CustodyWithdrawalStuck`（`SUBMITTED` 超过 24 小时，人工到托管方后台核对）、`CustodyCallbacksNeedAttention`（15 分钟）、`CustodyFeesUnbooked`（1 小时）。

## 端到端

`scripts/e2e/custody.sh`：新用户拿 TRC20 与比特币地址；模拟网关报 30 USDT 到账（入账一次，重试与重放不重复），0.5 USDT 记未入账；伪造签名与过期回调被拒并记录（含一条经公网发到 `https://astras.vip` 的伪造回调）；绑定身份验证器后 12 USDT 经审批交给托管方、`SUBMITTED` → `CONFIRMED` 带交易哈希并结算，10 USDT 发往失败地址 → `FAILED` 资金退回；最后对账无短缺。浏览器冒烟测试的充值页同时取 Sepolia 与 TRC20 地址，后台冒烟测试打开「托管方」页与一条回调。

## 已知局限

- 公开文档没有提现查询与充值列表接口：提现以回调为准（`SUBMITTED` 超 24 小时告警），漏掉的充值回调靠托管方重试与人工核对。
- 托管方没有测试环境：真网关的应答码、余额格式、`fee` 的币种以正式文档与小额联调为准。
