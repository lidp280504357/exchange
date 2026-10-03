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
- 回调：`tradeType` 1 充值、2 提现；`status` 0 待审核、1 审核通过、2 审核拒绝、3 成功、4 失败；`amount`、`fee` 是整数，实际值 = 值 ÷ 10^`decimals`；没有 `decimals` 的回调按格式不对拒收（审查 ②）。时间戳秒或毫秒都认（13 位按毫秒），与当前相差超过 5 分钟拒绝。应答正文 `success`；其它应答（含 401/500）托管方会重试。

## 配置

| 变量（apps.env） | 说明 |
|---|---|
| `UDUN_GATEWAY_URL` | 网关地址；不设则托管网络既不能充值也不能提现（页面照常显示网络，申请地址返回 `WALLET_UNAVAILABLE`） |
| `UDUN_MERCHANT_ID` | 商户号 |
| `UDUN_API_KEY` | 签名密钥（请求与回调共用），只在本地 `.env` 与服务器 `apps.env`；至少 32 个字符（128 位随机，例如 32 位十六进制），短了 wallet-service 不启动 |
| `UDUN_WALLET_ID` | 可选，商户下的钱包 |
| `UDUN_CALLBACK_URL` | 托管方回调地址：真网关用 `https://astras.vip/v1/wallet/callbacks/udun`；测试服模拟网关用 `http://api-gateway:8080/v1/wallet/callbacks/udun`（内网，经网关） |
| `UDUN_CALLBACK_ALLOWED_IPS` | 托管方回调的出口 IP（逗号分隔，可写 CIDR）。测试服填容器网段 `172.18.0.0/16`（模拟网关在内网经网关回调）。**空即不限来源**（2026-10-03：优盾后台与文档都没有给出回调出口 IP）：只靠签名、时间戳（±5 分钟）与按"单号 + 状态"去重，启动时记 WARN；每条回调（被拒的也一样）把来源 IP 记进 `custody_callbacks.remote_ips`（同一条回调的重发来自新地址时追加，保留最新的 8 个），真回调来过几笔后从这里取地址再收紧（见下文「换成真网关」）。公网这一层另由 nginx 把关：回调路径（`/v1/wallet/callbacks/` 下任何托管方、不分大小写，按解码后的地址匹配）每个来源每秒 10 个、可突发 50 个（超过 429，托管方会重发），只放行 `deploy/compose/nginx/snippets/custody-callback-allow.conf` 里的 `allow` 地址，文件里没有地址时全部 403（审查 B3），写 `allow all;` 即不限来源；wallet-service 只认小写的托管方名，`/v1/wallet/callbacks/UDUN` 这类写法 404 |
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

**充值**：首次申请地址时，wallet-service 先向托管方建地址（`alias` = `用户ID:网络`；这一步不在事务里，最长等到请求超时），再在事务里加咨询锁、落库 `deposit_addresses`；同一用户同一网络的两个首次请求同时到达时，后落库的一方用先落库的地址，托管方多建的那个地址不给任何人看（审查 B7）。地址落库时 `provider=UDUN`、没有派生序号，同时发 `DepositAddressAssigned`。托管方确认后回调 `status=3`：按 `provider_coin` 找网络、按地址找用户，记一笔 `CONFIRMED` 充值（键 `provider_tx_id = UDUN:<tradeId>`，同一笔交易付给多个地址也各记一笔），发 `DepositDetected`；处理器（5 秒一轮）照常检查资格后发 `DepositConfirmed`，账本记 `DEPOSIT_CREDIT`。低于最小额的记 `UNCLAIMED_DEPOSIT`；按资产精度截断后为 0 的记 `REJECTED`。`status` 不是 3 的充值回调只记录（`IGNORED`）。

**提现**：申请、地址簿、冷却、限额、风控、审批与自建模式相同；地址按网络格式校验（TRON Base58Check、比特币 bech32/Base58、EVM 校验和），再问托管方 `/mch/check/address`（托管方不可达时只用本地规则）。批准后处理器先把提现改为 `SUBMITTED`（从此不能撤销，发 `WithdrawalSubmitted`），再调 `/mch/withdraw`（`businessId` = 提现 ID）：
- 成功或 4288（托管方已有）→ 托管方状态 `ACCEPTED`；
- 首次提交被 4000–4999 的业务拒绝 → `FAILED`（原因 `CUSTODY_REFUSED: code …`），立即解冻；
- 网络错误等结果未知 → 保持 `SUBMITTED`/`SUBMITTED`，1 分钟后重交（重复的 `businessId` 被托管方拒绝，所以重交无害）；
- 重交时被 4288 以外的理由拒绝 → **不解冻**：托管方可能已经收下第一次（例如第一次超时但已受理，重交时余额已被它用掉，返回"余额不足"），这时解冻就是双花。提现保持 `SUBMITTED`，托管方状态记为 `UNCERTAIN`（原因 `UNCERTAIN: code …`），不再重交，只等托管方的 2/3/4 回调；告警 `CustodyWithdrawalsUncertain`。一直交不出去也一样：第一次交接起 30 分钟内托管方对哪次交接都没有应答（每次都超时或网络错误），同样记为 `UNCERTAIN`（原因 `UNCERTAIN: no answer from the custodian in 30m0s of hand-overs`）、停止重交、不解冻，等它的回调或人工了结（569a958）。重交期间托管方的回调已经说收下了（`ACCEPTED`、`REVIEW`、`APPROVED`）的不改成 `UNCERTAIN`，以它的回调为准。这个原因是托管方的原话，只给后台看：用户的提现接口在提现被拒、取消或失败之前不返回 `reject_reason`（审查 2026-10-02）。人工到托管方后台核实后用 `exchangectl wallet custody-resolve <提现ID> --sent --tx <哈希> --reason …`（已发出，处理器结算）或 `--failed --reason …`（没发出，处理器解冻）了结；两种都记审计 `wallet.custody.withdrawal.resolve`（前后状态、交易哈希）。托管方状态还是 `SUBMITTED`（处理器每分钟重交、还没有答复）时 `--failed` 被拒（`WALLET_CUSTODY_HANDOVER_PENDING`）：这时判失败并解冻，之后某次重交被托管方收下就是双付。万一托管方在提现已失败解冻后才收下一次交接（回调与重交赛跑），处理器记错误日志并计入 `wallet_custody_withdrawal_contradictions_total`（告警 `CustodyWithdrawalContradiction`），人工到托管方后台核对。

回调 `status` 0/1 只更新托管方状态（`REVIEW`/`APPROVED`）；3 → `CONFIRMED`、记交易哈希，并立即结算 `WITHDRAW_SETTLE`（处理器兜底重试）；2 或 4 → `FAILED`（原因 `CUSTODY_REJECTED`、`CUSTODY_FAILED: <txId>`），处理器解冻。托管方结算前资金一直冻结在用户账户，失败时不需要冲正。

### 托管方的手续费（审查 ④，2026-10-03）

回调里的 `fee` 是托管方为发出这笔提现向平台收的费，记入 `chain_fees`，像自建模式的 gas 一样从 `GAS_SUPPLY` 入账（`GAS_SUPPLY` → `WITHDRAWAL_PENDING`，对账的"账本应有"随之减少）。正式文档只说 `fee` 与 `amount` 一样按 `decimals` 换算，没说代币提现的手续费按哪种币计（链上的 gas 是用链的主币付的：ERC20 USDT 的 gas 是 ETH，按 18 位算会差 10¹² 倍；TRC20 与 TRX 都是 6 位，按错了也看不出来）。所以每个"资产·网络"的手续费单位要人工确认：

| 单位 | 含义 | 怎么记账 |
|---|---|---|
| `SELF` | 按提现的币种、回调的 `decimals` 计（文档的读法） | 记在提现资产上；链的主币（`coinType` 等于 `mainCoinType`，`support-coins` 的 `tokenStatus` 0，如 BTC `0:0`、ETH `60:60`）不确认也按它 |
| `MAIN` | 按链的主币、主币的精度计（代币提现的 gas） | 记在平台在同一托管方持有的主币资产上（ERC20 → ETH 网络 `60:60` 的 ETH）；平台没有这个主币的托管网络（TRX、BNB）时挂起 |
| `OUTSIDE` | 不从对账的币种余额里扣（托管方另有手续费账户） | 不记账，回调结果注明 |

确认用 `exchangectl wallet custody-fee-unit --asset USDT --network TRON --unit SELF --reason "第一笔真实提现在 tronscan 上核对过"`（不带 `--unit` 列出已确认的单位；写 `custody_fee_units` 并记审计 `wallet.custody.fee_unit`）。**代币的单位没人确认之前，它的每笔手续费都挂起**，不记账。

确认了单位的手续费再看数额：超过"网络手续费（用户付的 `withdraw_fee`）× 5"与提现金额两者中较小的那个（`MAIN` 只看主币网络的 `withdraw_fee` × 5）也挂起；所看网络的 `withdraw_fee` 是 0 时没有可比的上限（只剩提现金额本身），一律挂起：错了单位的手续费通常差得远（按最小单位的 gas，或把 13.6 TRX 当成 13.6 USDT，而 TRC20 的网络手续费是 1 USDT）。入账金额按资产精度向上取整（托管方扣的是整笔，多记零头不会留下短缺）。

挂起的手续费状态 `HELD`，写明原因（报的数额、原始整数与 `decimals`、为什么挂起），不入账，也**不算进对账的"未入账手续费"**（不遮住短缺：托管方真扣了的话，对账会报这笔短缺，直到人工处理）。计数 `wallet_custody_fees_held_total`，现有数 `wallet_custody_fees_held`，告警 `CustodyFeesHeld`。人工到托管方后台或区块浏览器核对后：

```bash
exchangectl wallet custody-fees                                    # 挂起的手续费与原因
exchangectl wallet custody-fee <提现ID> --book --reason "..."       # 按报的数额入账
exchangectl wallet custody-fee <提现ID> --book --amount 1.5 [--asset ETH] --reason "托管方账单上是 1.5"   # 按实际扣的入账
exchangectl wallet custody-fee <提现ID> --write-off --reason "..."  # 不入账：没从对账的余额里扣，或单位报错、实际没收
```

`--book` 的资产必须是平台在该托管方、这笔手续费的网络上持有的资产（提现资产本身，或同一网络上链的主币，如 ERC20 的网络 `ETH` 上的 ETH），数额不能超过资产精度；处理器下一轮入账。挂起的计数与错误日志在回调的事务提交之后才记，事务失败、托管方重发时不重复计。两种都记审计（`wallet.custody.fee.book`、`wallet.custody.fee.write_off`，含报的数额、入账的数额与挂起原因）。没挂起、只是在等 `GAS_SUPPLY` 的手续费（`CustodyFeesUnbooked`）也可以 `--write-off`，用于没有收入可以注资的时候；万一账本恰好在核销的同时入了账，以入账为准（状态回到 `BOOKABLE`，日志记一条），账本说了算。

**`GAS_SUPPLY` 的钱从哪来**：托管模式下没有"平台转进热钱包"可以记（`wallet fund` 是自建钱包用的）；用户付的提现手续费（`FEE_REVENUE`）本来就在托管方的余额里，托管方的手续费就从这里出：`exchangectl ledger gas-supply --asset USDT --amount 20 --reason "..."` 把手续费收入挪到 `GAS_SUPPLY`（两边都不是钱包应有数，对账不变；不能超过 `FEE_REVENUE`；审计 `ledger.gas_supply`）。`GAS_SUPPLY` 不够时手续费等着（`wallet_custody_fees_unbooked`，对账算作未入账手续费），告警 `CustodyFeesUnbooked`。

**回调**：先把原文（最多 16 KiB）、验签与时间检查结果记入 `custody_callbacks`，验签通过的同一（provider, tradeId, status）只记一行，托管方重试只加 `attempts`；再在一个事务里应用并记结果。验签失败、超出时间窗或格式不对的回调都计入 `wallet_custody_callbacks_rejected_total`，但每小时最多记 100 条、每条原文截到 2 KiB，免得伪造的回调灌满表（审查 B3）；后台看到的原文里 `sign` 打了码（审查 B7）：

| 结果 | 含义 |
|---|---|
| `APPLIED` | 已入账 / 已改状态；或与管理员的补记一致（已核对，不再入账） |
| `IGNORED` | 无需处理（充值的审核中状态、重复、晚到的审核回调） |
| `UNMATCHED` | 找不到网络、地址或提现，或回调的 `decimals` 与托管方 `support-coins` 列出的该币种小数位不同（25500000 按 0 位会记成 2550 万；币种列表 10 分钟读一次，读不到时按回调自己的小数位），回 `success` 停止重试，等人工处理（告警） |
| `REJECTED` | 验签失败、时间超窗或格式不对，回 401/400，不处理 |
| `FAILED` | 应用时出错，回 500 让托管方重试，也可在后台重放 |
| `DISCREPANCY` | 与管理员补记的充值不一致（地址、资产或数量），不更正、不入账，充值转为待处理并告警；或与已结束的提现矛盾——已失败（资金已解冻）后又说成功、已确认后又说失败：这是"钱出去了而我们已解冻"的唯一信号，不自动冲正，计数 `wallet_custody_withdrawal_contradictions_total`、告警 `CustodyWithdrawalContradiction`，人工向托管方核实；回 `success`。终态：同一回调重发时原样回 `success`，不再处理（C5.5 ⑦） |

后台「托管方」页可以重放 `FAILED`、`UNMATCHED`、`RECEIVED` 的回调（重新验签但不查时间，写审计 `wallet.custody.callback.replay`）。

**回调丢失时的补记**（2026-10-02 管理后台设计 §4.3）：网关没有按交易号查询的接口，托管方重试也放弃时，管理员先在优盾商户后台或区块浏览器核对到账，再在后台「充值 → 补记充值」录入网络、交易号、地址、哈希与数量。系统核对：网络由托管方服务、地址属于该网络上的用户、`UDUN:<tradeId>` 与（网络、哈希、地址）都没出现过、数量符合资产精度；按资金操作的护栏执行（单人模式限额内立即，否则第二位管理员批准），之后与回调一样记 `CONFIRMED`（来源 `MANUAL`、录入人），由处理器交账本入账。真回调晚到时按交易号找到这笔补记：一致记 `APPLIED`（`callback_at`），不一致记 `DISCREPANCY`（计数 `wallet_custody_deposit_discrepancies_total`，告警 `CustodyDepositDiscrepancy`）。`DISCREPANCY` 是回调的终态：托管方重发同一回调只回 `success`，不会改写成 `IGNORED`；回调先于处理器到达时，这笔补记不再交账本入账，等人处理（C5.5 ⑦）。还没等到回调的补记在 `exchangectl wallet reconcile --network UDUN`（或 `checks`）的报告里单列——托管方余额里没有对应到账，就是录错了。操作见 [admin.md](admin.md#充值处置与补记2026-10-02-设计-43c2c)。

## 对账（不变量 4 的托管版）

每小时（`exchangectl wallet reconcile --network UDUN` 立即）按资产比较：

```
短缺 = 账本应有 −(DEPOSIT_PENDING + WITHDRAWAL_PENDING) − 托管方余额 − 其它持有方 − 在途提现 − 未入账手续费
```

- 托管方余额：`/mch/support-coins` 各币种余额之和（USDT 三条链合计）。
- 其它持有方：资产的另一种保管方式此刻持有的数额。测试服的 ETH 同时在 Sepolia 自建钱包与托管方：托管方的检查把 Sepolia 热钱包与充值地址的链上余额算作"其它持有方"，Sepolia 的链上检查（[wallet.md](wallet.md)）把托管方余额算作"其它持有方"，两边都看整笔资产。
- 在途提现：托管方已收下（托管方状态 `ACCEPTED`、`REVIEW`、`APPROVED`）或已报告发出（`SUCCESS`、账本还没结算）的提现金额：这些托管方余额里已经没有、账本还算在内。交出去还没有回音（`SUBMITTED`）或 `UNCERTAIN` 的不算：托管方未必收下了，算进去会把同样大的短缺遮住（审查 B5）。
- 托管方没报某个币种的余额（或报的余额小数位多于该币种精度）时，这个资产这次不比较、报错（`not compared`），而不是当成 0 报一笔假短缺；某个币报的余额达到账本应有数按该币最小单位（托管方 `support-coins` 的 `decimals`）的一半以上时也不比较（多半是按最小单位报的，照比会遮住真正的短缺；按币种精度判断，小数位少的币也拦得住）。其他资产照常比较。没比较的资产 `wallet_custody_not_compared{asset}` 为 1（比较了为 0），持续 30 分钟告警 `CustodyNotCompared`：这期间它短不短缺没人知道。
- 结果写 `chain_checks`（`network = UDUN`，多了 `elsewhere`、`in_flight` 两列），`exchangectl wallet checks --network UDUN` 或后台「托管方」页查看。

### 短缺时自动停提（设计 §9，审查 B4，2026-10-03；ebb8aaa 审查修订）

短缺里减去说得清的部分之后，还缺得比这个资产的门槛多，就是"说不清的短缺"。说得清的部分：

- 结果未知的提现可能已拿走的：交出去没回音的 `SUBMITTED` 与 `UNCERTAIN`，托管方可能已经发出；
- 挂起等人工处理的手续费（`HELD`）：每笔都有自己的告警 `CustodyFeesHeld`；挂起的正是超出常理、单位没人确认或没有上限可比的那些，所以每笔最多只算它网络提现手续费的 5 倍（找不到网络的不算），不让一笔报错单位的手续费掩盖同样大的缺口（审查 AB）；
- 人工解除停提时接受的差额，在接受期内（见下文）。

门槛（ebb8aaa 审查 H1）：`WALLET_SHORTFALL_STOP`，如 `USDT=1,BTC=0.0001,ETH=0.001`（测试服 compose 里就是这组，大约是托管方手续费的粒度）；没写的资产取它托管网络里最小的提现手续费，网络都不收手续费时取最小的最小提现数量、再没有就取最小的最小充值数量（审查 AB：不是 0）。托管方余额 0.000001 的舍入不会再停提。

- 第一次看到：记进表 `shortfall_watch`（`suspect_since`，重启不丢），5 分钟后再对一次账（不等一小时；那次对账失败了，再过 5 分钟又对，不会拖到下一个整点）；中间人工 `reconcile` 不算数。
- 5 分钟后还缺：**停掉这个资产的提现**——表 `withdrawal_suspensions` 写一行（缺多少、两次对账的时间），审计 `wallet.withdrawals.suspend`（操作人 `system:custody-check`）。用户新提这个资产直接被拒（422 `WALLET_WITHDRAW_SUSPENDED`，"该币种暂停提现，平台正在核对资金"），公开的 `GET /v1/wallet/networks` 里这个资产的网络 `withdraw_enabled` 为 false、`withdraw_suspended` 为 true（两个站的提现页显示"暂停提现，平台正在核对资金"）。已批准的停在 `APPROVED` 不交给托管方（自建钱包也不签名，但不挡后面的站内转账），按资产计数 `wallet_withdrawals_suspended_waiting{asset}`，等了 30 分钟告警 `WalletWithdrawalsWaitingOnSuspension`。充值、交易、站内转账照常。指标 `wallet_withdrawals_suspended{asset}` 为 1，告警 `WalletWithdrawalsSuspended`（严重）。
- 第二次对账时已经不缺了：忘掉第一次，什么都不停。

停了之后不会自己恢复：人工查清原因（挂起的手续费、迟到的回调、托管方的账单）后解除，或者明知要停时手工先停（资产要有提现网络，拼错的资产名会被拒）：

```bash
exchangectl wallet withdrawals-suspended
exchangectl wallet withdrawals-resume --asset USDT --reason "托管方账单核对：挂起的 1.5 USDT 手续费已补记"
exchangectl wallet withdrawals-resume --asset USDT --reason "托管方舍入差 2 USDT，账本更正单另走" --accept 2 --for 72h
exchangectl wallet withdrawals-suspend --asset USDT --reason "托管方通报事故"
```

后台（C5.5 ⑯）：「提现」页顶部显示暂停的资产，ADMIN 可以带理由解除（不带 `--accept`），与上面的 `withdrawals-resume` 同一套、同一条审计，见 [admin.md](admin.md)。

解除后对账从头开始：还缺的话重新"第一次看到"，5 分钟后再停，不会立刻又停（ebb8aaa 审查 H2）。查清了但一时补不平的差额（比如托管方的舍入差，账本更正还在走流程），解除时用 `--accept` 接受下来、`--for` 定期限（最长 7 天，默认 24 小时）：期内对账只把超出接受额的部分当缺口，再多缺了照样停（停提原因里写明接受了多少、谁接受的）；对账自动停的，最多接受停提时记下的缺额（人工停的由操作人自己负责）；到期自动失效（下一次对账清掉），差额还在就重新停。长期的差额要在账本里更正，不要反复延长接受。`withdrawals-suspended` 同时列出正在观察的资产（第一次看到的时间、接受的差额与期限）。

两者都写审计（`wallet.withdrawals.resume` 带上停提的时间、原因与接受的差额）。

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
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T udun-mock /app/udun-mock outcome --address T... --status 3 --lose-answer --repeat-code 4001 --review --fee 1500000000 --charge 1500000
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T udun-mock /app/udun-mock delay --seconds 600
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T udun-mock /app/udun-mock replay --trade TRADE_ID --age 600
```

`deposit` 模拟一笔到账（地址必须是模拟网关生成的），`outcome` 设置发往某地址的提现结局（2 拒绝、3 成功、4 链上失败），以及模拟网关在途中怎么"不守规矩"（审查 B7，让 B1/B2 的路径都能走到）：`--fee N` 回调里带按币种最小单位计的手续费、`--charge N` 发出时从该币余额里另扣的手续费（同样按最小单位；与 `--fee` 相同就是如实报，不同就是报错了单位，审查 ④）、`--review` 先报审核中（status 0）再报通过、`--lose-answer` 收下第一次提交但回 502（回调等它重交后才发）、`--repeat-code C` 重复的 `businessId` 用 C 拒绝而不是 4288（像先查余额的网关那样，因为第一次已用掉余额而回"余额不足"）；只写 `--status` 即清掉这些设置。`delay` 让回调延后（故障演练），`replay` 重发一条已送达的回调（`--age` 签成若干秒前、`--forge` 用错误密钥签）。**不要删 state.json**：模拟网关余额与账本对不上，对账会一直报短缺。

## 换成真网关

联调计划（每步的回退）先交协调会话批准：测试商户在生产网关上，链上动作都是真的。商户的设置先放在服务器 `infra/udun-real.env`（0600，compose 不加载），不经 wallet-service、不碰数据库就能直接问网关：

```bash
cd /opt/exchange/infra && sudo docker run --rm --env-file udun-real.env exchange-app:latest /app/exchangectl udun coins
sudo docker run --rm --env-file udun-real.env exchange-app:latest /app/exchangectl udun check-address --main-coin 195 --address <地址>
sudo docker run --rm --env-file udun-real.env exchange-app:latest /app/exchangectl udun create-address --main-coin 195 --alias probe-tron --yes
```

`coins` 列出商户的币种编码（`provider_coin` 就填这里的 `CODE`）、小数位、是否代币与余额，旁边是 wallet-service 读成的小数位与余额（读不成的余额是 `-`，对账时会报不比较）；`check-address`、`create-address` 必须写 `--main-coin`（195 TRON、60 以太坊、0 比特币，见 `coins` 的 `MAIN` 列）。`create-address` 先说明要建什么（链、钱包、名字、回调地址）：建的地址会把充值回调到 `UDUN_CALLBACK_URL`，没有对应用户，到账只会记成 `UNMATCHED`、钱留在托管方；不带 `--yes` 只说明不建，带了才建，并打印托管方的原始应答。设置值去掉首尾空白，带引号（`docker --env-file` 不去引号）、密钥不足 32 位、网关不是 https 的都直接拒绝；密钥、商户号与钱包号都不打印。

1. 托管方后台登记回调地址 `https://astras.vip/v1/wallet/callbacks/udun`，把服务器出口 IP 加白名单。
2. `apps.env` 改 `UDUN_GATEWAY_URL`、`UDUN_MERCHANT_ID`、`UDUN_API_KEY`、`UDUN_WALLET_ID`、`UDUN_CALLBACK_URL`；托管方给了回调出口地址时 `UDUN_CALLBACK_ALLOWED_IPS` 填它、`custody-callback-allow.conf` 加同样的 `allow` 行，没给时前者留空、后者写 `allow all;`（只靠签名），提交部署；重启 wallet-service。真回调来过几笔后查来源再收紧：

   ```sql
   SELECT ip, count(*) FROM custody_callbacks, unnest(remote_ips) AS ip WHERE signature_ok GROUP BY ip ORDER BY 2 DESC;
   ```
3. 后台「托管方」页核对托管方返回的币种编码，改 `deploy/instruments/<环境>.json` 的 `provider_coin`。
4. 先关 `wallet.withdraw`，小额充值每个"资产·网络"一笔、对账通过后再逐个开提现；回调日志保留全量。
5. 去掉 compose 里的 `udun-mock`。

## 指标与告警

- `wallet_custody_up`、`wallet_custody_balance{coin}`（5 分钟）、`wallet_custody_held/expected/shortfall{asset}`（每次对账）、`wallet_custody_submitted`、`wallet_custody_submitted_oldest_seconds`、`wallet_custody_withdrawals_uncertain`、`wallet_custody_callbacks_attention`、`wallet_custody_deposits_held`、`wallet_custody_fees_unbooked`、`wallet_custody_fees_held`（等人工处理的手续费笔数）、`wallet_withdrawals_suspended{asset}`（该资产停提时为 1）、`wallet_withdrawals_suspended_waiting{asset}`（因停提等着的已批准提现），常量标签 `provider`；`wallet_custody_fees_held_total`（挂起的手续费）、`wallet_custody_callbacks_rejected_total`（被拒的回调，记不记表都算）、`wallet_custody_deposit_discrepancies_total`（与补记不一致的回调）。
- 告警（`deploy/observability/alerts.yml`）：`CustodyShortfall`（短缺 15 分钟，严重）、`CustodyNotCompared`（某资产 30 分钟没比较）、`CustodyUnreachable`（10 分钟）、`CustodyWithdrawalStuck`（`SUBMITTED` 超过 24 小时，人工到托管方后台核对）、`CustodyWithdrawalsUncertain`（重交被拒或 30 分钟无应答、托管方可能仍会发出，5 分钟，严重）、`CustodyCallbacksNeedAttention`（15 分钟）、`CustodyCallbacksRejected`（15 分钟内有回调被拒：伪造，或 `UDUN_API_KEY` 与托管方的不一致、充值进不来，审查 B6）、`WalletWithdrawalsSuspended`（某资产停提，严重；查清后 `exchangectl wallet withdrawals-resume`）、`WalletWithdrawalsWaitingOnSuspension`（已批准的提现因停提等了 30 分钟）、`CustodyFeesHeld`（有手续费等人工入账或核销）、`CustodyFeesUnbooked`（1 小时，`GAS_SUPPLY` 不够：`exchangectl ledger gas-supply`）、`CustodyDepositDiscrepancy`（回调与补记不一致，严重；在后台「充值 → 待处理」查明后驳回或调账）、`CustodyWithdrawalContradiction`（托管方的回调与已结束的提现矛盾，严重；`CustodyCallbacksNeedAttention` 也把 `DISCREPANCY` 计入）。

## 端到端

`scripts/e2e/custody.sh`：新用户拿 TRC20 与比特币地址；模拟网关报 30 USDT 到账（入账一次，重试与重放不重复），0.5 USDT 记未入账；伪造签名与过期回调被拒并记录；经公网发到 `https://astras.vip` 的回调（`udun`、`UDUN`、`Udun` 三种写法）在 nginx 就被拒（403），到不了平台；运营手工停掉 USDT 提现时新提现被拒（422 `WALLET_WITHDRAW_SUSPENDED`），解除后照常（自动停提要两次相隔 5 分钟的对账，端到端不等，由单元测试覆盖）；绑定身份验证器后 12 USDT 经审批交给托管方、`SUBMITTED` → `CONFIRMED` 带交易哈希并结算，托管方为它扣的 1.2 USDT 从 `GAS_SUPPLY` 入账（脚本先确认 TRC20 的手续费单位为 `SELF`，`GAS_SUPPLY` 不到 10 USDT 时从手续费收入挪 20）；10 USDT 发往失败地址 → `FAILED` 资金退回；模拟网关对第三个地址收下提现却丢了应答、重交时以余额不足拒绝、先报审核中、把实际扣的 1.5 USDT 报成 1500：提现停在 `UNCERTAIN`、资金冻结，之后回调到达照常发出并结算，手续费挂起不入账，`exchangectl wallet custody-fee --book --amount 1.5` 后入账；最后对账无短缺（托管方余额与账本都少了这两笔手续费）。浏览器冒烟测试的充值页同时取 Sepolia 与 TRC20 地址，后台冒烟测试打开「托管方」页与一条回调。

## 已知局限

- 公开文档没有提现查询与充值列表接口：提现以回调为准（`SUBMITTED` 超 24 小时告警），漏掉的充值回调靠托管方重试，再不行由管理员核对后补记（见上文「回调丢失时的补记」）。
- 托管方没有测试环境：真网关的应答码、余额格式、`fee` 的币种以正式文档与小额联调为准；每个代币网络第一笔真实提现的手续费要对照区块浏览器确认单位（`custody-fee-unit`），确认前它的手续费一律挂起。
- **接真网关前要定**（审查 2026-10-03）：TRC20、BEP20 的手续费若按主币（TRX、BNB）收，平台没有这两种资产的托管网络，也就没有它们的手续费收入给 `GAS_SUPPLY` 注资，`MAIN` 单位下这些手续费只会挂起、永远记不上账。二选一：确认托管方从另设的手续费账户扣（单位定为 `OUTSIDE`，不记账，平台在账外给托管方充 TRX/BNB）；或者把 TRX、BNB 加为托管资产（网络行），`GAS_SUPPLY` 由它们的手续费收入或一条平台注资路径补。ERC20 按 ETH 收时可以记账（ETH 有托管网络与手续费收入）。
