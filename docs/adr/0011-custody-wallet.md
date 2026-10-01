# ADR-0011：充提接入第三方托管钱包（优盾），替代自建链适配

- 状态：已接受（2026-10-01；用户 2026-09-30 决定充提改接托管钱包，托管方为优盾，无测试环境）
- 关联：设计文档 `docs/设计-体验重构与市场钱包扩展-2026-09-30.md` §9；[ADR-0001](0001-ledger-double-entry.md)（余额只经账本）、[ADR-0003](0003-signing-isolation.md)（私钥只在 signer）、[ADR-0005](0005-feature-flags.md)

## 背景

阶段 2 的钱包是自建的：signer 持有 HD 种子，wallet-service 派生地址、扫描 Sepolia、签名广播提现、归集与链上对账。它只覆盖一条 EVM 链。阶段 4 要支持 USDT（TRC20、BEP20、ERC20）、BTC、ETH 的充提，自建就要接 TRON、BSC、比特币的节点、地址格式、签名、归集与重组处理，工作量和运维风险都远超学习项目的范围。用户决定改接第三方托管钱包：地址生成、链上监听、签名与广播都由托管方完成，平台只做账本、风控、审批与对账。

## 决策

1. **网络选择托管方**：instrument-service 的网络增加 `provider`（空 = 自建，`UDUN` = 优盾）与 `provider_coin`（托管方的币种代码，`主链代码:代币代码`，例如 USDT-TRC20 为 `195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t`）。wallet-service 按网络的 `provider` 选择自建适配器或托管适配器；其余流程（地址簿、冷却、限额、风控、审批、step-up、站内地址）不分模式。
2. **端口与适配器**：`internal/wallet/ports.Custody`（建充值地址、提交提现、校验地址、读币种与余额、解析回调）；协议放在 `internal/platform/udun`（信封、签名、客户端、回调），适配器 `internal/wallet/adapters/custody` 把它接到端口上。请求与字段按优盾官方 Go SDK：信封四个字段 `timestamp`（秒）、`nonce`、`sign`、`body`（请求的 JSON 字符串），`sign = md5(body + key + nonce + timestamp)`；建地址、提现、地址校验的 `body` 是数组。
3. **没有测试环境时的验证**：测试服运行模拟网关 `udun-mock`（`cmd/udun-mock`）：同样的接口、信封、签名与回调，另有只在内网可达的 `/mock/deposit` 模拟一笔到账；提现收到后先回调"审核通过"再回调"成功"。wallet-service 的 udun 适配器原样走全部代码路径，接真网关只换 `UDUN_GATEWAY_URL`、`UDUN_MERCHANT_ID`、`UDUN_API_KEY`、`UDUN_CALLBACK_URL`。
4. **回调**：`POST /v1/wallet/callbacks/udun`（网关公开路由，不要登录；nginx 可按托管方出口 IP 限制）。验签、时间戳在 ±5 分钟内、以（`tradeId`，`status`）幂等；原文、验签与处理结果全部记入 `custody_callbacks`，后台可以重放单条。处理失败返回非 200，让托管方重试。
5. **充值**：托管方确认后回调（`status=3`）即 `CONFIRMED`，照常发 `DepositConfirmed`，由账本记 `DEPOSIT_CREDIT`（对手 `DEPOSIT_PENDING`）；托管方的 `tradeId` 记为 `provider_tx_id`（唯一）。USDT 任一链充入都记同一个 USDT 余额，充值记录保留网络。低于最小额的照旧记入 `UNCLAIMED_DEPOSIT`。
6. **提现**：`APPROVED` 后提交托管方，`businessId` = 提现 ID（托管方拒绝重复，天然幂等），进入新状态 `SUBMITTED`（托管方处理中，含其自己的复核）。回调 `status=3` → `CONFIRMED`，记 `txId` 并结算（`WITHDRAW_SETTLE`）；`2`（托管方拒绝）或 `4`（链上失败）→ `FAILED`，解冻。自建模式在广播时结算，托管模式在成功回调时才结算：托管方失败时资金还冻结在用户账户里，不需要冲正。
7. **对账**：每小时读托管方各币种余额，按资产与账本认为应在链上的数额 `−(DEPOSIT_PENDING + WITHDRAWAL_PENDING)` 比较（不变量 4 的托管版），记入 `chain_checks`，短缺进指标与告警。账本的数额覆盖资产的全部持有方：同一资产同时在自建钱包（测试服的 Sepolia ETH）与托管方时，各自的检查把对方此刻的持有算作"其它持有方"；`SUBMITTED` 的提现算作在途（托管方可能已发出、账本尚未结算）。
8. **自建模式保留**：ETH-SEPOLIA 的自建充提（signer、扫描、归集、链上对账）作为备用与现有端到端测试继续运行，不再扩展到新链。

## 理由

- 学习项目的重点在交易、账本与风控，自建多链钱包的投入与风险不成比例；托管方把私钥与链上操作挪出平台，平台更简单也更安全（ADR-0003 的"私钥只在 signer"在托管模式下变为"私钥不在平台"）。
- 端口把托管方隔离在一个适配器里：换托管方或回到自建，只影响适配器与网络配置。
- 模拟网关实现同一协议，适配器的签名、信封、回调验签在测试服每天都被走到，真网关联调时只剩配置与托管方侧的差异。

## 后果

- 新增状态 `SUBMITTED`（需求附录 B、前端时间线"托管方处理中"）；`deposit_addresses` 的派生序号对托管地址为空；去掉地址与交易哈希只允许 `0x` 的约束。
- 托管方的密钥（`UDUN_API_KEY`）只在本地 `.env` 与服务器 `apps.env`。
- 托管方没有提现查询与交易列表接口（公开文档）：提现以回调为准，`SUBMITTED` 超过 24 小时未回调告警，人工到托管方后台核对；漏掉的充值回调靠托管方重试与人工核对。
- 托管方余额对账依赖托管方的余额接口；账本仍是用户余额的唯一事实来源。

## 备选

- 继续自建并扩展到 TRON、BSC、BTC：节点、签名、归集、重组处理都要自己做，否决。
- 把托管方当作"外部用户"、在账本里开一个托管方科目：与 `DEPOSIT_PENDING`/`WITHDRAWAL_PENDING` 的语义重复，对账更难，否决。
