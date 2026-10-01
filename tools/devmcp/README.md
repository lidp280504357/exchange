# devmcp：测试服务器开发 MCP Server

给 Claude Code 用的本地 MCP Server，让 Claude 能直接操作测试服务器。已在项目根目录的 `.mcp.json` 注册，名字是 `exchange-dev`。

## 前置条件

- 本地 `ssh exchange` 能免密登录测试服务器（`~/.ssh/config` 已配置 `ProxyJump ye`）。
- 本机安装 Go 1.27 以上。
- 第一次在项目里启动 Claude Code 时，会提示是否信任 `.mcp.json` 里的 Server，选择允许。

## 提供的工具

| 工具 | 作用 |
| --- | --- |
| `remote_exec` | 在测试服执行任意 shell 命令，默认工作目录 `/opt/exchange/infra` |
| `remote_put_file` | 把本地文件上传到测试服指定路径，可设置权限 |
| `compose` | 对基础设施执行 `ps` / `logs` / `up` / `restart` / `stop` / `pull` / `config` |
| `pg_query` | 在 PostgreSQL 容器里用 psql 执行 SQL |
| `redis_cmd` | 在 Redis 容器里执行一条 redis-cli 命令 |
| `ch_query` | 在 ClickHouse 容器里执行一条 SQL |

数据库凭据全部读取服务器上的 `/opt/exchange/infra/.env`，本地不存密码，`.mcp.json` 可以放心提交。

## 自检

```bash
go run -C tools/devmcp . --check
```

输出测试服主机名和 docker compose 各服务状态即表示链路正常。

## 可选环境变量

- `DEVMCP_SSH_HOST`：SSH 别名，默认 `exchange`。
- `DEVMCP_INFRA_DIR`：compose 目录，默认 `/opt/exchange/infra`。
