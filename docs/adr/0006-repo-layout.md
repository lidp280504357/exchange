# ADR-0006：单仓库单 module，服务间通过 lint 强制隔离

- 状态：已接受（2026-09-29）
- 关联：需求 §10.1；决策 #1、#2

## 背景

1 人 + AI 开发多个 Go 微服务，多 module 或多仓库会把时间耗在依赖版本同步和跨仓 PR 上；但单 module 又容易让服务之间随手 import，破坏限界上下文。

## 决策

1. 一个仓库、一个 `go.mod`（`github.com/lidp280504357/exchange`），前端与桌面端同仓库。
2. 目录：`cmd/<service>` 入口；`internal/<service>/{domain,application,ports,adapters,transport}`；`internal/platform` 共享基础库；`api/openapi`、`api/proto` 契约；`migrations/<service>`；`deploy/`；`docs/`。
3. `internal/<service>` 之间禁止互相 import，只能依赖 `internal/platform` 与 `api/`；由 golangci-lint 的 depguard 规则强制（首个第二服务出现时启用）。
4. 每个服务独立 PostgreSQL schema，跨服务只走 gRPC/事件，禁止跨 schema 查询。
5. 技术选型按需求 §10.4（chi、pgx、goose、franz-go、clickhouse-go、go-redis、shopspring/decimal、golang-jwt、oapi-codegen、slog、OTel、koanf、testcontainers）。

## 理由

单 module 让重构、测试和 CI 最简单；隔离靠工具而不是靠自觉。

## 后果

- 所有服务同一时刻使用同一依赖版本，升级一次全量验证。
- 仓库会变大；用 `go build ./cmd/<service>` 与 Docker 多阶段构建按服务出镜像。

## 备选

go.work 多 module（工具链繁琐）；前后端分仓（OpenAPI 生成的类型要跨仓同步）。
