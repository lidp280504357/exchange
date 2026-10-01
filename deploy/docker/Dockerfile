# syntax=docker/dockerfile:1.7
# 一次编译 cmd/ 下的全部服务，打进同一个镜像；每个容器用 command 选择要运行的二进制。
# 构建上下文是仓库根目录（服务器上为 /opt/exchange/src），见 deploy/compose/docker-compose.apps.yml。
# 基础镜像用 AWS ECR Public 的 Docker 官方镜像，避免 Docker Hub 拉取限额。

ARG GO_IMAGE=public.ecr.aws/docker/library/golang:1.27-alpine
ARG RUNTIME_IMAGE=public.ecr.aws/docker/library/alpine:3.22

FROM ${GO_IMAGE} AS build
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -buildvcs=false \
      -ldflags "-s -w -X github.com/lidp280504357/exchange/internal/platform/app.buildVersion=${VERSION}" \
      -o /out/ ./cmd/...

FROM ${RUNTIME_IMAGE}
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 app
COPY --from=build /out/ /app/
USER app
WORKDIR /app
