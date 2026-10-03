# ADR-0017：替身托管方与隐藏测试资产

- 状态：已接受（2026-10-04，协调会话代用户批准方案 A）
- 关联：[ADR-0011](0011-custody-wallet.md)（托管钱包）、[ADR-0005](0005-feature-flags.md)（功能开关）、[ADR-0013](0013-house-assets-and-inventory.md)（可充提资产）；运维见 [custody.md](../runbook/custody.md)（「真网关联调」「方案 A」）、[instruments.md](../runbook/instruments.md)、[feature-flags.md](../runbook/feature-flags.md)

## 背景

托管充提的端到端测试（`custody.sh`、故障演练 `custody-callbacks.sh`）靠测试服的模拟网关 udun-mock：它能造充值、按需失败、丢应答、把手续费报错单位，覆盖托管方各种不守规矩的路径。优盾给的是生产网关上的测试商户，没有沙箱；`UDUN` 一旦接上真网关，这些测试要么动真钱，要么只能跳过，端到端就丢了托管路径的覆盖。用户决定真网关的切换与主网小额测试放到全部开发完成之后，并把这里的方案提前做，切换时不丢覆盖。

## 决策

1. **第二个托管方 `UDUNMOCK`**：wallet-service 同时接多个托管方，每个有自己的设置（`UDUN_*`、`UDUNMOCK_*`）、处理器、租约、指标标签、回调来源名单与币种缓存；对账按资产覆盖全部持有方（其他托管方也算"别处持有"）。`UDUNMOCK` 永远指向模拟网关，切换只改 `UDUN_*`。模拟网关只读 `UDUNMOCK_MERCHANT_ID`、`UDUNMOCK_API_KEY`，从不读 `UDUN_*`：否则切换后它拿真商户的密钥签的回调会被当成真网关的。`UDUNMOCK` 的回调只在容器网络里经 api-gateway 进来，nginx 对公网的 `/v1/wallet/callbacks/udunmock` 一律 403，wallet-service 再按它的来源名单（容器网段）把关。
2. **隐藏资产**：资产有 `hidden` 标记。隐藏资产不进任何公开列表（`/v1/market/assets` 与图标；它没有交易对与合约，行情、ticker、走势图、自选自然没有它），`instruments apply` 拒绝给它建交易对或合约，拒绝把有交易对的资产设为隐藏，也拒绝把 `UDUNMOCK` 的网络配给不隐藏的资产（或把这样的资产取消隐藏）。内部 gRPC 照常列出它（钱包、账本、后台要用）。
3. **只给端到端账户**：隐藏资产的网络、充值地址、地址校验、地址簿与提现只对具备 `TEST_ASSETS` 资格的用户开放，其他人一律当作没有这个网络（404 `WALLET_NETWORK_UNKNOWN`）。user-service 把 `TEST_ASSETS` 映射到开关 `wallet.test_assets`，默认关；测试服只对地区 `AQ` 打开（沿用 `risk.enforce` 的先例），端到端脚本用 `AQ` 注册用户。
4. **TUSD**：唯一的隐藏资产，6 位小数，一个网络 `TRON-TEST`（TRON 地址格式，provider `UDUNMOCK`，coin `195:TQQCuyVcUEknTGyfSRKhcUuLZfEe93qWpy`，一个固定的、格式合法的假合约），最小充值 1、最小提现 10、手续费 5。它没有行情，提现限额按 `WALLET_FALLBACK_PRICES` 的 `TUSD:1` 折算。托管方对它收的手续费照常从 `GAS_SUPPLY` 出，`GAS_SUPPLY` 由它自己的手续费收入补（`custody.sh` 在第一笔提现结算后挪）。
5. **端到端改在 TUSD 上跑**：`custody.sh` 与 `custody-callbacks.sh` 只用 `UDUNMOCK` 与 TUSD，不碰 `UDUN`；浏览器冒烟测试只取 Sepolia 地址（本地派生），后台冒烟读 `UDUNMOCK` 的托管方页。只读的检查（`UDUN` 的网络配置、伪造的公网回调被拒）照旧对 `UDUN` 做。

## 理由

- 同一套适配器与处理逻辑既跑真网关又跑替身，端到端测的就是生产代码；替身只是换了网关与商户。
- 把测试资产藏在现有的资产模型里（一个字段、一个资格），比单独做一套"测试钱包"简单，也不会出现在用户看得到的任何地方。
- 用地区 `AQ` 圈定端到端账户不需要新的身份概念，与风控开关的做法一致；开关关着就谁都看不见。

## 后果

- instrument 迁移 00007（`assets.hidden`、`networks.provider` 允许 `UDUNMOCK`），协议 `Asset.hidden`；钱包内部接口 `/internal/wallet/custody` 与回调列表按 `?provider=` 分开，后台托管方页加托管方切换。
- 服务器 `apps.env` 有 `UDUNMOCK_MERCHANT_ID`、`UDUNMOCK_API_KEY`（与模拟网关现在的商户相同）；`UDUNMOCK_GATEWAY_URL` 等写在 compose 里。
- 后台的报表与对账会出现 TUSD 与端到端账户的余额（只在测试服）。
- 上线到真实环境时 `UDUNMOCK` 不配置（没有它的设置就不启用），`wallet.test_assets` 保持关闭，TUSD 不进生产的参考数据。

## 备选

- **切换后跳过 `custody.sh`**：托管路径从此没有端到端覆盖，回归只能靠单元测试，否决。
- **测试资产照常公开、只靠"不在交易对里"**：用户在充值页能看到并往一个假代币地址打钱，否决。
- **按用户 ID 白名单圈定端到端账户**：每次运行注册的新用户都要先改开关，否决。
