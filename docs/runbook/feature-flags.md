# 功能开关运维（ADR-0005）

高风险能力默认关闭，由 PostgreSQL `config.flags` 表控制；服务每 5 秒刷新本地副本，缺失的开关按关闭处理。阶段 2 管理后台上线前，用命令行工具 `exchangectl` 修改。每次修改在同一事务里写 `config.flag_changes` 历史，并经 `config.outbox` 发布 `audit.ConfigChanged` 到 `audit.events`（进入 ClickHouse `audit_logs`）。

## 已知开关

| 键 | 作用 | 阶段 |
|---|---|---|
| `auth.sms` | 手机号注册/登录验证码通道（高风险地区码保持仅邮箱） | 1 |
| `account.transfer` | 现货 ↔ 合约账户划转 | 1 |
| `ledger.manual_adjustment` | 运营给测试账户记入模拟余额（`MANUAL_ADJUSTMENT`） | 1 |
| `ledger.welcome_credit` | 新注册用户自动获得模拟演示资金，仅测试环境 | 1 |
| `wallet.withdraw` | 提现 | 2 |
| `derivatives.trading` | 永续合约交易 | 3 |
| `market.reference_kline` | 无成交的新交易对临时展示参考 K 线 | 2 |

## 规则维度

`set` 只修改给出的选项，其余保持不变：`--on/--off` 是总开关；`--allow-<维度>` 与 `--deny-<维度>` 取逗号分隔的值，传空值清除该维度。维度为 `regions`（ISO 国家码）、`statuses`（账户状态）、`assets`、`symbols`、`users`（用户 ID 白名单）。判定：总开关打开且每个有约束的维度都放行才算开；调用方没给出某个有约束维度的值时按关闭处理（失败即关闭）。deny 优先于 allow。

紧急开关（需求 §5.12）用 deny 表达：全站暂停某能力 `--off`；单资产 `--deny-assets USDT`；单交易对 `--deny-symbols BTC-USDT`。

## 常用命令

本机（读仓库根目录 `.env`，直连测试服数据库）：

```bash
go run ./cmd/exchangectl flags list
go run ./cmd/exchangectl flags set account.transfer --on --reason "开放划转联调"
go run ./cmd/exchangectl flags set account.transfer --deny-regions KP,IR --reason "地区限制演示"
go run ./cmd/exchangectl flags history account.transfer
```

测试服（任一应用容器里都有 `/app/exchangectl`，环境变量来自 `apps.env`）：

```bash
ssh exchange 'sudo docker exec exchange-infra-user-service-1 /app/exchangectl flags list'
```

必须带 `--reason`，操作者记为 `cli:<系统用户名>`。Kafka 暂时不可用时修改照常生效，审计事件留在 `config.outbox`，下次运行 `exchangectl` 时补发。
