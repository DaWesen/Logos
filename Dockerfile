﻿# ==================== 阶段1: 构建 ====================
FROM docker.m.daocloud.io/library/golang:1.25.1-alpine AS builder

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

# ==================== 阶段2: 运行 ====================
# 使用具体版本号，避免 latest 标签问题
FROM docker.m.daocloud.io/library/alpine:3.20

LABEL maintainer="Logos Team"
LABEL description="Logos - AI-Powered Instant Messaging Platform"
LABEL version="2.0.0"

RUN apk --no-cache add ca-certificates tzdata curl

WORKDIR /app

# 创建日志目录
RUN mkdir -p logs && chmod 755 logs

# Platform
COPY --from=builder /app/gateway .
COPY --from=builder /app/user .
COPY --from=builder /app/monitoring .
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
COPY --from=builder /app/config ./config

EXPOSE 8080 9001 9002 9003 9004 9005 9006 9007 9008 9009 9010 9011 9012 9013

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

CMD ["./gateway"]
