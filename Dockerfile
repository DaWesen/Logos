# ==================== 阶段1: 构建 ====================
FROM docker.1ms.run/library/golang:1.25.1-alpine AS builder

RUN apk --no-cache add ca-certificates tzdata git

ENV GOPROXY=https://goproxy.cn,direct
ENV GO111MODULE=on

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG BUILD_TAGS=""
ARG LDFLAGS=""

# ==================== Platform 领域 ====================
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o gateway ./cmd/platform/gateway
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o user ./cmd/platform/user
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o monitoring ./cmd/platform/monitoring
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o billing ./cmd/platform/billing
# ==================== Messaging 领域 ====================
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o im ./cmd/messaging/im
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o chat ./cmd/messaging/chat
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o contact ./cmd/messaging/contact
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o message ./cmd/messaging/message
# ==================== AI 领域 ====================
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o knowledge ./cmd/ai/knowledge
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o search ./cmd/ai/search
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o vector ./cmd/ai/vector
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o question ./cmd/ai/question
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o recommend ./cmd/ai/recommend
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o extraction ./cmd/ai/extraction
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o collection ./cmd/ai/collection
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o bot ./cmd/ai/bot
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o process ./cmd/ai/process
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o summary ./cmd/ai/summary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o mcp ./cmd/ai/mcp
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w ${LDFLAGS}" -tags "${BUILD_TAGS}" -o moderation ./cmd/ai/moderation

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
