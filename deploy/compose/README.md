# deploy/compose：测试环境基础设施编排

2026-09-27 从测试服务器 `/opt/exchange/infra` 同步而来，是服务器上正在运行的那份 `docker-compose.yml`。以后改动**先改这里，再上传到服务器**，不要直接在服务器上编辑。

## 本地启动

```bash
cp deploy/compose/.env.example deploy/compose/.env   # 填入本地密码；本地可把 PUBLIC_IP 改为 127.0.0.1
docker compose -f deploy/compose/docker-compose.yml --env-file deploy/compose/.env up -d
```

## 上传到测试服务器

```bash
COPYFILE_DISABLE=1 scp deploy/compose/docker-compose.yml exchange:/opt/exchange/infra/docker-compose.yml
ssh exchange 'cd /opt/exchange/infra && sudo docker compose up -d'
```

也可以在 Claude Code 里用 `exchange-dev` MCP 的 `remote_put_file` + `compose up`。

## 待补充（实施计划阶段 0–1）

- 可观测性：Go 服务通过 OTLP 直发 Grafana Cloud（docs/runbook/grafana-cloud.md）；以后需要容器日志再加 Grafana Alloy 容器。
- 反向代理与 TLS：nginx 容器 + Cloudflare 源站证书，域名 astras.vip（docs/runbook/cloudflare-nginx-tls.md）。
- Go 服务的 compose 片段（`docker-compose.apps.yml`），与本文件共用 `exchange` 网络。
- Redpanda 目前是 `dev-container` 单节点、单副本，仅适合测试。
- Topic 初始化：`deploy/redpanda/topics.sh`（幂等，已于 2026-09-28 在服务器执行，45 个 topic）；上传到 `/opt/exchange/infra/redpanda/topics.sh` 后 `bash` 运行即可。

`.env` 已被 `.gitignore` 忽略，只提交 `.env.example`。
