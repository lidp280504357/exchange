# Grafana Cloud 接入（免费层）

方案：Go 服务通过 OpenTelemetry SDK 把指标、日志、链路直接以 OTLP 协议发到 Grafana Cloud，测试服上不自建 Prometheus/Loki。以后需要采集 docker 容器日志和主机指标时，再在服务器上装 Grafana Alloy。

## 一、创建 Stack（只做一次）

1. 打开 https://grafana.com 用注册的账号登录，进入 **My Account**（Cloud Portal）。
2. 页面左侧是你的组织（Org）。如果 **Overview** 里没有任何 Stack，点 **Add stack**（或 **Create stack**）：
   - **Stack name**：填 `exchangedev`，它同时决定访问地址 `https://exchangedev.grafana.net`。
   - **Region**：选离测试服最近的，测试服在悉尼，有 `AU`/`Asia Pacific` 就选它，没有就选 `US`。
   - 免费层（Free）直接创建，几十秒后 Stack 状态变为 Active。
3. 免费层限制：指标 10k 活跃序列、日志 50 GB/月、链路 50 GB/月、保留 14 天，够开发用。

## 二、拿到 OTLP 连接参数

1. 在 Cloud Portal 的 Stack 页面找到 **OpenTelemetry** 磁贴，点 **Configure**。
2. 页面会让你生成一个 token：点 **Generate now**（或 Create token），名字填 `exchange-dev`，权限保持默认的写入即可。**token 只显示一次**，立即复制。
3. 页面随后生成三行环境变量，原样复制到本地 `.env`（已被 git 忽略）：

```text
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp-gateway-<region>.grafana.net/otlp
OTEL_EXPORTER_OTLP_HEADERS=Authorization=Basic <一长串 base64>
```

`base64` 里是 `实例ID:token`，不要拆开也不要改。如果以后要用 Alloy，同一个 token 也能用。

## 三、Go 服务怎么用

阶段 1 任务 3（日志/trace/指标基础库）接入 OpenTelemetry SDK 时，只读这三个变量再加两个：

```text
OTEL_SERVICE_NAME=auth-service          # 每个服务不同
OTEL_RESOURCE_ATTRIBUTES=deployment.environment=test,service.namespace=exchange
```

SDK 用 `otlptracehttp`、`otlpmetrichttp`、`otlploghttp` 三个 exporter，它们会自动读取上述环境变量，代码里不写地址和 token。本地运行时 `OTEL_SERVICE_NAME` 用 `exchange-local`，`deployment.environment=local`，这样在面板里能和测试服区分。

## 四、验证数据到了

1. Cloud Portal 的 Stack 页面点 **Launch** 打开 Grafana。
2. 左侧 **Explore**，数据源下拉选 `grafanacloud-exchangedev-traces`，Search 里 Service Name 应能看到你的服务名；选 `grafanacloud-exchangedev-prom` 查 `up` 或自定义指标；选 `grafanacloud-exchangedev-logs` 用 `{service_name="auth-service"}` 查日志。
3. 数据一般 30 秒内出现。看不到时先检查 `.env` 三个变量是否原样粘贴、服务启动日志里有没有 `401`（token 错）或 `404`（endpoint 少了 `/otlp`）。

## 五、以后：Alloy 采集容器日志与主机指标

Grafana 里 **Connections → Add new connection**，搜 **Docker** 或 **Linux Server**，按向导会给出 Alloy 的安装命令和配置；在测试服以 compose 容器方式运行 `grafana/alloy`，把 `/var/run/docker.sock` 和 `/var/lib/docker/containers` 只读挂进去即可。阶段 1 结束前不做。
