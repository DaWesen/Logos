# ==================== 阶段1: 构建 ====================
FROM docker.1ms.run/library/golang:1.25.1-alpine AS builder

RUN apk --no-cache add ca-certificates tzdata git

ENV GOPROXY=https://goproxy.io,https://proxy.golang.org,direct
ENV GO111MODULE=on
ENV GOMODCACHE=/go/pkg/mod

WORKDIR /app

# 先在宿主机运行 go mod download，然后通过 vendor 传递依赖进 Docker
# 这样 Docker 内完全不需要网络下载 Go 依赖
COPY go.mod go.sum ./
COPY vendor/ vendor/

COPY . .

ARG BUILD_TAGS=""
ARG LDFLAGS=""

# ==================== Platform 领域 ====================
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o gateway ./cmd/platform/gateway
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o user ./cmd/platform/user
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o monitoring ./cmd/platform/monitoring
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o billing ./cmd/platform/billing
# ==================== Messaging 领域 ====================
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o im ./cmd/messaging/im
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o chat ./cmd/messaging/chat
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o contact ./cmd/messaging/contact
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o message ./cmd/messaging/message
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o qqbridge ./cmd/messaging/qqbridge
# ==================== AI 领域 ====================
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o knowledge ./cmd/ai/knowledge
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o search ./cmd/ai/search
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o vector ./cmd/ai/vector
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o question ./cmd/ai/question
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o recommend ./cmd/ai/recommend
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o extraction ./cmd/ai/extraction
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o collection ./cmd/ai/collection
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o bot ./cmd/ai/bot
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o process ./cmd/ai/process
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o summary ./cmd/ai/summary
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o mcp ./cmd/ai/mcp
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o moderation ./cmd/ai/moderation

# ==================== 阶段2: 运行 ====================
FROM docker.1ms.run/library/alpine:3.20

LABEL maintainer="Logos Team"
LABEL description="Logos - AI-Powered Instant Messaging Platform"
LABEL version="2.0.0"

RUN apk --no-cache add ca-certificates tzdata curl python3 nodejs ffmpeg

WORKDIR /app

# 创建日志目录
RUN mkdir -p logs && chmod 755 logs

# Platform
COPY --from=builder /app/gateway .
COPY --from=builder /app/user .
COPY --from=builder /app/monitoring .
COPY --from=builder /app/billing .
# Messaging
COPY --from=builder /app/im .
COPY --from=builder /app/chat .
COPY --from=builder /app/contact .
COPY --from=builder /app/message .
COPY --from=builder /app/qqbridge .
# AI
COPY --from=builder /app/knowledge .
COPY --from=builder /app/search .
COPY --from=builder /app/vector .
COPY --from=builder /app/question .
COPY --from=builder /app/recommend .
COPY --from=builder /app/extraction .
COPY --from=builder /app/collection .
COPY --from=builder /app/bot .
COPY --from=builder /app/process .
COPY --from=builder /app/summary .
COPY --from=builder /app/mcp .
COPY --from=builder /app/moderation .
COPY --from=builder /app/config ./config

EXPOSE 8888 9001 9002 9003 9004 9005 9006 9007 9008 9009 9010 9011 9012 9013 9016 9017 9018 9019 9020 9021

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8888/health || exit 1

CMD ["./gateway"]
