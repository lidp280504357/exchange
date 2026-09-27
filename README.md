# exchange

Go 微服务虚拟资产交易所（学习项目）。

- 新会话先读：[CLAUDE.md](CLAUDE.md)、[docs/交接总结-2026-09-29.md](docs/交接总结-2026-09-29.md)
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
task ci            # 本地跑与 CI 相同的检查（格式、vet、lint、脚本、测试）
task run -- api-gateway  # 本机运行一个服务（读取根目录 .env，Ctrl-C 优雅退出）
task build         # 编译全部服务到 bin/
task deploy        # 测试服拉取最新代码并更新（task deploy -- <commit> 回滚）
task deploy:status # 测试服容器状态
```

本机只需 Go 1.26+、Node 24+ 和 `~/go/bin` 里的工具（gofumpt、golangci-lint、task 等），不需要 Docker；部署流程见 [docs/runbook/server-deploy.md](docs/runbook/server-deploy.md)。
