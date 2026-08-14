# syntax=docker/dockerfile:1.7

# ─── Build stage ─────────────────────────────────────────────────────────────
# --platform=$BUILDPLATFORM: builder 永远跑在构建机的原生架构上,靠 GOARCH 交叉
# 编译出目标架构的二进制。多架构构建时如果让 builder 跟着目标平台走,arm64 那份
# 会在 QEMU 里模拟编译,Go 编译是 CPU 密集型,慢一个数量级。
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /src

# 整个源码先拷进来：`go mod tidy` 要解析 import 才能补全 go.mod/go.sum。
# 依赖若没在本地先 tidy 过，这里会自动补齐（pgx 的传递依赖 + 校验和），
# 所以 CI / Render 构建不用依赖本地提交过 go.sum。
COPY . .

RUN go mod tidy

# TARGETARCH 由 buildx 注入(amd64 / arm64 ...)。经典 builder 不注入,兜底 amd64。
ARG TARGETARCH

# CGO disabled — pgx 驱动是纯 Go，不需要 C 工具链。
# -ldflags strip symbols to shave ~5MB off binary.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags="-s -w" \
    -trimpath \
    -o /out/gemini-web2api .

# ─── Runtime stage ───────────────────────────────────────────────────────────
# distroless/static: ~2MB base, no shell, no package manager — minimal attack surface.
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=builder /out/gemini-web2api /app/gemini-web2api

USER nonroot:nonroot

EXPOSE 8083

# 数据库走 PostgreSQL，连接串由 DATABASE_URL 环境变量注入（Neon/Render 都给这个）。
# 端口走 PORT 环境变量（Render 自动注入），本地跑则回退到默认 8083。
ENTRYPOINT ["/app/gemini-web2api"]
