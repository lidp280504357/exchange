# exchange

Go 微服务虚拟资产交易所（学习项目）。

- 新会话先读：[CLAUDE.md](CLAUDE.md)、[docs/交接总结-2026-09-29.md](docs/交接总结-2026-09-29.md)
- 2026-09-30 新需求设计（体验重构 PC/手机两站、主流 50 币、多链充提，交给新会话执行）：[docs/设计-体验重构与市场钱包扩展-2026-09-30.md](docs/设计-体验重构与市场钱包扩展-2026-09-30.md)
- 需求：[需求文档-v0.2.md](需求文档-v0.2.md)
- 实施计划：[实施计划.md](实施计划.md)
- 准备工作清单：[准备工作清单.md](准备工作清单.md)
- 架构决策：[docs/adr](docs/adr/README.md)；运行手册：[docs/runbook](docs/runbook/)
- 评审与决策记录：[文档评审与待决策-2026-09-27.md](文档评审与待决策-2026-09-27.md)
- 测试环境编排：[deploy/compose](deploy/compose/README.md)
- Claude Code 测试服工具：[tools/devmcp](tools/devmcp/README.md)

`环境配置.md` 含测试环境凭据，已被 `.gitignore` 忽略，不入库。

## 常用命令

```bash
task --list        # 全部任务
task ci            # 本地跑与 CI 相同的检查（格式、go.mod、vet、lint、脚本、测试）
task test:integration  # 连同集成测试（.env 的 TEST_*，测试服 exchange_test 库）
task run -- api-gateway  # 本机运行一个服务（读取根目录 .env，Ctrl-C 优雅退出）
task build         # 编译全部服务到 bin/
task web:dev       # 本机启动 PC 站（web/apps/pc，http://localhost:5173；-- m 为手机站；API 代理到测试服）
task deploy        # 测试服拉取最新代码并更新（task deploy -- <commit> 回滚）
task deploy:status # 测试服容器状态
task e2e           # 对测试环境跑端到端检查（scripts/e2e，含无头 Chrome 的 PC 站与手机站冒烟测试）
```

本机只需 Go 1.27+、Node 24+（pnpm 11）和 `~/go/bin` 里的工具（gofumpt、golangci-lint、task 等），不需要 Docker；部署流程见 [docs/runbook/server-deploy.md](docs/runbook/server-deploy.md)，前端见 [docs/runbook/web.md](docs/runbook/web.md)。
