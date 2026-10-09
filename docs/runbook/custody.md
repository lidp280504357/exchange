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
| `UDUNMOCK_*` | 模拟网关的第二个商户 `UDUNMOCK`（ADR-0017，只在测试服）：同上六项（`UDUNMOCK_GATEWAY_URL`、`UDUNMOCK_MERCHANT_ID`、`UDUNMOCK_API_KEY`、`UDUNMOCK_WALLET_ID`、`UDUNMOCK_CALLBACK_URL`、`UDUNMOCK_CALLBACK_ALLOWED_IPS`），只服务端到端用的隐藏测试资产，`UDUN` 换成真网关后端到端照常在它上面跑。网关、回调地址与来源写在 compose 里（`http://udun-mock:8097`、`http://api-gateway:8080/v1/wallet/callbacks/udunmock`、容器网段），商户号与密钥在 `apps.env`。模拟网关只读 `UDUNMOCK_MERCHANT_ID`、`UDUNMOCK_API_KEY`，不读 `UDUN_*`：切换后 `UDUN_*` 是真商户的，模拟网关拿它签的回调会被当成真网关的 |

网络在 `deploy/instruments/test.json`（`exchangectl instruments apply` 同步）：

| 资产 | 网络 | 显示名 | provider_coin | 确认数 | 最小充 | 最小提 | 手续费 | 充提 |
|---|---|---|---|---|---|---|---|---|
| USDT | TRON | TRC20 | `195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t` | 20 | 1 | 10 | 1 | 开 |
| USDT | BSC | BEP20 | `2510:0x55d398326f99059fF775485246999027B3197955` | 15 | 1 | 10 | 0.5 | 关 |
| USDT | ETH | ERC20 | `60:0xdAC17F958D2ee523a2206206994597C13D831ec7` | 12 | 5 | 20 | 5 | 关 |
| BTC | BTC | Bitcoin | `0:0` | 2 | 0.0001 | 0.001 | 0.0002 | 开 |
| ETH | ETH | Ethereum | `60:60` | 12 | 0.002 | 0.005 | 0.001 | 开 |
| TUSD（隐藏，`UDUNMOCK`） | TRON-TEST | TRC20 (test) | `195:TQQCuyVcUEknTGyfSRKhcUuLZfEe93qWpy` | 1 | 1 | 10 | 5 | 开，只对 `TEST_ASSETS` |

同一网络的资产共用一个地址（每用户每网络一个，托管方按主链生成）：USDT-ERC20 与 ETH 都在网络 `ETH`。ETH 另有自建的 `ETH-SEPOLIA`。

**USDT 先只走 TRC20**（用户 2026-10-03 决定）：优盾商户的钱包只有 TRON 上的 USDT，BEP20、ERC20 两个网络保留配置、充提都关（站点照常列出，显示暂停）；以后在优盾后台加了币种，按 `udun coins` 核对编码后在 `test.json` 打开再部署。托管方没有列出、且充提都关的网络，对账时不计它的币种（那里不可能有钱，不计只会显出短缺、不会掩盖短缺）；网络开着而托管方没列出时，该资产照旧"不比较"并告警。BSC 的主链编码是托管方 `support-coins` 列出的 `2510`（2026-10-03 实测，原先按 SLIP-44 猜的 9006 不对），模拟网关同样改用 2510，旧编码下余额为 0 的币种在它重启时去掉。

## 流程

**充值**：首次申请地址时，wallet-service 先向托管方建地址（`alias` = `用户ID:网络`；这一步不在事务里，最长等到请求超时），再在事务里加咨询锁、落库 `deposit_addresses`；同一用户同一网络的两个首次请求同时到达时，后落库的一方用先落库的地址，托管方多建的那个地址不给任何人看（审查 B7）。地址落库时 `provider=UDUN`、没有派生序号，同时发 `DepositAddressAssigned`。托管方确认后回调 `status=3`：按 `provider_coin` 找网络、按地址找用户，记一笔 `CONFIRMED` 充值（键 `provider_tx_id = UDUN:<tradeId>`，同一笔交易付给多个地址也各记一笔），发 `DepositDetected`；处理器（5 秒一轮）照常检查资格后发 `DepositConfirmed`，账本记 `DEPOSIT_CREDIT`。低于最小额的记 `UNCLAIMED_DEPOSIT`；按资产精度截断后为 0 的记 `REJECTED`。`status` 不是 3 的充值回调只记录（`IGNORED`）。

**没有主人的充值**（B7a，2026-10-03）：托管方报到账、币种能对上网络、但地址不属于任何用户（探测地址、退役的模拟地址）时，记一笔"无主"充值：`user_id` 是空 UUID（`00000000-0000-0000-0000-000000000000`，代码里的 `domain.NoOwner`）、`unclaimed`、原因 `UNKNOWN_ADDRESS`、`CONFIRMED`，回调记 `APPLIED`。处理器不发事件，直接调账本 gRPC `CreditUnclaimed` 记入 `UNCLAIMED_DEPOSIT`（`DEPOSIT_PENDING` → `UNCLAIMED_DEPOSIT`，键 `deposit:<充值ID>`，重试只会重放）：账本因此"预期托管方持有"这笔钱，对账不会把它当成多出来的。空 UUID 只由这条路写，凡是要用户的地方都不收：不发任何按用户的事件（存储层直接拒绝，通知服务与读模型都见不到它）、不查资格、后台"入账给用户"（`/credit`）拒绝（`WALLET_DEPOSIT_NO_OWNER`），账本的 `ReleaseUnclaimed` 也拒绝空 UUID。处理办法二选一：

- 入账给查明的用户：`POST /internal/wallet/deposits/{id}/assign`，`{"user_id", "actor", "reason"}`（admin-service 调用；用户须当前可以充值），一次操作里把主人改成这个用户并 `ReleaseUnclaimed` 给他，审计 `wallet.deposit.assigned`（细节里带地址退役前的主人）与账本的 `ledger.unclaimed_released`，再给这个用户发 `DepositCredited`。重复调用不会重复入账。账本的放行先提交、钱包的记录后写：记录失败时钱已经付给当时选的用户、这笔仍是无主（审查 AJ）。之后先问账本（`GetUnclaimedRelease` 带收款用户）：已放行时只能分配给收款的那个用户，这次只把放行记下来（不再检查他现在能否充值，钱已经在他那里；审计细节 `"release_recorded": true`），换别人得到 409 `WALLET_DEPOSIT_RELEASED_TO_USER`（详情 `user_id` 与 `journal_id`；与有主人的待处理充值的 `WALLET_DEPOSIT_RELEASED` 分开，审查 AL），驳回也是同样的 409。站点上，入账后的这类充值显示"经人工核实后入账"（原因 `UNKNOWN_ADDRESS`，公开契约的充值原因多了这个值）。一笔无主充值账本不肯入账（例如被拒）时只有它自己等着，同一网络其他充值照常入账；它先等 1 分钟再试，之后每失败一次等待加倍、最长 1 小时（重启后立刻再试），每次失败记一条错误日志；等着的笔数是 `wallet_custody_deposits_unbookable`，大于 0 持续 30 分钟告警 `CustodyDepositUnbookable`（审查 AL）。后台列表与详情（`GET /internal/wallet/deposits`、`/{id}`）对无主充值给出 `address_owner`：地址现在的主人，或退役前的主人（`address_owner_retired`），供判断参考。
- 驳回（`/dismiss`，与其他待处理充值相同）：钱留在 `UNCLAIMED_DEPOSIT`、仍在托管方；若在托管方后台把钱退给了付款人，要另做账本更正，否则对账会发现托管方少了这笔。

币种对不上任何网络的到账不记充值（没有资产可记），回调仍是 `UNMATCHED`、等人处理后重放。两种情况都计数 `wallet_deposits_unmatched_total{reason="unknown_address"|"unknown_coin"}`，一小时内有就告警 `CustodyDepositUnmatched`。

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

后台用的内部接口（2026-10-03，与命令行同一套判断与审计，`actor` 为管理员邮箱；网关不转发 `/internal`）：

- `GET /internal/wallet/custody/fees?provider=UDUN|UDUNMOCK&status=HELD|BOOKABLE|WRITTEN_OFF&cursor=&limit=`（另可 `user_ids` 或 `exclude_user_ids` 按提现的用户收窄，L2；账户多时用 `POST /internal/wallet/custody/fees/list`，同样的查询串、账户放请求体，最多 5,000 个；见 [wallet.md](wallet.md)）：托管方的提现手续费，最新的在前（`limit` 默认 50，超过 200 按 200；不写 `provider`、`status` 即全部；不认识的游标 400）。返回 `{"items": [...], "next_cursor": "<tx_hash>" | null}`，每项 `{"withdrawal_id", "provider"（提现的托管方 UDUN/UDUNMOCK）, "tx_hash"（托管方与成交号，游标用它）, "asset", "network", "amount", "unit"（该网络上确认过的单位 SELF/MAIN/OUTSIDE，没人确认是 null）, "status", "hold_reason", "journal_id", "created_at", "booked_at", "written_off_at", "resolved_by", "resolution"}`。
- `POST /internal/wallet/custody/fees/{withdrawal_id}/book`，`{"asset"?, "amount"?, "actor", "reason"}`：按报的入账，或按查到实际扣的资产与数额入账（`amount` 为十进制字符串）。
- `POST /internal/wallet/custody/fees/{withdrawal_id}/write-off`，`{"actor", "reason"}`：核销。
- 两个写接口都返回这笔手续费（同上的一项）；提现不存在或没有托管方的手续费 404 `WALLET_CUSTODY_FEE_NOT_FOUND`，手续费不在等人处理（已入账、已核销、或照常等着入账却要按人工入账）409 `WALLET_CUSTODY_FEE_NOT_HELD`（详情 `status`），刚被另一个决定抢先 409 `WALLET_CUSTODY_FEE_CHANGED`，参数不对 400（审查 AL）。

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

**回调丢失时的补记**（2026-10-02 管理后台设计 §4.3）：网关没有按交易号查询的接口，托管方重试也放弃时，管理员先在优盾商户后台或区块浏览器核对到账，再在后台「充值 → 补记充值」录入网络、交易号、地址、哈希与数量。系统核对：网络由托管方服务、地址属于该网络上的用户、`UDUN:<tradeId>` 与（网络、哈希、地址）都没出现过、数量符合资产精度；按资金操作的护栏执行（单人模式限额内立即，否则第二位管理员批准），之后与回调一样记 `CONFIRMED`（来源 `MANUAL`、录入人），由处理器交账本入账。真回调晚到时按交易号找到这笔补记：一致记 `APPLIED`（`callback_at`），不一致记 `DISCREPANCY`（计数 `wallet_custody_deposit_discrepancies_total`，告警 `CustodyDepositDiscrepancy`）。`DISCREPANCY` 是回调的终态：托管方重发同一回调只回 `success`，不会改写成 `IGNORED`；回调先于处理器到达时，这笔补记不再交账本入账，等人处理（C5.5 ⑦）。还没等到回调的补记在 `exchangectl wallet reconcile --network UDUN`（或 `checks`）的报告里单列——托管方余额里没有对应到账，就是录错了。操作见 [admin.md](admin.md#充值处置与补记)。

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

容器 `udun-mock`（REST 8097、运维 9097），商户号与密钥只读 `UDUNMOCK_MERCHANT_ID`、`UDUNMOCK_API_KEY`（服务器 `apps.env`；wallet-service 的 `UDUNMOCK` 托管方用同样的值。模拟网关只有一个商户，所以 `UDUN` 还指向它的时候，`UDUN_MERCHANT_ID`、`UDUN_API_KEY` 必须与这两项相同；切换真网关时只改 `UDUN_*`，审查 AO），状态在 `/opt/exchange/infra/udun-mock/state.json`（地址、余额、提现、待发回调；属主 uid 10001，部署脚本创建）。行为：
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

## 真网关联调（2026-10-03 起）

优盾给的是**生产网关上的测试商户**（正式上线会换）：没有沙箱，链上动作都是真的。商户的设置在本地 `.env` 与服务器 `infra/udun-real.env`（0600，compose 不加载），切换时才并入 `apps.env`；密钥、商户号、钱包号与网关地址不进仓库、文档、日志与消息。

计划由协调会话 2026-10-03 批准：步骤 0–3 先做（都不动钱）；步骤 4 等 1–3 的结果报协调会话放行；步骤 5 由协调会话与用户约时间；步骤 6 在 5 之后。每一步做完把结果报协调会话，并记在下面的「进展」里。

### 决定

- **回调来源**：优盾后台与文档都没给回调出口 IP。`UDUN_CALLBACK_ALLOWED_IPS` 为空即不按来源过滤（启动时 WARN），只靠签名、±5 分钟时间戳与按"单号 + 状态"去重；nginx 回调路径 `allow all;`，每个来源每秒 10 个、突发 50 个；每条回调的来源记进 `custody_callbacks.remote_ips`，真回调跑过几笔后从这里取地址收紧（两处同时改）：

  ```sql
  SELECT ip, count(*) FROM custody_callbacks, unnest(remote_ips) AS ip WHERE signature_ok GROUP BY ip ORDER BY 2 DESC;
  ```
- **B1 账本基线**：模拟网关的充值让账本"预期托管方持有"若干 USDT（2026-10-03 下午 483.55，端到端每跑一次 `custody.sh` 都会变），真商户上是 0，一切换就是短缺、5 分钟后停提。切换时按资产记一笔审计过的对冲分录 `DEPOSIT_PENDING +X / ADJUSTMENT −X`（X 取切换当刻 `exchangectl wallet checks --network UDUN` 的 `HELD`，即模拟网关持有的；`ELSEWHERE` 是自建钱包的，不动）：

  ```bash
  exchangectl ledger custody-reset --asset USDT --amount <X> --reason "模拟网关换成真网关：模拟充值不在任何托管方" --key switch-usdt
  exchangectl ledger custody-reset --asset USDT --amount <X> --reason "回退到模拟网关" --key back-usdt --reverse   # 回退
  ```

  要开关 `ledger.manual_adjustment`，写审计 `ledger.custody_reset`；`DEPOSIT_PENDING` 不会因此变成正数（预期持有不能小于 0），回退不能超过已经重置的（账本自己按 `custody-reset:` 分录的合计检查，审查 AH）。X 不能超过该托管方最近一次对账的 `HELD`（多出的部分会悄悄压低预期，以后真少了钱对账也看不出来）；还没有对账时先 `exchangectl wallet reconcile --network UDUN`，确实要超过时加 `--force`。每笔分录同时记进钱包库的 `custody_baselines`（同一个 `--key` 重跑不会重复记），对账（`exchangectl wallet checks --network UDUN`、后台「托管方」页）每个资产多一列 `SIMULATED`（后台接口 `baseline`）："模拟资金、不在托管方"，所以预期为 0 是写明的，不是凭空消失；它不计入短缺。用户的模拟余额不动。
- **B2 模拟时期的充值地址**：`deposit_addresses` 里 `provider = UDUN` 的是模拟网关编的地址（2026-10-03：TRON 129 个、BTC 27 个），切换后有人往里打真钱就丢了。切换时移进 `retired_deposit_addresses`，用户下次申请拿到真地址；提现到这些地址（包括之前加进地址簿的）一律拒绝（`WALLET_INVALID_ADDRESS`，原因 `ADDRESS_RETIRED`）；回退时放回，期间已经拿到新地址的用户保留新的：

  ```bash
  exchangectl wallet retire-addresses --provider UDUN --reason "模拟网关的地址，真链上不存在" --yes
  exchangectl wallet restore-addresses --provider UDUN --reason "回退到模拟网关" --yes   # 回退
  ```

  不带 `--yes` 只说要动多少个地址；两个命令都只在 `UDUN_GATEWAY_URL` 指向模拟网关 `udun-mock` 的地方运行（在仍连模拟网关的 wallet-service 容器里），接上真网关后它的地址是真的，命令拒绝（审查 AH：切换后误跑会把真地址退役，真充值就成了无主）。所以回退时先恢复 `apps.env` 并重启 wallet-service，再恢复地址。

  两者都写审计（`wallet.deposit_addresses.retire|restore`）。**退役、恢复与上面的 `custody-reset` 一律在 wallet-service 容器里运行**（审查 AJ）：退役与恢复按运行它的进程自己的 `UDUN_GATEWAY_URL` 判断是不是模拟网关，只有 wallet-service 容器的环境与正在运行的 wallet-service 一致；改了 `apps.env` 而没重启的其他容器还带着旧值，在那里跑会放过不该放过的：

  ```bash
  cd /opt/exchange/infra && sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T wallet-service /app/exchangectl wallet retire-addresses --provider UDUN --reason "..."
  ```

  站点的充值地址都按接口取（`Cache-Control: no-store`，service worker 只缓存离线页，没有本地存储）；打开着的充值页每分钟、回到页面时重新取一次，旧地址最多再显示一分钟。
- **B3 提现**：人人都有模拟 USDT，接上真网关后批出去的提现付的是真钱。切换时先手动暂停 USDT、BTC、ETH 的提现；用户测试的窗口里只解除要测的资产，所有托管提现进人工审核、只批用户自己那笔；窗口结束重新暂停。商户只放测试金额。
- **B4 端到端**：`custody.sh` 靠模拟网关（造充值、各种不守规矩、手续费、`UNCERTAIN`、对账），接真网关后会动真钱。选方案 A：wallet-service 同时接两个托管方，`UDUN`（真网关）管 USDT/BTC/ETH，`UDUNMOCK`（模拟网关）管只给端到端用的 `TUSD`；TUSD 对站点与市场完全不可见（不进资产列表与交易对，只有端到端账户能充提），provider 标签清楚。做好之前 `custody.sh` 在网络的托管方是真网关时拒绝运行。

### 直接问网关

不经 wallet-service、不碰数据库（步骤 1、2 用）：

```bash
cd /opt/exchange/infra && sudo docker run --rm --env-file udun-real.env exchange-app:latest /app/exchangectl udun coins
sudo docker run --rm --env-file udun-real.env exchange-app:latest /app/exchangectl udun check-address --main-coin 195 --address <地址>
sudo docker run --rm --env-file udun-real.env exchange-app:latest /app/exchangectl udun create-address --main-coin 195 --alias probe-tron --yes
```

`coins` 列出商户的币种编码（`provider_coin` 就填这里的 `CODE`）、小数位、是否代币与余额，旁边是 wallet-service 读成的小数位与余额（读不成的余额是 `-`，对账时会报不比较）；加 `--raw` 先原样打印网关应答的 `data`。`check-address`、`create-address` 必须写 `--main-coin`（195 TRON、60 以太坊、0 比特币、2510 BSC，见 `coins` 的 `MAIN` 列）。`create-address` 先说明要建什么（链、钱包、名字、回调地址）：建的地址会把充值回调到 `UDUN_CALLBACK_URL`，没有对应用户，钱留在托管方；wallet-service 接上这个网关后，到账记成无主充值（`UNKNOWN_ADDRESS`，进 `UNCLAIMED_DEPOSIT`，等人在后台入账给查明的用户或驳回，见上文「没有主人的充值」），接模拟网关时它的回调被来源名单挡住（403）、优盾会重发；不带 `--yes` 只说明不建，带了才建，并打印托管方的原始应答。设置值去掉首尾空白，带引号（`docker --env-file` 不去引号）、密钥不足 32 位、网关不是 https 的都直接拒绝；密钥与商户号不打印，钱包号只露头尾。

### 商户的币种与探测地址（步骤 1、2，2026-10-03 实测）

`udun coins --raw`（`support-coins`）：

| CODE（`provider_coin`） | 币种（`coinName`） | 主链 | 代币 | 小数位 | 余额 | 平台用途 |
|---|---|---|---|---|---|---|
| `0:0` | BTC（Bitcoin） | BTC | 否 | 8 | `null` | BTC 托管 |
| `60:60` | ETH（Ethereum） | ETH | 否 | 18 | `null` | ETH 托管 |
| `195:195` | TRX（TRON） | TRX | 否 | 6 | `null` | 只作 TRC20 的燃料，不是平台资产 |
| `195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t` | USDT（USDT-TRC20） | TRX | 是 | 6 | `null` | USDT 托管（TRC20） |
| `2510:2510` | BNB（BNB-BSC） | BNB | 否 | 18 | `null` | 只作燃料，不是平台资产 |

- 应答的字段与官方文档一致：`decimals`、`mainCoinType`、`coinType` 是字符串，`tokenStatus` 是数字（0 主币、1 代币），另有 `logo`、`coinName`。BEP20 USDT（`2510:0x55d3…7955`）与 ERC20 USDT（`60:0xdAC1…1ec7`）不在商户的币种里（所以这两个网络关着）。
- **余额**：每个币种都是 `"balance": null`。换请求写法结果一样：`showBalance` 为 `true`、`false` 或字符串 `"true"`，商户号写成字符串或数字，带不带 `walletId`。所以不是平台漏读了字段（字段名就是 `balance`），而是网关写了 `null`；官方文档示例里的余额是字符串 `"0"`。最可能是钱包从没收过钱、没有余额记录，但第一笔真钱到账（步骤 5）之前分不清"空钱包"与"不给余额"。wallet-service 把 `null` 读成"没有余额"，该资产不比较，不当作 0（B10）：接上真网关后 USDT、BTC、ETH 的对账都是"不比较"，30 分钟后告警 `CustodyNotCompared`，直到余额出现；步骤 5 到账后再看余额是不是变成数字、是币还是最小单位，B10 的修正等这一步。
- `check-address`：TRON（195）的 `TR7N…Lj6t` 有效，改掉末位无效，以太坊格式的地址无效；以太坊（60）的 `0xdAC1…1ec7` 有效，少一位无效，全小写和大小写混合但校验和错的都算有效（网关不查 EIP-55 校验和；平台自己的校验拒绝校验和错的地址）；比特币（0）的 `bc1q…`、taproot `bc1p…`、旧式 `1A1z…` 有效，改掉末位无效，测试网 `tb1…` 无效（只认主网）；BSC（2510）的 `0x55d3…7955` 有效。

探测地址（步骤 2，每条链一个，`create-address --yes`）。**不要用、不要往里充值**：它们不属于任何用户，钱留在托管方，接上真网关后到账记成无主充值。三个地址都通过了平台的地址校验（`domain.CheckAddress`）与网关的 `check-address`：

| 链 | 名字（alias） | 地址 |
|---|---|---|
| TRON（195） | `probe-tron` | `TPYmLv1FbHoxaPa8K9ktTz8yphYckvDJ9Q` |
| 比特币（0） | `probe-btc` | `bc1qd40zw4h0tm6j4w84a2d9aeaq8jnesvjw5hgujz`（P2WPKH） |
| 以太坊（60） | `probe-eth` | `0x7db4734f19f55a2ece0ea87c09df6a44fc463d8c` |

以太坊地址是全小写给的（没有 EIP-55 校验和）。平台按地址查找一律不分大小写（`lower(address)`），回调能对上；用户看到的是托管方给的原样。

### 步骤与回退

| 步骤 | 做什么 | 回退 |
|---|---|---|
| 0 代码 | 停提的阈值与"解除后从头对账"、回调来源语义（空即不过滤）与 `remote_ips`、nginx 限速、直接问网关的命令、对账基线与退役地址的工具、`custody.sh` 遇真网关拒绝运行；仍连模拟网关部署 | `git revert` 后部署 |
| 1 只读 | `udun coins`、各链 `check-address`：核对币种编码（BSC 是否 9006）、小数位、是否代币、余额的单位，修 `deploy/instruments/test.json` 的 `provider_coin`。网关拒绝我们的 IP 时由协调会话请用户在优盾后台把 `3.107.113.199` 加白 | 无（只读） |
| 2 探测地址 | 每条链（TRON、BSC、ETH、BTC）各建一个地址（`--alias probe-<链>`），用平台的地址校验核对格式；这些地址记在下面、**不要用**：往里充值只会记成 `UNMATCHED`，钱留在托管方 | 无（地址不用，没有状态） |
| 3 开放回调 | `custody-callback-allow.conf` 改成 `allow all;` 并部署；仍连模拟网关时公网回调被 wallet-service 的来源名单挡住（403），切换后由签名把关（401） | 恢复 allow 文件后部署；优盾会重发回调 |
| 4 切换（协调会话放行） | 运维锁内：暂停 USDT/BTC/ETH 提现（B3）→ 把 `provider = UDUN` 的充值地址导出到 `infra/backup/`，在仍连模拟网关的 wallet-service 容器里退役模拟地址（B2）→ 同一容器里先对账一次，以它的 `HELD` 为 X、固定 `--key` 记基线对冲分录（B1）→ 备份 `apps.env`，把 `udun-real.env` 的值并入 `UDUN_*`（`UDUN_CALLBACK_ALLOWED_IPS` 留空）→ 重启 wallet-service → 核对 `wallet_custody_up` 为 1、测试账户在 TRC20、比特币、以太坊拿到真地址；余额是 `null` 时各资产"不比较"，第一次对账由人工核对（B10） | 恢复 `apps.env` 备份并重启、恢复地址、记反向分录；其间发出的真地址在优盾那边仍有效，充值等切回后再处理 |
| 5 主网小额（协调会话约用户） | 用户在站点拿 TRC20 地址充至少 12 USDT（最小提现 10 + 手续费 1），看回调（`remote_ips`）、入账与对账；只在窗口里解除 USDT，用户提 10 USDT 到自己的地址、管理员批准；按回调与 tronscan 核对手续费单位后 `custody-fee-unit`；窗口结束重新暂停；按 `remote_ips` 收紧回调来源；其他网络要不要测由用户定（各自要付链上手续费） | 重新暂停；卡住的提现走 `custody-resolve` |
| 6 端到端 | 方案 A：`UDUNMOCK` 托管方 + `TUSD`，`custody.sh` 改用它 | `git revert` 后部署 |

### 进展

- 2026-10-03 步骤 0 的第一部分已部署（43956f5、5804b6e、14d2412、462db60、a26989e、58bf6d8、6ee3673；完整端到端通过）：停提阈值、解除后从头对账、回调来源语义与 `remote_ips`、nginx 限速、直接问网关的命令。
- 2026-10-03 步骤 1：**被网关挡住**。`udun coins` 与 `udun check-address` 都返回 `code 4264`（应答的说明也只有 4264）。优盾公开的返回码表里没有 4264；它不是签名错（4162/4163）、商户不存在（4001）或账户被禁用（4169/4226/4261/4262），每个接口都一样，像是接口的 IP 白名单。服务器出口 IP 是 `3.107.113.199`。已请协调会话转用户在优盾后台加白（或问优盾 4264 的含义），之后重跑。
- 2026-10-03 步骤 0 的第二部分（对账基线 `ledger custody-reset` 与 `SIMULATED` 列、退役与恢复模拟地址、提现拒绝退役地址、充值地址不再长期缓存、`custody.sh` 遇真网关跳过、审查 AF：按未托管的主币挂起的手续费记在该主币上、不解释代币的短缺）与步骤 3（nginx 回调路径 `allow all;`）已部署（0ffbcf1、915ab3a，钱包迁移 00011）；端到端 `custody.sh`、`web.sh`、`admin.sh` 通过。步骤 3 之后经公网伪造的回调到了 wallet-service，仍被它的来源名单（模拟网关的容器网段）以 403 `COMMON_FORBIDDEN` 挡住；切换后由签名把关。
- 2026-10-03 晚 步骤 1、2 完成（用户把 `3.107.113.199` 加进了优盾的白名单）：币种、地址校验、余额 `null` 与三个探测地址见上文「商户的币种与探测地址」。随之改了配置（用户决定 USDT 先只走 TRC20）：`test.json` 的 BEP20 编码改成 `2510:…`，BEP20、ERC20 两个 USDT 网络充提都关、配置保留；对账不计托管方没有列出、且充提都关的网络的币种（否则真网关下 USDT 会因为没有 BEP20、ERC20 而一直"不比较"）；模拟网关改用 2510；`custody.sh` 改为检查只有 TRC20 开着。
- 2026-10-03 晚 审查 AJ 的四项改完：无主充值分配的"账本已付、钱包没记上"按收款用户补记（账本 `GetUnclaimedRelease` 多返回 `user_id`）；一笔无主充值入账失败不再卡住整个网络的轮次；退役、恢复与基线重置写明在 wallet-service 容器里跑（步骤 4 的顺序改成先退役与重置、后并入 `apps.env`）；站点有了 `UNKNOWN_ADDRESS` 的文案，入账后的充值不再把原因显示成红色的"未入账"。
- 2026-10-04 审查 AT 的数据修复（运维锁内，21:32Z）：d1217a8 之前被分配的无主充值 `01a10324-8533-7036-9c66-776411a7cd5b`（TUSD 1.0，CREDITED/CREDITED，`user_id` 为空，admin.sh 第 ㉑ 例产生）。证据：释放分录 `01a10324-c48a-76f7-b362-6c11de733301`（`deposit-release:01a10324-8533-7036-9c66-776411a7cd5b`）把 1 TUSD 从 `UNCLAIMED_DEPOSIT` 记给用户 `01a10324-71b6-79b2-b6bb-c938304e4e72` 的 SPOT；后台审批 `01a10324-be65-73b9-9c05-12c83192999b`（DEPOSIT_ASSIGN，EXECUTED）指向同一用户与同一分录。执行：`UPDATE wallet.deposits SET user_id = '01a10324-71b6-79b2-b6bb-c938304e4e72' WHERE id = '01a10324-8533-7036-9c66-776411a7cd5b' AND user_id = '00000000-0000-0000-0000-000000000000' AND status = 'CREDITED' AND resolution = 'CREDITED' AND release_journal_id = '01a10324-c48a-76f7-b362-6c11de733301'`（UPDATE 1）。复查：没有别的持有人为空却已释放或已入账的充值（0 行）；后台没有 FAILED 的审批（0 行，即没有"已付但 FAILED"的资金操作）。没有事件，ClickHouse 的 `wallet_deposits` 快照仍是空持有人（测试数据，不补）。

## 端到端的托管方 UDUNMOCK 与测试资产 TUSD（方案 A，ADR-0017）

真网关切换后 `UDUN` 不能再让端到端造充值、丢应答，所以端到端有自己的托管方：模拟网关的第二个商户 `UDUNMOCK`（设置见上文「配置」的 `UDUNMOCK_*`）与只给它用的隐藏资产 TUSD（网络 `TRON-TEST`，见上表）。

- wallet-service 为每个配置了的托管方各起一个处理器（租约 `wallet-custody:<托管方>`，指标常量标签 `provider`）、各有回调来源名单与币种缓存；对账按资产覆盖全部持有方，另一个托管方持有的算"别处持有"。后台接口 `/internal/wallet/custody?provider=UDUNMOCK`、`/internal/wallet/custody/callbacks?provider=UDUNMOCK` 分开看（`/internal/wallet/custody` 带 `gateway_host`：托管方网关地址的主机名，不含协议、路径与查询，没配置时为空；后台「上线检查清单」据此区分模拟网关 `udun-mock` 与真网关，2026-10-04 设计 §4.6）；`exchangectl wallet reconcile --network UDUNMOCK`、`checks --network UDUNMOCK`、`custody-fee-unit --provider UDUNMOCK`。
- `UDUNMOCK` 的回调只在容器网络里经 api-gateway 回到 `/v1/wallet/callbacks/udunmock`；nginx 对公网的这条路径一律 403，wallet-service 再按 `UDUNMOCK_CALLBACK_ALLOWED_IPS`（容器网段）把关。
- TUSD 不进任何公开列表，充提只对 `TEST_ASSETS` 资格（开关 `wallet.test_assets`，测试服只对地区 `AQ`）的用户开放，其他人一律 404 `WALLET_NETWORK_UNKNOWN`；端到端用 `AQ` 注册。它没有行情，提现限额按 `WALLET_FALLBACK_PRICES` 的 `TUSD:1` 折算；停提阈值 `WALLET_SHORTFALL_STOP` 的 `TUSD=1`。
- 托管方对 TUSD 收的手续费（模拟网关的单位按 `SELF` 确认）从 `GAS_SUPPLY` 出，`GAS_SUPPLY` 只能由 TUSD 自己的手续费收入补：`custody.sh` 在第一笔提现结算后把手续费收入挪过去（每笔 5 TUSD，托管方每轮约收 2.7）。
- 切换真网关时只改 `UDUN_*`；模拟网关只认 `UDUNMOCK_MERCHANT_ID`、`UDUNMOCK_API_KEY`，不读 `UDUN_*`，`UDUNMOCK` 照常指向它。

## 指标与告警

- `wallet_custody_up`、`wallet_custody_balance{coin}`（5 分钟）、`wallet_custody_held/expected/shortfall{asset}`（每次对账）、`wallet_custody_submitted`、`wallet_custody_submitted_oldest_seconds`、`wallet_custody_withdrawals_uncertain`、`wallet_custody_callbacks_attention`、`wallet_custody_deposits_held`、`wallet_custody_fees_unbooked`、`wallet_custody_fees_held`（等人工处理的手续费笔数）、`wallet_withdrawals_suspended{asset}`（该资产停提时为 1）、`wallet_withdrawals_suspended_waiting{asset}`（因停提等着的已批准提现），常量标签 `provider`；`wallet_custody_fees_held_total`（挂起的手续费）、`wallet_custody_callbacks_rejected_total`（被拒的回调，记不记表都算）、`wallet_custody_deposit_discrepancies_total`（与补记不一致的回调）、`wallet_deposits_unmatched_total{reason}`（地址不属于任何用户或币种对不上网络的到账，B7a）、`wallet_custody_deposits_unbookable`（账本不肯入账、等着重试的无主充值，审查 AL）。某资产"不比较"时它的 `held/expected/shortfall` 序列去掉，免得旧值像现值。
- 告警（`deploy/observability/alerts.yml`）：`CustodyShortfall`（短缺 15 分钟，严重）、`CustodyNotCompared`（某资产 30 分钟没比较）、`CustodyUnreachable`（10 分钟）、`CustodyWithdrawalStuck`（`SUBMITTED` 超过 24 小时，人工到托管方后台核对）、`CustodyWithdrawalsUncertain`（重交被拒或 30 分钟无应答、托管方可能仍会发出，5 分钟，严重）、`CustodyCallbacksNeedAttention`（15 分钟）、`CustodyCallbacksRejected`（15 分钟内有回调被拒：伪造，或 `UDUN_API_KEY` 与托管方的不一致、充值进不来，审查 B6）、`WalletWithdrawalsSuspended`（某资产停提，严重；查清后 `exchangectl wallet withdrawals-resume`）、`WalletWithdrawalsWaitingOnSuspension`（已批准的提现因停提等了 30 分钟）、`CustodyFeesHeld`（有手续费等人工入账或核销）、`CustodyFeesUnbooked`（1 小时，`GAS_SUPPLY` 不够：`exchangectl ledger gas-supply`）、`CustodyDepositDiscrepancy`（回调与补记不一致，严重；在后台「充值 → 待处理」查明后驳回或调账）、`CustodyWithdrawalContradiction`（托管方的回调与已结束的提现矛盾，严重；`CustodyCallbacksNeedAttention` 也把 `DISCREPANCY` 计入）、`CustodyDepositUnmatched`（一小时内有地址不属于任何用户或币种对不上网络的到账：前者在后台入账给查明的用户或驳回，后者查币种的网络配置后重放回调）、`CustodyDepositUnbookable`（无主充值账本不肯入账超过 30 分钟：看 wallet-service 日志里的 `deposit ... of nobody`）。

## 端到端

`scripts/e2e/custody.sh`（2026-10-04 起在 `UDUNMOCK` 与 TUSD 上跑，ADR-0017，见上一节）：用地区 `AQ` 注册的新用户看到 `UDUN` 的网络配置（USDT 只开 TRC20、BTC、ETH）与 `TRON-TEST` 上的 TUSD，公开资产列表里没有 TUSD；拿 `TRON-TEST` 地址；模拟网关报 50 TUSD 到账（入账一次，重试与重放不重复），0.5 TUSD 记未入账；伪造签名与过期回调被拒并记录；经公网发到 `https://astras.vip` 的伪造回调都被拒（`udun`：接模拟网关时 wallet-service 的来源名单 403、接真网关后验签 401，`UDUN`、`Udun` 404；`udunmock`、`UDUNMOCK` 由 nginx 403）；运营手工停掉 TUSD 提现时新提现被拒（422 `WALLET_WITHDRAW_SUSPENDED`），解除后照常（自动停提要两次相隔 5 分钟的对账，端到端不等，由单元测试覆盖）；绑定身份验证器后 12 TUSD 经审批交给托管方、`SUBMITTED` → `CONFIRMED` 带交易哈希并结算，托管方为它扣的 1.2 TUSD 从 `GAS_SUPPLY` 入账（脚本先确认 `TRON-TEST` 的手续费单位为 `SELF`，`GAS_SUPPLY` 不到 10 时从 TUSD 的手续费收入挪，至多 20；第一次运行要等这笔提现的 5 TUSD 手续费进了收入）；10 TUSD 发往失败地址 → `FAILED` 资金退回；模拟网关对第三个地址收下提现却丢了应答、重交时以余额不足拒绝、先报审核中、把实际扣的 1.5 TUSD 报成 1500：提现停在 `UNCERTAIN`、资金冻结，之后回调到达照常发出并结算，手续费挂起不入账（高于发出的 11 与 5 倍手续费 25 中较小者），`exchangectl wallet custody-fee --book --amount 1.5` 后入账；最后 `UDUNMOCK` 对账无短缺。故障演练 `custody-callbacks.sh` 同样只用 `UDUNMOCK` 与 TUSD。对账"无短缺"不等于缺口为 0：`admin.sh` 的留存手续费（`lib/held-fees.sh`：模拟网关报 999 TUSD 手续费、一分不扣，之后从 `GAS_SUPPLY` 记 1 TUSD 的手续费）每轮让替身托管方比账本应付的恰好多出 1 TUSD，属设计（`wallet.chain_fees` 里每轮一笔 999 TUSD 核销、一笔 1 TUSD 入账；10-05 零点到 17:30 共 9 轮，缺口从 -6 变到 -15）；所以 `custody-callbacks.sh` 演练前先对一次账，演练后要求缺口与演练前相同（B63）。浏览器冒烟测试的充值页只取 Sepolia 地址（不再向托管方要地址），后台冒烟测试读 `UDUNMOCK` 的「托管方」页与回调（后台会话）。

两个脚本只在 wallet-service 的 `UDUNMOCK` 托管方是模拟网关（容器环境 `UDUNMOCK_GATEWAY_URL` 指向 `udun-mock`）时运行，否则打印 `SKIP` 并以 0 退出；`UDUN` 换成真网关不影响它们，它们也不碰 `UDUN` 的钱（只读它的网络配置、向它的回调路径发伪造回调看是否被拒）。要开关 `wallet.test_assets` 对地区 `AQ` 打开。

## 已知局限

- 公开文档没有提现查询与充值列表接口：提现以回调为准（`SUBMITTED` 超 24 小时告警），漏掉的充值回调靠托管方重试，再不行由管理员核对后补记（见上文「回调丢失时的补记」）。
- 托管方没有测试环境：真网关的应答码、余额格式、`fee` 的币种以正式文档与小额联调为准；每个代币网络第一笔真实提现的手续费要对照区块浏览器确认单位（`custody-fee-unit`），确认前它的手续费一律挂起。
- **接真网关前要定**（审查 2026-10-03）：TRC20、BEP20 的手续费若按主币（TRX、BNB）收，平台没有这两种资产的托管网络，也就没有它们的手续费收入给 `GAS_SUPPLY` 注资，`MAIN` 单位下这些手续费只会挂起、永远记不上账。二选一：确认托管方从另设的手续费账户扣（单位定为 `OUTSIDE`，不记账，平台在账外给托管方充 TRX/BNB）；或者把 TRX、BNB 加为托管资产（网络行），`GAS_SUPPLY` 由它们的手续费收入或一条平台注资路径补。ERC20 按 ETH 收时可以记账（ETH 有托管网络与手续费收入）。

## 协调会话批准记录（2026-10-03，代用户决定；用户次日复核）

编码会话 12:00 提交的联调计划与四个阻塞项，协调会话的决定如下（消息渠道不可靠时以本节为准）：

| 项 | 决定 |
|---|---|
| 步骤 0 代码 | 已完成并部署（`5804b6e`、`14d2412`、`462db60`、`a26989e`）。 |
| 步骤 1 只读探测（`udun coins`、`check-address`） | **批准**。网关拒绝本机 IP 时由用户在优盾后台把 `3.107.113.199` 加白名单。把 `coins` 的币种编码、小数位与余额单位写进本文。 |
| 步骤 2 探测地址（`create-address --yes`） | **批准**，每条链最多一个；这些地址不给用户，到账只会 `UNMATCHED`。 |
| 步骤 3 开回调路径 | **批准**：`custody-callback-allow.conf` 写 `allow all;`，`UDUN_CALLBACK_ALLOWED_IPS` 留空，靠签名与 `limit_req`，`remote_ips` 记来源；几笔真回调后收紧。 |
| 步骤 4 切换 `apps.env` 到真网关 | **须协调会话按 1–3 的结果再放行**；在运维锁内做，备好回退（原 `apps.env` 与模拟地址备份）。 |
| 步骤 5 主网小额测试 | 与用户一起：TRC20 充值 ≥ 12 USDT，再提现 10 USDT；按回调与 tronscan 确认手续费单位；之后收紧回调 IP。用户需先在优盾钱包备好 TRX 作 TRC20 的燃料。 |
| 步骤 6 端到端 | 方案 A：UDUN 真网关只带 USDT/BTC/ETH，`udun-mock` 改带一个隐藏的、仅端到端用的 TUSD，`custody.sh` 改在 TUSD 上跑。 |
| B1 账本基线 | 模拟期充值在账本里的"应在托管方"余额，用 `exchangectl ledger custody-reset`（`manual_adjustment` 之后）记每资产一组 `DEPOSIT_PENDING +X / ADJUSTMENT −X` 的冲销分录，带审计、可逆；对账页注明"模拟资金不在托管方"。 |
| B2 模拟期 UDUN 地址 | 先备份（表导出留在服务器 `infra/backup/`），再退役；用户下次取地址拿真地址。 |
| B3 模拟余额 | 切换后 USDT/BTC/ETH 提现暂停（`wallet withdrawals-suspend`），仅用户测试窗口放开，且窗口内托管提现全部人工审核。 |
| B15 TRX/BNB 燃料 | TRC20、BEP20 的手续费单位定为 `OUTSIDE`（托管方从商户钱包的 TRX/BNB 扣，平台不记账、账外充值），BTC、ETH 为 `SELF`；以步骤 5 的链上记录确认后再 `custody-fee-unit`。 |
| B10 `expected=0` | 步骤 1 看清托管方的余额单位后再补；在此之前基线重置后的第一次对账由人工核对。 |
| B7a 未匹配充值 | 进 `UNCLAIMED_DEPOSIT` 的待处理账户，须在放真实用户之前落地，不阻塞步骤 1–6。 |
| 审查 AF Medium | `custodyfee.go:78` 附近：按主币计价、而平台未托管该主币的挂起手续费（如 TRC20 的 TRX）不得再按代币口径解释短缺（应为 0），步骤 5 之前改好并补测试。 |
| B7a 设计（18:30） | **批准**编码会话的方案：无主充值行（`NoOwner` 哨兵只由此路径写入，`UNKNOWN_ADDRESS`，`CONFIRMED`，按 provider_tx_id 幂等），经既有未认领路径记入 `UNCLAIMED_DEPOSIT`（分录键确定），不发 DepositDetected/DepositConfirmed。条件：凡期待用户的地方都拒绝 `NoOwner`（ReleaseUnclaimed 到 NoOwner、资格与风控查询），并有单测证明哨兵到不了 notification；对账把 `UNCLAIMED_DEPOSIT` 计入"应在托管方"；指标 `wallet_deposits_unmatched_total` 与告警 `CustodyDepositUnmatched` 连"没有资产可记"的币一起算；钱包提供一个内部接口（新文件）一次完成"设持有人 + ReleaseUnclaimed"并审计，后台页由后台会话做（后台待办 ㉑）；迁移 00012 前先 fetch origin/main。不采用只记账不建行的替代。 |
| 审查 AH（0ffbcf1，19:40） | 无 Critical/High，工具可留在测试服，切换按 runbook 顺序使用（锁 → 停提 → 退役 → 以最近一次对账的 HELD 为 X、固定 `--key` 重置 → 重启 → 按 B10 人工核对第一次对账）。**步骤 4 之前补**：① `retire-addresses` 加确认开关并在 `UDUN_GATEWAY_URL` 不是模拟网关时拒绝（现在只要 `--reason` 就删光 `provider='UDUN'` 的行，切换后误跑会把真地址退役成 `UNMATCHED`）；② `custody-reset --amount` 超过该托管方最近一次 `chain_checks.chain` 时警告或要求 `--force`（多出的部分会无声地压低预期，B4 看不见）；③ `--reverse` 的"不能超过已重置额"改到服务端（现只在 CLI）；④ `custody.sh` 对 `udun` 小写路径只接受 401/403（404 会让路由缺失也通过）。Low：`ON CONFLICT (network, address)` 不覆盖 `lower(address)` 唯一索引（大小写变体会整笔回滚，理论上）；后台对账页还没显示 SIMULATED 列（后台待办 ㉑ 附）。 |
| 审查 AJ（7ef8bf5 B7a、54fac93 防呆、56e491b，21:10） | 无 Critical/High；构建、vet、lint 干净。B7a 六项条件 5 项 ✓（哨兵只在 `custody.go:292` 写入、各处拒绝、`TestADepositOfNobody` 证明无用户事件；`UNKNOWN_ADDRESS`/`CONFIRMED`/按 provider_tx_id 幂等、分录键 `deposit:<id>`；预期 = −(DEPOSIT_PENDING+WITHDRAWAL_PENDING) 天然计入 UNCLAIMED_DEPOSIT；指标与告警含无资产情形；迁移 00012 与账本 00006 不冲突；退役地址的来款记为无主并带原持有人）。AH ①–④ ✓（`--yes` 且仅模拟网关；超过最近链上余额要 `--force`；服务端按 `custody-reset:` 键净额限反向；`udun` 路径只认 401/403）。**真实用户之前补**：Medium——`AssignDeposit`（`admin_unmatched.go:52-84`）在账本已付给用户后若 Update/Audit/Emit 失败，没有像 `CreditDeposit` 那样的"已释放未记录"补救（`UnclaimedRelease`/`RecordRelease`），行会停在无主而钱已付、换人分配撞幂等冲突，只能再分配给同一人；Low：无主入账一笔永久失败会卡住该网络后续已确认充值的轮次（`scanner.go:405`，用户充值走 DLQ 隔离）；`standIn` 读的是 CLI 进程自己的 `UDUN_GATEWAY_URL`，退役与重置要在 wallet-service 容器里跑；站点缺 `UNKNOWN_ADDRESS` 文案（pc/m i18n，编码会话）；后台契约 `admin.yaml` 还没有 `address_owner`/`address_owner_retired` 与分配路由（后台会话 ㉑ 先补契约）；后台列表每条无主行多 1–2 次查询。 |
| 用户决定（21:55） | **USDT 先只走 TRC20**：优盾钱包目前只有 TRON 的 USDT（195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t），ERC20/BEP20 的 USDT 网络不开放充提、配置保留；以后在优盾后台加了币种再开。BTC（0:0）、ETH（60:60）照设计托管；BNB（2510:2510）与 TRX（195:195）不是平台资产，只作燃料。步骤 2 的探测地址只建 TRON、BTC、ETH 三条链。 |
| 步骤 4 放行（23:05） | 步骤 1–2 结果已核（`63b1d78`：商户只有 BTC 0:0、ETH 60:60、TRX 195:195、USDT-TRC20、BNB 2510:2510；`balance` 一律 null → 对账"不比较"而非 0；TRON/BTC/ETH 各一个探测地址，不要用；TRC20 独开、BEP20 编码 2510）；审查 AJ 的修复 `7cabdbe` 已推送。**放行步骤 4**，条件：① 先部署 `7cabdbe`，并在模拟网关上最后跑一遍 `custody.sh` 与 `web.sh`（绿）；② 先把遗留的挂起手续费 `UDUN:1791031535827261` 按实扣 1.5 USDT 入账，让模拟方短缺归零再读 X；③ 运维锁内按上表顺序：导出 `provider='UDUN'` 的地址行到 `infra/backup/`（带时间戳，0600）→ B3 暂停 USDT/BTC/ETH 提现（理由写明只在用户测试窗口解除）→ 在 wallet-service 容器里退役地址 → 对账一次、各资产的 HELD 即 X → `custody-reset` 用固定键 `switch-2026-10-03-<asset>`（X 为 0 的资产不记）→ 备份 `apps.env` 后并入 `udun-real.env` 的 `UDUN_*`（`UDUN_CALLBACK_ALLOWED_IPS` 留空）→ 只重启 wallet-service；④ 核对：`wallet_custody_up`=1、对账跑过（余额 null 时三个资产"不比较"属预期，`CustodyNotCompared` 会响）、无短缺、测试账户在 TRON/BTC/ETH 拿到真地址（格式过 `check-address`）、伪造的公网回调得 401（签名把关）、三项停提在位；⑤ 任一项不过立即按表回退（恢复 `apps.env` 并重启、恢复地址、`--reverse` 冲回、解除停提）并报告；⑥ `udun-mock` 容器保留，`custody.sh` 此后 SKIP 直到方案 A；⑦ 切换后到步骤 5 之前不再碰托管方，第一次对账人工核对（B10）。步骤 5 由协调会话与用户另约（用户先备 TRX 燃料，TRC20 充 ≥ 12 USDT，再提 10 USDT）。`CLAUDE.md` 里"BSC 9006 待确认"一句待用户或协调会话更正为 2510。 |
| 用户决定（23:45） | **步骤 5 放到全部开发完成之后**，步骤 4 随之推迟（没有测试的切换只换来三个资产"不比较"的告警和跳过的 `custody.sh`）；两步最后与用户一起做，仍按上一行的条件与核对项。此前测试服继续用 `udun-mock`；`udun-real.env` 留在服务器不启用；探测地址照旧"不要用"。**方案 A 提前**：现在就在模拟网关上加第二个托管方 `UDUNMOCK` 与只给端到端用的隐藏资产 TUSD、`custody.sh` 改在 TUSD 上跑，UDUN（USDT/BTC/ETH）在切换前仍指向模拟网关，这样切换时不丢端到端覆盖。遗留的挂起手续费照常按 1.5 入账。 |
| 方案 A 设计批准（10-04 00:10） | 编码会话 00:00 的方案照准：① wallet-service 同时接 `UDUN` 与 `UDUNMOCK` 两个托管方，各自的处理器、租约、指标标签、回调来源名单、币种缓存与 `UDUNMOCK_*` 设置；`udun-mock` 只读 `UDUNMOCK_*`，切换时只改 `UDUN_*`；`UDUNMOCK` 回调路由在网关公开但由 wallet-service 限在容器网络，nginx 对公网的 `/v1/wallet/callbacks/udunmock` 直接 403；instrument 迁移先 fetch origin/main 取 max+1。② TUSD（6 位小数）一个网络 `TRON-TEST`（provider `UDUNMOCK`、`195:<固定假合约>`），`WALLET_FALLBACK_PRICES` 加 TUSD:1。③ 资产新字段 `hidden`：隐藏资产不进任何公开列表（assets、summary、tickers、sparklines、自选、站点资产资料），`instruments apply` 拒绝给隐藏资产建交易对或合约，也拒绝把 `UDUNMOCK` 配给非隐藏资产；隐藏资产的充提只对具备 `TEST_ASSETS` 资格的用户开放（user-service 映射到开关 `wallet.test_assets`，测试服只对地区 AQ 打开，沿用 `risk.enforce` 先例；`custody.sh` 的用户注册为 AQ），资格判断失败一律 404、开关默认关。④ 手续费从 `GAS_SUPPLY` 出，`custody.sh` 在首笔 TUSD 提现结算后从手续费收入挪款。⑤ 浏览器冒烟测试现在就离开 UDUN：PC/手机取 ETH Sepolia 地址（本地派生），后台冒烟读 `UDUNMOCK` 的托管方页与回调行；TRC20 流程由 `custody.sh` 在 TUSD 上覆盖。⑥ 新增 ADR-0017（替身托管方与隐藏测试资产），更新 custody/instruments/wallet/feature-flags/testing 手册。⑦ 后台契约（`?provider=`、回调与手续费 JSON 带 provider、`Network.provider` 加 UDUNMOCK、`Asset.hidden`）由编码会话在 ① 推送后发给后台会话。 |
| 审查 AL（18db132、63b1d78、7cabdbe，10-04 00:40） | 无 Critical/High；lint 0 问题，单测通过；三者都可留在测试服。核实：手续费后台接口与 CLI 走同一条 `DecideCustodyFee`/`ResolveCustodyFee` 与同样两条审计，入账只经 `BookChainFee` 的幂等分录（`chain-fee:<tx_hash>`，GAS_SUPPLY），核销不可重复（409），`/internal` 不在网关路由、nginx 只转 `/v1/` 与 `/admin/v1/`、容器不发布端口；对账的"剔除未上架币种"只作用于托管方持有一侧，预期侧与在途不变，只会放大短缺不会藏住；`balance` 为 null → 不比较 → `CustodyNotCompared`，B4 不触发（正确）；`Payee` 按精确分录键取正向 USER 行，同人补记、他人 409 只带 ID。**切换前补**：Medium——被账本永久拒绝的无主充值每 5 秒重试并 Warn 一次（`scanner.go:406-410`、`main.go:417-431`），无退避、无指标告警，一天约 1.7 万行日志 → 加退避或只在状态变化时记一次，并出指标。Low：`limit > 200` 回落到 50 而非夹到 200（`admin_fees.go:25-27`，与手册"最多 200"不符）；未知游标返回空页而非 400、`w.id::text = f.reference` 用不上主键索引；404/409 用 `COMMON_*` 且两种 409 原因共用一个码（附录 C 风格应为 `WALLET_*`）；三条新路由没有 handler 测试；`ErrReleasedToAnother` 与 `ErrReleasedUnrecorded` 共用 `WALLET_DEPOSIT_RELEASED`，后台要按 `user_id` 细节分支；"不比较"时 `wallet_custody_{held,expected,shortfall}` 保留旧值（既有问题）。 |
| 审查 AO（f8934c0 方案 A 第 1 步，10-04 01:35） | 无 Critical/High；lint 0 问题，单测通过；UDUN 行为无回归（只有预期的改动：总览只看本托管方的对账、attention/停提指标按托管方）。条件 ①③ ✓（按前缀读设置、`Custodians[provider]` 与适配器 `Name` 一致、租约 `wallet-custody:<PROVIDER>`、指标常量标签 `provider`、来源名单与币种缓存按托管方、回调按路径分派并用该托管方的密钥验签；udun-mock 只读 `UDUNMOCK_*`）；② partial：网关路由沿用既有的 `/v1/wallet/callbacks/{provider}`、wallet-service 的来源门有测试，但 **nginx 对公网 `/v1/wallet/callbacks/udunmock` 的 403 还没加**（现靶：公网来源被 172.18.0.0/16 门挡在任何写库之前，属纵深防御）；④⑤ 不在本提交：instrument 的 UDUNMOCK 放行与迁移 00007、"UDUNMOCK 只能配给隐藏资产"规则在工作区未提交——**须同一提交落地**（在此之前不可能配出 UDUNMOCK 网络，UDUNMOCK 处理器空转，安全）。Medium（部署前置条件）：`UDUNMOCK_GATEWAY_URL` 一设就要求 `UDUNMOCK_MERCHANT_ID/API_KEY/CALLBACK_URL`，udun-mock 只有一个商户，切换前 `UDUN_*` 必须与 `UDUNMOCK_*` 相同——测试服 apps.env 已如此（协调会话 01:35 核对：两对相同，容器健康），但 `custody.md:164`、`server-deploy.md:27` 仍写模拟网关共用 `UDUN_*`，按它新建服务器会起不来 → 改文并写明相等要求。Low：`wallet_withdrawals_suspended` 只报托管方服务的资产；ADR-0017 在代码、compose、`.env.example` 与手册里被引用但尚未写；`?provider=FOO` 返回 200 `configured:false` 而非 404；缺测试：无网络资产的 `Holdings` 短路、按托管方的币种缓存、`Custody()` 过滤、postgres `Attention/Page` 的 provider 过滤、同一注册表两个处理器。 |
| 审查 AQ（56156f8 方案 A 第 2 步，10-04 03:00） | 无 Critical/High；lint 0 问题，单测通过；UDUN 行为无回归。条件 (a) 隐藏资产不进公开列表 ✓（`/v1/market/assets` 与 logo 过滤；summary/tickers/sparklines/自选按交易对，隐藏资产不能有交易对与合约）；(b) UDUNMOCK 只能配隐藏资产、隐藏资产不能有交易对/合约、取消隐藏被拒 ✓，迁移 00007 唯一且 CHECK 与 `hidden` 同一迁移放宽 ✓；(d) nginx 对公网 `udunmock` 路径 403 ✓（两个用户站 server 块都含，`custody.sh:155` 断言）；(e) 冒烟改 Sepolia ✓；(f) ADR-0017 与手册 ✓（过期的 `UDUN_*` 共用文案在 605d572 修正）。(c) **partial，Medium**：资格判断出错时没有收敛成 404——`networks.go:28-34,43-46`、`adapters/users/users.go:19-25` 原样返回 gRPC 错误：user-service 不可达时 TUSD 的地址/校验/提现得 503，旧版 user-service 得 400；更要紧的是 `NetworksFor` 直接返回错误而不是去掉隐藏行，于是 user-service 一重启，所有登录用户的 `GET /v1/wallet/networks`（充值、提现页）都 503。要求：`Check` 出错 → `visible` 视为 `ErrUnknownNetwork`（记日志）、`NetworksFor` 返回过滤后的列表；可按用户缓存 TEST_ASSETS 结果约 60 秒（现在每次 networks 调用都多一次 user-service 往返）。Low：`custody.sh` 只注册 AQ 用户，缺一个 SG 用户期望 404 的反向用例；00007 的 Down 在 TUSD 的 UDUNMOCK 网络行存在时会失败；"UDUNMOCK ⇒ hidden"只在应用层（apply/gRPC 是唯一写入方，接受）。其余核对：test.json 的 TUSD 设置与重生成保留 `hidden` ✓；背书资产列表含 TUSD（HOUSE 不能卖空、无交易对，无害）✓；组合估值不含 TUSD、只有端到端用户持有 ✓；gRPC 列表不过滤 ✓；UDUNMOCK 对账覆盖 TUSD ✓；SKIP 守卫看容器内 `UDUNMOCK_GATEWAY_URL` ✓；手续费从 GAS_SUPPLY、补充经账本分录 ✓。部署后需 `wallet.test_assets --on --allow-regions AQ`（编码会话已于 18:05Z 打开）。 |
| 审查 AR（605d572 AL/AO 修复批，10-04 03:10） | 无 Critical/High；各项声明都在代码或测试里核实：无主充值的退避按充值 ID、内存态、1 分钟起翻倍到 1 小时、入账或驳回后清除；指标 `wallet_custody_deposits_unbookable`（常量标签 provider）与告警 `CustodyDepositUnbookable`（30 分钟）；"不比较"时用 `DeleteLabelValues` 删序列、从未设过也不会 panic；`WALLET_DEPOSIT_RELEASED_TO_USER` 的拆分不影响后台的同人补记（后台按 KindConflict 再读充值比对 user_id 与 release_journal_id，不按码分支）；手续费三个新码、`limit` 夹到 200、未知游标 400、按提现主键的联接、JSON 带 `provider`（契约已有）、未知 `?provider=` 404、AO 要求的测试、手册与 server-deploy 的 `UDUNMOCK_*` 文案都 ✓；lint 0 问题，单测通过。Low：`retries.keep()` 只在 `requestCredits` 跑到末尾时执行，资格判断出错或用户充值写入失败的提前返回会跳过它，已驳回/已入账的无主充值会在指标里多留一轮（user-service 停 30 分钟以上会误报）→ 提前返回前也 keep；`alerts_test.yml` 没有 `CustodyDepositUnbookable` 用例；`.env.example` 还没有 `UDUNMOCK_MERCHANT_ID/API_KEY`（硬性约定 1 的变量名清单）；nit：`admin_fees.go:50-53` 键里没有冒号时 provider 会取整个哈希（不可达）。后台侧：四个新错误码在后台没有文案（落到"未知错误"），`admin.yaml` 驳回 409 的说明仍只写 `WALLET_DEPOSIT_RELEASED`（后台待办 A30）。 |
| 审查 AT（d1217a8 持有人写库修复、852b81b 手续费按托管方，10-04 04:50） | 两者安全：`user_id` 只在 NoOwner→用户这一步写入（`CASE WHEN user_id = NoOwner THEN $19 ELSE user_id`，行持 FOR UPDATE），10 处 `Deposits().Update` 调用都先从库里读行、没有一条会把空持有人写回去；store 回归测试（nobody→alice 持久、alice→其他人保持 alice）；`deposits_check2` 与 `deposits_release_check` 由真实分配满足；手续费过滤为参数化谓词、游标单行、`limit+1` 正确；lint 0 问题，单测通过。**Medium（数据）**：修复前被分配过的行无法经任何接口修复（AssignDeposit → `WALLET_DEPOSIT_NOT_RELEASABLE`，CreditDeposit → `NO_OWNER`，Dismiss 不在待处理里）——测试服恰有一行：`wallet.deposits` `01a10324-8533-7036-9c66-776411a7cd5b`（TUSD 1.0，CREDITED/CREDITED，已释放，user_id 为空，19:01:43Z admin.sh 第 ㉑ 例产生）→ 编码会话在运维锁内按释放分录的收款人一次性 UPDATE，并把 SQL 与证据记进「进展」；后台侧核对无"已付但 FAILED"的资金操作。Low：`/internal/wallet/custody/fees?provider=FOO` 返回 200 空表而 `/custody?provider=FOO` 404（求一致）；单测的内存 store 整对象存取、测不出该 bug（只有 store 测试与 admin.sh ㉑ 覆盖）。过程提醒：d1217a8 自己的集成测试夹具推上去时是红的、15 分钟后才修——资金路径的修复推送前要先跑所涉包的集成测试（`task ci` 不含）。CI 自 605d572 起红的两处是测试本身的问题（改码后的旧文案、无效夹具），未掩盖产品缺陷。 |
