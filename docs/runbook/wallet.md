# 钱包：充值、签名、归集与对账（wallet-service 与 signer）

实施计划 §6.3 任务 9–10，需求 §5.10、§11.4–§11.6，ADR-0001（余额只经账本）、ADR-0003（私钥只在 signer）。

## 组成

```
signer init（一次性，服务器上）── keystore.json（助记词，scrypt + AES-256-GCM）
        │ 打印充值账户 xpub（m/44'/60'/0'）
        ▼
apps.env WALLET_XPUB ──> wallet-service ──分配地址 m/44'/60'/0'/0/i（公钥派生，不碰私钥）
                           │ 扫描器（Alchemy Sepolia JSON-RPC，每 30 秒，数据库租约保证单实例）
                           ▼
              wallet.deposit.events：DepositAddressAssigned / Detected / Confirmed / Credited / Orphaned / Rejected
                           │ DepositConfirmed
                           ▼
              ledger-service：DEPOSIT_CREDIT（键 deposit:<id>）DEPOSIT_PENDING → 用户 SPOT 或 UNCLAIMED_DEPOSIT
                           │ ledger.events EntryPosted
                           ▼
              wallet-service 标记 CREDITED / REJECTED 并发 DepositCredited ──> notification-service（站内信、未入账加邮件）
                                                                         └─> api-gateway（私有频道 deposits；余额走 balances）
```

- 服务：wallet-service，HTTP 8092（网关转发 `/v1/wallet/*`），运维 9092，schema `wallet`。
- 接口：`GET /v1/wallet/deposit-address?asset=ETH&network=ETH-SEPOLIA`（首次请求分配下一个派生地址，同一网络的所有资产共用一个地址）、`GET /v1/wallet/deposits`（新到旧，`cursor`/`limit`），契约 `api/openapi/wallet.yaml`。H5 充值页 `/deposit`（资产页"充值"按钮进入）。
- 测试服目前只开放 ETH（Sepolia，12 个确认，最小充值 0.001 ETH），见 `deploy/instruments/test.json`。

## 密钥与 keystore

- 助记词（BIP-39，24 词）只在 signer 的 keystore 里，文件用口令经 scrypt（N=2^18）派生的密钥做 AES-256-GCM 加密，权限 0600。业务服务只拿 xpub，`evm.NewDeriver` 拒绝 xprv。
- 派生路径：充值账户 `m/44'/60'/0'`，用户地址 `m/44'/60'/0'/0/i`（`deposit_addresses.derivation_index`，按网络递增、永不复用）；热钱包是账户 1（任务 10）。
- 测试服位置：`/opt/exchange/infra/signer/`（目录 700，属主为镜像里的 app 用户 uid 10001），`keystore.json` + `signer.env`（`SIGNER_PASSPHRASE`，600，随机生成，从不打印）。

首次创建（已在测试服做过，重复执行会因文件已存在而失败）：

```bash
ssh exchange 'cd /opt/exchange/infra && sudo install -d -m 700 -o 10001 -g 10001 signer \
  && printf "SIGNER_PASSPHRASE=%s\n" "$(openssl rand -base64 32)" | sudo tee signer/signer.env >/dev/null \
  && sudo chmod 600 signer/signer.env && sudo chown 10001:10001 signer/signer.env \
  && sudo docker run --rm --env-file signer/signer.env -v "$PWD/signer:/keystore" exchange-app:latest /app/signer init --keystore /keystore/keystore.json'
```

打印出的 xpub 追加到 `apps.env` 的 `WALLET_XPUB=`（先备份 apps.env），然后 `sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml up -d wallet-service`。再次查看 xpub：同一 docker run 把 `init` 换成 `xpub`。

备份：keystore 文件与口令分开保存；两者任一丢失，充值地址里的资金就无法再动用（测试网无真实价值，但流程按生产要求执行）。更换 keystore 会改变全部充值地址，已分配给用户的旧地址仍会被扫描，但资金只能用旧 keystore 归集。

## 签名服务（signer serve）

- 容器 `signer`（gRPC 9193，只给 wallet-service；运维 9093），启动时用 `signer.env` 的口令打开 keystore（scrypt 一次约 256 MiB，容器内存上限 384 MiB），口令用完即丢；schema `signer` 记审计。
- 只签两类 EIP-1559 交易，规则在签名服务自己这边（`internal/signer/domain/policy.go`），不依赖 wallet-service 的判断：
  - 提现（`WITHDRAWAL`）：从热钱包（`m/44'/60'/1'/0/0`）付出，必须带提现单 ID 与审批人，不能付给热钱包自己；单笔不超过 `SIGNER_MAX_WITHDRAWAL`（默认 1 ETH），24 小时内按提现单去重累计不超过 `SIGNER_DAILY_WITHDRAWAL`（默认 5 ETH）。同一提现单的所有签名必须同一 nonce、同一收款地址与金额，替换交易只能提高费用，因此一笔提现不可能被签出两笔不同的支付。
  - 归集（`SWEEP`）：从充值地址 `m/44'/60'/0'/0/i` 付出，收款方只能是热钱包。
  - 通用：链 ID 必须是 `ETH_CHAIN_ID`，每 gas 费用不超过 `SIGNER_MAX_FEE_GWEI`（默认 200），gas 上限 `SIGNER_MAX_GAS`（默认 100000），暂不签带 calldata 的交易（代币转账随代币提现/归集再开）。
- 幂等：同一 `request_id` 同样内容返回第一次的签名，不同内容 `COMMON_IDEMPOTENCY_CONFLICT`。所有签名写 `signer.signatures`，所有拒绝写 `signer.refusals`，两表只能追加（触发器禁止改删与 TRUNCATE）。
- 测试环境的已知差距：gRPC 在内部网络上没有 mTLS，靠签名服务自身规则兜底；生产环境需独立主机/网络与双向 TLS（或 KMS/HSM）。

## 归集、注资与链上对账

wallet-service 中持有扫描租约的实例在每轮扫描后执行"操作"：运维用 `exchangectl` 排队的命令（归集、注资、对账）、跟踪已广播归集的回执、把链上 gas 记账、每小时一次链上对账、每 5 分钟读一次热钱包余额。命令记在 `wallet.commands`，`exchangectl wallet commands` 查看结果。

```bash
# 测试服上（任一带 exchangectl 的容器，读的是 wallet schema）
ssh exchange sudo docker exec exchange-infra-wallet-service-1 /app/exchangectl wallet sweep            # 余额不低于最小充值额的充值地址全部归集
ssh exchange sudo docker exec exchange-infra-wallet-service-1 /app/exchangectl wallet sweep --min 0.01
ssh exchange sudo docker exec exchange-infra-wallet-service-1 /app/exchangectl wallet fund --tx 0x...  # 把平台转入热钱包的一笔记到 GAS_SUPPLY
ssh exchange sudo docker exec exchange-infra-wallet-service-1 /app/exchangectl wallet reconcile        # 立即对账；wallet checks 看最近结果
ssh exchange sudo docker exec exchange-infra-wallet-service-1 /app/exchangectl wallet commands
```

- **归集**：对每个余额 ≥ 阈值且没有进行中归集的充值地址，按"2 × 基础费 + 小费"的费用上限预留 21000 gas，余额减去预留全部转到热钱包；签名后先落库（`wallet.sweeps`）再广播，节点丢失的交易一分钟后重发。实际 gas 与预留的差额作为零头留在充值地址。归集不产生账本分录（都是平台钱包），但 gas 是真实的链上流出，挖出后记入 `wallet.chain_fees`，再由账本记 `GAS_SUPPLY → WITHDRAWAL_PENDING`（分录类型 `WITHDRAW_SETTLE`，键 `chain-fee:<tx>`）。
- **注资 GAS_SUPPLY**：`GAS_SUPPLY` 不能为负，没钱时 gas 暂不入账（`wallet_chain_fees_unbooked`、告警 WalletChainFeesUnbooked）。平台从外部地址向热钱包转一笔 ETH，确认数达到网络要求后执行 `wallet fund --tx`：核对交易付给热钱包、成功、不是来自充值地址（归集不是注资），记 `DEPOSIT_CREDIT`（`DEPOSIT_PENDING → GAS_SUPPLY`，键 `fund:<tx>`），同一笔只记一次；确认数不够时命令保持 PENDING，后续轮次自动完成。
- **链上对账（不变量 4）**：链上持有 = 热钱包 + 所有充值地址的余额；账本预期 = `−(DEPOSIT_PENDING + WITHDRAWAL_PENDING)`；已付未记账的 gas 解释差额的一部分；`缺口 = 预期 − 持有 − 未记账 gas`，大于 0 即平台钱包少钱（告警 WalletChainShortfall，critical）。小于 0 是正常的盈余：已发现未入账的充值、已广播未挖出的提现、尚未 `fund` 的平台转账、合约内部转入等。测试环境的模拟资金来自 `ADJUSTMENT`，不影响这一核对。结果写 `wallet.chain_checks`，指标 `wallet_chain_balance`、`wallet_chain_expected`、`wallet_chain_shortfall`。
- **热钱包余额**：`wallet_hot_wallet_balance`；低于 0.005 ETH（WalletHotWalletLow）时提现会停在 APPROVED，需要归集或注资；高于 1 ETH（WalletHotWalletHigh）应人工转冷。

## 提现

状态机（§11.6）：`REQUESTED → PENDING_REVIEW / APPROVED → SIGNING → BROADCAST → CONFIRMING → CONFIRMED`；分支 `REJECTED`、`CANCELED`（签名前用户撤销）、`FAILED`；站内地址 `APPROVED → CONFIRMED`（账本 `INTERNAL_TRANSFER`，不签名不广播）。风控评分在申请时同步完成，没有单独的 `RISK_SCORING` 停留态。

- **地址簿**：`POST /v1/wallet/withdraw-addresses`（需 step-up）；新地址冷却期后才能用（`WALLET_WHITELIST_COOLDOWN`，生产 24 小时，测试服 1 分钟）；不能添加自己的充值地址。
- **申请** `POST /v1/wallet/withdrawals`（需 step-up，已绑定身份验证器时只能用 TOTP 证明）：eligibility `WITHDRAW`（开关 `wallet.withdraw`，默认关，测试服已开）→ 资产与网络开放提现、只支持链上原生币 → 地址格式 → 精度与最小提现额 → 地址在地址簿且过了冷却期 → 当日价格折算 USDT（当天第一次取价后固定，存 `price_snapshots`；来源 market-data 的 `ASSET-USDT` 参考价，没有新鲜参考价时用 `WALLET_FALLBACK_PRICES`）→ 日/月限额（双身份 + TOTP：2,000 / 20,000 USDT，否则 20%）→ 风控规则 → 冻结金额 + 手续费（`WITHDRAW_FREEZE`，键 `withdraw:<id>`）。账本拒绝冻结时记为 REJECTED 并返回账本错误码；账本不可达时停在 REQUESTED，处理器一分钟后用同一个键补完。
- **风控规则**（`internal/wallet/domain/withdrawal.go`）：新账户（< 72 小时）、新设备（该会话设备首次登录 < 24 小时）、近期安全变更（换绑或改/重置密码 < 24 小时）、新地址（加入地址簿 < 72 小时）、大额（> 1,000 USDT）、当日累计超过日限额一半，任一命中即 PENDING_REVIEW 需一人批准；> 20,000 USDT 需两人。安全上下文由 auth-service 在兑换 step-up 时一并返回（`ConsumeStepUp` 的 `security`）。
- **审批**（管理后台前用 `exchangectl`，每次审批/拒绝写 `audit.events`）：

  ```bash
  ssh exchange sudo docker exec exchange-infra-wallet-service-1 /app/exchangectl wallet withdrawals             # 待审核
  ssh exchange sudo docker exec exchange-infra-wallet-service-1 /app/exchangectl wallet withdrawals --status ALL
  ssh exchange sudo docker exec exchange-infra-wallet-service-1 /app/exchangectl wallet approve <id> --reviewer alice --reason "..."
  ssh exchange sudo docker exec exchange-infra-wallet-service-1 /app/exchangectl wallet reject <id> --reviewer alice --reason "..."
  ```

  同一审核人不能重复批准；被拒绝、撤销或上链前失败的提现由处理器解冻（`WITHDRAW_UNFREEZE`，键 `withdraw-release:<id>`）。
- **发送**（处理器，按创建顺序逐笔）：链上手续费上限（`WALLET_MAX_FEE_GWEI`，默认 100）以内、热钱包够付金额 + gas 时才发送，否则停在 APPROVED（`wallet_withdrawals_waiting`、告警 WalletWithdrawalsWaiting），并且挡住后面的提现，保证 nonce 连续。nonce 取"库里记录的下一个"与"节点 pending 计数"的较大者，签名成功后才占用，签名失败不会留下空洞。状态先置 SIGNING（此后不可撤销）→ 签名服务签名 → 先落库（`withdrawal_attempts`）再广播 → 账本 `WITHDRAW_SETTLE`（§11.6：广播成功即扣减；键 `withdraw-settle:<id>`，失败会重试）。
- **确认与替换**：任一尝试的回执出现即开始计确认数，满网络确认数（Sepolia 12）为 CONFIRMED，实际 gas 记入 `chain_fees` 由账本记 `GAS_SUPPLY → WITHDRAWAL_PENDING`。10 分钟（`WALLET_REPLACE_AFTER`）未上链则用同一 nonce、费用提高 25%（或市场价，取高者，不超过上限）签发替换交易，所有尝试的 tx_hash 都保留；节点丢失的交易一分钟后重发。签名服务拒签（如超过它的每日限额）或链上执行失败的提现置为 FAILED 并告警，需人工处理（上链前失败的会自动解冻）。
- **站内地址**：收款地址是平台其他用户的充值地址时手续费为 0；批准后账本记 `INTERNAL_TRANSFER`（付款方冻结 → 收款方可用），提现直接 CONFIRMED，收款方生成一条 `kind = INTERNAL` 的充值记录（`tx_hash` 为 `internal:<提现 ID>`），双方都有通知。
- **通知与推送**：申请、完成、拒绝、失败发站内信加邮件，撤销只发站内信；私有频道 `withdrawals` 推送每次状态变化。
- **端到端**：`scripts/e2e/withdraw.sh`（约 6–7 分钟）：绑定身份验证器、加两个地址、冷却期内被拒、向端到端发送方提现 0.0011 ETH（人工批准后签名、广播、12 个确认，链上余额恰好增加 0.0011）、向另一个新用户的充值地址提现 0.02 ETH（站内划转）、第三笔审核中撤销。热钱包的钱来自归集（脚本开头排一次 `wallet sweep`）和 GAS_SUPPLY 注资。

## 扫描与状态机

- 游标：`scan_cursors` 记已扫描的最高块；首次启动从当前高度开始（`WALLET_SCAN_START` 可指定起点），不回扫历史。
- 每轮：读链头，最多扫 20 块；每块取完整交易，找发往本平台地址、金额 > 0 的交易并查回执确认成功（合约内部转账看不到）；再用一次 `eth_getLogs`（ERC-20 `Transfer`，接收方为本平台地址，任意合约）覆盖这些块。Alchemy 免费档 `eth_getLogs` 一次最多 10 块、地址按 500 个一组分批。
- 父哈希校验：新块的 `parentHash` 必须等于已存的上一块哈希（`scanned_blocks` 保留最近 128 块）。不一致就回退 `确认数 + 6` 块（Sepolia 为 18 块）重扫，回退范围内仍待确认的充值记为 ORPHANED；重扫时交易再次出现则回到 DETECTED（重新计确认数），已入账的充值只跟随新区块号。
- 同一笔转账 `(network, tx_hash, log_index)` 只有一条记录（原生币 `log_index = -1`）。
- 确认数只按已校验过父哈希的块计算（不按链头），满 `confirmations` 进入 CONFIRMED；之后请求入账：资产与网络都开放充值时发 `DepositConfirmed`，否则挂起（`wallet_deposits_held`），重新开放后自动继续。
- 分流（§11.5、§5.4）：低于最小充值额 → 标记 `BELOW_MINIMUM`，记入 `UNCLAIMED_DEPOSIT`；账户已注销（eligibility `DEPOSIT` 返回 `USER_CLOSED`）→ `ACCOUNT_CLOSED`，同样记入 `UNCLAIMED_DEPOSIT`；未上架代币 → REJECTED（`UNSUPPORTED_TOKEN`），不入账本（账本只接受已配置资产），通知用户，人工评估找回；精度截断后为 0 的零头同样 REJECTED。冻结账户照常入账（§5.4：入账但不可动）。
- 入账：账本 `DEPOSIT_CREDIT` 的幂等键 `deposit:<id>`，重复投递只会重放；钱包看到 `EntryPosted` 后把充值置为 CREDITED（或 REJECTED）并发 `DepositCredited`。

## 调用量估算（Alchemy 免费档）

每轮约 `eth_blockNumber`（10 CU）+ 每块 `eth_getBlockByNumber`（16 CU）+ 一次 `eth_getLogs`（75 CU，没有地址时不调用），命中充值时每笔加一次回执（15 CU）。30 秒一轮、每轮约 2.5 块时约 36 万 CU/天、1100 万 CU/月。本机开发栈默认不扫链（`scripts/dev.sh` 去掉了端点，`DEV_SCAN=1` 才保留），避免两个扫描器翻倍用量。

## 配置

| 变量 | 说明 |
|---|---|
| `WALLET_XPUB` | 充值账户 xpub（apps.env）；不设则不分配地址（`WALLET_UNAVAILABLE`） |
| `ALCHEMY_SEPOLIA_HTTPS_URL`、`ETH_CHAIN_ID` | 节点地址（含 API key，日志与错误里从不出现）与链 ID；启动扫描前核对 `eth_chainId`，不符则每分钟报错重试、不扫描 |
| `WALLET_NETWORK` | 扫描的网络，默认 `ETH-SEPOLIA` |
| `WALLET_SCAN_INTERVAL`、`WALLET_SCAN_START` | 扫描间隔（默认 30s）、首次起点（默认 0 = 当前高度） |

## 指标与告警

- `wallet_scan_block`、`wallet_scan_lag_blocks`、`wallet_scan_last_success_timestamp_seconds`、`wallet_deposits_held`、`wallet_deposits_detected_total{status}`、`wallet_deposits_orphaned_total`（标签 `network`）。
- 告警（`deploy/observability/alerts.yml`）：`WalletScanLagging`（落后 50 块以上持续 10 分钟）、`WalletScanStalled`（10 分钟没有完成一轮：节点、租约或数据库）、`WalletDepositsHeld`（有已确认充值因资产关闭充值挂起超过 1 小时）。

## 常用操作

```bash
# 扫描进度与待处理充值（MCP pg_query 或服务器 psql，schema wallet）
SELECT network, block, updated_at FROM wallet.scan_cursors;
SELECT id, user_id, asset, amount, status, confirmations, reason, tx_hash FROM wallet.deposits
 WHERE status NOT IN ('CREDITED') ORDER BY id DESC LIMIT 20;
# 未入账资金（账本侧）
SELECT asset, available FROM ledger.accounts WHERE account_type = 'UNCLAIMED_DEPOSIT';
```

- 重扫一段区块（例如漏扫怀疑）：停 wallet-service，`UPDATE wallet.scan_cursors SET block = <起点-1>` 并删掉 `wallet.scanned_blocks` 中更高的块，再启动。已记录的充值不会重复（唯一键），ORPHANED 的会复活。
- 未入账资金的处置（找回或并入平台）要等管理后台（任务 11）的审批流程；在此之前只记录，不手工调账。
- 切换节点：改 apps.env 的 URL 后重启 wallet-service；扫描从游标继续。

## 端到端

`scripts/e2e/deposit.sh`（`task e2e` 的一部分，约 5 分钟）：新用户取地址 → 端到端发送方转 0.0012 ETH 与 0.0002 ETH → 前者 12 个确认后 CREDITED、余额与流水可见、有到账通知，后者 `BELOW_MINIMUM` 记为 REJECTED → WebSocket `deposits`/`balances` 都有推送。

- 发送方地址 `0xF94cC1F88410AA374bc1d7A288D09F6Af366a965`，私钥在本地 `.env` 的 `E2E_SEPOLIA_SENDER_KEY`（只有测试币）。查余额：`go run ./scripts/e2e/sendeth -balance`；低于 0.003 ETH 时脚本跳过链上部分，只测地址分配。补充测试币：Google Cloud Web3 或 Alchemy 的 Sepolia 水龙头转到上面的地址。
- 每次消耗约 0.0014 ETH 加 gas；这些币留在各测试用户的充值地址里，任务 10 的归集可以把它们收回热钱包。

## 已知局限

- 只扫一个 EVM 网络；合约内部转账（internal transaction）的原生币到账看不到，需要人工补录（任务 11）。
- ERC-20 需要在 instrument 配置合约地址才会入账（decimals 从合约读取并缓存）；未配置的代币只记录不入账。代币提现、代币归集与 gas 补给尚未实现（签名服务不签带 calldata 的交易），测试服没有配置代币。
- 充值风控评分（§11.5 的"风控拦截"）尚未接入，账户状态由 eligibility 把关。
- 提现审批暂用 `exchangectl`（审核人名字由命令行给出），RBAC 与双人审批界面在管理后台（任务 11）。
- 测试环境用户的模拟资金（`ADJUSTMENT`）也能提现到链上，热钱包里的真实测试币会因此减少；不变量 4 的核对不受影响（预期与持有同步减少）。
