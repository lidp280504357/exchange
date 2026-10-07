# 架构决策记录（ADR）

每个文件记录一个已接受的架构决策：背景、决策、理由、后果、备选。决策来源是 [文档评审与待决策-2026-09-27.md](../../文档评审与待决策-2026-09-27.md) §8 的用户确认结论；修改决策时新增一条 ADR 并在旧 ADR 标注"被 ADR-xxxx 取代"，不直接改写旧文。

| 编号 | 标题 | 状态 |
|---|---|---|
| [0001](0001-ledger-double-entry.md) | 账本采用不可变双重记账，作为余额唯一事实来源 | 已接受 |
| [0002](0002-matching-engine-boundary.md) | 撮合引擎按交易对分片，只处理命令与事件，不持有余额 | 已接受 |
| [0003](0003-signing-isolation.md) | 签名服务与业务服务隔离，私钥不进入业务进程 | 已接受 |
| [0004](0004-market-data-boundary.md) | 外部行情只作参考与指数输入，上线前必须取得授权 | 已接受（第 1 条被 0010 取代） |
| [0005](0005-feature-flags.md) | 高风险能力由功能开关控制，默认关闭 | 已接受 |
| [0006](0006-repo-layout.md) | 单仓库单 module，服务间通过 lint 强制隔离 | 已接受 |
| [0007](0007-deployment.md) | 本机只调试，测试服拉取源码构建镜像部署 | 已接受 |
| [0008](0008-money-and-ids.md) | 金额用十进制定点数与字符串传输，ID 用 UUIDv7 与 int64 序号 | 已接受 |
| [0009](0009-auth-model.md) | 密码 + 验证码注册登录，7 天无登录触发二次验证 | 已接受 |
| [0010](0010-binance-market-display.md) | 行情展示全部使用币安数据且不标注来源 | 已接受 |
| [0011](0011-custody-wallet.md) | 充提接入第三方托管钱包（优盾），替代自建链适配 | 已接受 |
| [0012](0012-frontend-architecture.md) | 前端采用三应用两包、共享设计系统与单一 WebSocket 数据层 | 已接受 |
| [0013](0013-house-assets-and-inventory.md) | 站内资产与 HOUSE 库存 | 已接受 |
| [0014](0014-price-multiplier-and-reference-mapping.md) | 低价币 1000 倍计价与参考符号映射 | 已接受 |
| [0015](0015-house-liquidity.md) | 合并簿与虚拟参考流动性（B-book） | 已接受 |
| [0016](0016-platform-coin-simulated-market.md) | 平台币 ASTRA 与模拟市场（机器人池等同平台、运营控价与守卫、仅限学习项目） | 已接受 |
| [0017](0017-stand-in-custodian-and-hidden-test-asset.md) | 替身托管方 UDUNMOCK 与隐藏测试资产 TUSD（端到端在真网关切换后照常覆盖托管路径） | 已接受 |
| [0018](0018-margin-debt-rows.md) | 杠杆负债记成用户名下的负数行（三行记法） | 已接受 |
| [0019](0019-mark-price-from-binance.md) | 合约的标记价、指数价与资金费率跟随币安，自算价格作后备 | 已接受 |
| [0020](0020-inverse-contracts-ledger-and-precision.md) | 反向（币本位）合约的账本与精度：按笔舍入一次、按结算资产分账户与对账、HOUSE 的币不为负 | 已接受 |
| [0021](0021-price-overlay.md) | 价格叠加：任意币种的价格事件以乘数平移参考行情并自动回归，默认连带合约与杠杆，用户侧不标注（与 ADR-0010 的关系） | 已接受 |

领域词汇表见需求文档附录 A，状态机见附录 B。
