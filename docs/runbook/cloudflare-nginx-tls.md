# 域名、Cloudflare 代理与 nginx TLS（已于 2026-09-28 部署）

域名 `astras.vip` 已在 Cloudflare 托管并开启代理（橙色云），A 记录指向测试服 `3.107.113.199`。用户可见的 TLS 由 Cloudflare 边缘提供，Cloudflare 到测试服之间用 **Cloudflare 源站证书**（免费、15 年有效、不用申请 Let's Encrypt、不需要 80 端口验证）。

## 一、Cloudflare 侧设置

1. **SSL/TLS → Overview**：加密模式选 **Full (strict)**。不要用 Flexible，否则源站是明文。
2. **SSL/TLS → Origin Server → Create Certificate**：私钥类型 RSA 2048，Hostnames 填 `astras.vip` 和 `*.astras.vip`，有效期 15 年。页面会显示证书和私钥两段 PEM，**私钥只显示一次**，分别保存为 `origin.pem` 和 `origin.key`。
3. **SSL/TLS → Edge Certificates**：打开 **Always Use HTTPS**，Minimum TLS Version 选 1.2。
4. **Network**：确认 **WebSockets** 是 On（默认 On）。
5. **Caching → Cache Rules**：新建规则，路径以 `/v1/` 开头的请求 **Bypass cache**。
6. **DNS**：`astras.vip`（A，代理开）；以后管理后台加 `admin.astras.vip`（A，代理开），并用 Cloudflare Access 或 nginx `allow` 限制来源 IP。

- **IPv6 兼容（用户 2026-10-10 决定：登录记录优先显示 IPv4）**：客户端 IP 取自 `CF-Connecting-IP`，即客户端连到 Cloudflare 时用的地址；双栈客户端默认走 IPv6，记录里就是 IPv6。服务端无法"同一连接改取 IPv4"，只能让客户端用 IPv4 连：在 Cloudflare 把该域名的 **IPv6 Compatibility 关掉**（DNS 只答 A 记录）。免费套餐的控制台开关是只读的，用 API 改：`curl -X PATCH https://api.cloudflare.com/client/v4/zones/<ZONE_ID>/settings/ipv6 -H "Authorization: Bearer <TOKEN>" -H "Content-Type: application/json" --data '{"value":"off"}'`（令牌只在本机 `.env`，不进仓库）。关掉后双栈客户端走 IPv4，纯 IPv6 客户端经运营商 NAT64 也会以 IPv4 到达；`cloudflare-real-ip.conf` 仍保留 ips-v6 名单。**用户 2026-10-10 18:4x 最终决定：不关闭，保持现状**——登录记录里双栈客户端显示 IPv6 即可，以上做法只作备忘，正式环境也不关（launch.md 步 1 同）。

## 二、把证书放到测试服

```bash
ssh exchange 'sudo mkdir -p /opt/exchange/infra/nginx/ssl && sudo chmod 700 /opt/exchange/infra/nginx/ssl'
COPYFILE_DISABLE=1 scp origin.pem origin.key exchange:/tmp/
ssh exchange 'sudo mv /tmp/origin.pem /tmp/origin.key /opt/exchange/infra/nginx/ssl/ && sudo chmod 600 /opt/exchange/infra/nginx/ssl/origin.key'
```

## 三、nginx（以 compose 容器运行，与 Go 服务同一网络）

实际文件在 `deploy/compose/nginx/`（compose 服务 `nginx`），下面是要点摘录；上游 `api-gateway:8080` 通过 docker 内置 DNS 按请求解析，网关未部署时返回 502。

```nginx
# deploy/compose/nginx/conf.d/astras.vip.conf
# 只信任 Cloudflare 的来源 IP，并从 CF-Connecting-IP 取真实客户端 IP（限流、风控、Turnstile remoteip 都依赖它）
include /etc/nginx/cloudflare-real-ip.conf;   # 由脚本从 https://www.cloudflare.com/ips-v4 与 ips-v6 生成 set_real_ip_from 列表
real_ip_header CF-Connecting-IP;

map $http_upgrade $connection_upgrade { default upgrade; '' close; }

server {
    listen 443 ssl;
    http2 on;
    server_name astras.vip;
    ssl_certificate     /etc/nginx/ssl/origin.pem;
    ssl_certificate_key /etc/nginx/ssl/origin.key;

    location /v1/ws {
        proxy_pass http://api-gateway:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_read_timeout 300s;
    }
    location /v1/ {
        proxy_pass http://api-gateway:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
    }
    location / {
        root /usr/share/nginx/html;        # H5 构建产物
        try_files $uri /index.html;
    }
}

server { listen 80; server_name astras.vip; return 301 https://$host$request_uri; }
```

OpenResty 也可以，配置完全兼容；除非要写 Lua 逻辑，否则用官方 `nginx:alpine` 镜像更省事。

## 四、安全组

80 与 443 对 `0.0.0.0/0` 放行（已完成）。数据库、Redis、Redpanda、ClickHouse 端口继续只放行本机开发 IP。如果想更严格，443 只放行 Cloudflare 的 IP 段。

## 五、本地开发

本地 `vite dev` 和本地 Go 服务走 `http://localhost`，不经过 Cloudflare；Turnstile 的 hostname 允许列表已包含 `localhost`。refresh token cookie 在本地是非 Secure 的开发模式，测试服上为 `Secure`。
