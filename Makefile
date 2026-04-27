.PHONY: all build clean run test docker-up docker-down docker-logs

APP_NAME := Logos
VERSION := 2.0.0
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT)"

# ==================== 领域分组 ====================
PLATFORM_SERVICES := gateway user monitoring
MESSAGING_SERVICES := im chat contact message
AI_SERVICES := knowledge search vector question recommend extraction collection

SERVICES := $(PLATFORM_SERVICES) $(MESSAGING_SERVICES) $(AI_SERVICES)

# 服务路径映射
svc-path = $(if $(filter $(1),gateway user monitoring),cmd/platform/$(1),\
           $(if $(filter $(1),im chat contact message),cmd/messaging/$(1),\
           cmd/ai/$(1)))

all: build

# ==================== 构建命令 ====================

build:
	@echo "Building all services..."
	@echo "  [Platform] $(PLATFORM_SERVICES)"
	@for service in $(PLATFORM_SERVICES); do \
		echo "  Building $$service..."; \
		go build $(LDFLAGS) -o bin/$$service ./cmd/platform/$$service; \
	done
	@echo "  [Messaging] $(MESSAGING_SERVICES)"
	@for service in $(MESSAGING_SERVICES); do \
		echo "  Building $$service..."; \
		go build $(LDFLAGS) -o bin/$$service ./cmd/messaging/$$service; \
	done
	@echo "  [AI] $(AI_SERVICES)"
	@for service in $(AI_SERVICES); do \
		echo "  Building $$service..."; \
		go build $(LDFLAGS) -o bin/$$service ./cmd/ai/$$service; \
	done
	@echo "Build completed successfully!"

build-platform:
	@echo "Building Platform services..."
	@for service in $(PLATFORM_SERVICES); do \
		go build $(LDFLAGS) -o bin/$$service ./cmd/platform/$$service; \
	done

build-messaging:
	@echo "Building Messaging services..."
	@for service in $(MESSAGING_SERVICES); do \
		go build $(LDFLAGS) -o bin/$$service ./cmd/messaging/$$service; \
	done

build-ai:
	@echo "Building AI services..."
	@for service in $(AI_SERVICES); do \
		go build $(LDFLAGS) -o bin/$$service ./cmd/ai/$$service; \
	done

build-%:
	@echo "Building $*..."
	@$(eval PATH := $(shell echo "$*" | sed -e 's/gateway\|user\|monitoring/cmd\/platform\/&/' -e 's/im\|chat\|contact\|message/cmd\/messaging\/&/' -e 's/knowledge\|search\|vector\|question\|recommend\|extraction\|collection/cmd\/ai\/&/'))
	@go build $(LDFLAGS) -o bin/$* ./$(PATH)/$*
	@echo "Build $* completed!"

# ==================== 运行命令 ====================

# Platform
run-gateway:
	@echo "Starting Gateway service..."
	./bin/gateway
run-user:
	@echo "Starting User service..."
	./bin/user
run-monitoring:
	@echo "Starting Monitoring service..."
	./bin/monitoring

# Messaging
run-im:
	@echo "Starting IM service..."
	./bin/im
run-chat:
	@echo "Starting Chat service..."
	./bin/chat
run-contact:
	@echo "Starting Contact service..."
	./bin/contact
run-message:
	@echo "Starting Message service..."
	./bin/message

# AI
run-knowledge:
	@echo "Starting Knowledge service..."
	./bin/knowledge
run-search:
	@echo "Starting Search service..."
	./bin/search
run-vector:
	@echo "Starting Vector service..."
	./bin/vector
run-question:
	@echo "Starting Question service..."
	./bin/question
run-recommend:
	@echo "Starting Recommend service..."
	./bin/recommend
run-extraction:
	@echo "Starting Extraction service..."
	./bin/extraction
run-collection:
	@echo "Starting Collection service..."
	./bin/collection

run-all:
	@echo "Starting all services..."
	@for service in $(SERVICES); do \
		echo "Starting $$service..."; \
		./bin/$$service & \
	done
	@echo "All services started!"

# ==================== 测试命令 ====================

test:
	@echo "Running tests..."
	go test -v -cover ./...

test-short:
	@echo "Running short tests..."
	go test -v -short ./...

test-platform:
	@echo "Testing Platform domain..."
	go test -v ./internal/platform/... ./cmd/platform/...

test-messaging:
	@echo "Testing Messaging domain..."
	go test -v ./internal/messaging/... ./cmd/messaging/...

test-ai:
	@echo "Testing AI domain..."
	go test -v ./internal/ai/... ./cmd/ai/...

# ==================== 代码质量 ====================

lint:
	@echo "Running linter..."
	golangci-lint run ./...

fmt:
	@echo "Formatting code..."
	gofmt -l -w .

vet:
	@echo "Vetting code..."
	go vet ./...

clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	@echo "Clean completed!"

# ==================== Docker 命令 ====================

docker-build:
	@echo "Building Docker images..."
	docker-compose build

docker-up:
	@echo "Starting Docker containers..."
	docker-compose up -d

docker-down:
	@echo "Stopping Docker containers..."
	docker-compose down

docker-logs:
	@echo "Showing Docker logs..."
	docker-compose logs -f

docker-ps:
	@echo "Showing Docker containers status..."
	docker-compose ps

docker-restart:
	@echo "Restarting Docker containers..."
	docker-compose restart

# ==================== IDL 生成 ====================

generate-idl:
	@echo "Generating code from Proto files..."
	@echo "  [Common]..."
	@protoc --go_out=. --go_opt=module=Logos --go-grpc_out=. --go-grpc_opt=module=Logos idl/common.proto
	@echo "  [Platform]..."
	@for file in idl/platform/*.proto; do \
		echo "  Processing $$file..."; \
		protoc --go_out=. --go_opt=module=Logos --go-grpc_out=. --go-grpc_opt=module=Logos -I idl $$file; \
	done
	@echo "  [Messaging]..."
	@for file in idl/messaging/*.proto; do \
		echo "  Processing $$file..."; \
		protoc --go_out=. --go_opt=module=Logos --go-grpc_out=. --go-grpc_opt=module=Logos -I idl $$file; \
	done
	@echo "  [AI]..."
	@for file in idl/ai/*.proto; do \
		echo "  Processing $$file..."; \
		protoc --go_out=. --go_opt=module=Logos --go-grpc_out=. --go-grpc_opt=module=Logos -I idl $$file; \
	done
	@echo "IDL generation completed!"

tidy:
	@echo "Tidying dependencies..."
	go mod tidy
	@echo "Tidy completed!"

deps:
	@echo "Downloading dependencies..."
	go mod download
	@echo "Dependencies downloaded!"

# ==================== 信息 ====================

info:
	@echo "Project Information:"
	@echo "  Name:    $(APP_NAME)"
	@echo "  Version: $(VERSION)"
	@echo "  Build:   $(BUILD_TIME)"
	@echo "  Commit:  $(GIT_COMMIT)"
	@echo ""
	@echo "Domain Architecture:"
	@echo "  Platform:  $(PLATFORM_SERVICES)"
	@echo "  Messaging: $(MESSAGING_SERVICES)"
	@echo "  AI:        $(AI_SERVICES)"

help:
	@echo "Available commands:"
	@echo ""
	@echo "Build commands:"
	@echo "  make build              - Build all services (grouped by domain)"
	@echo "  make build-platform     - Build Platform services only"
	@echo "  make build-messaging    - Build Messaging services only"
	@echo "  make build-ai           - Build AI services only"
	@echo "  make build-<service>    - Build specific service"
	@echo ""
	@echo "Run commands:"
	@echo "  make run-<service>      - Run specific service"
	@echo "  make run-all            - Run all services"
	@echo ""
	@echo "Test commands:"
	@echo "  make test               - Run all tests"
	@echo "  make test-platform      - Test Platform domain"
	@echo "  make test-messaging     - Test Messaging domain"
	@echo "  make test-ai            - Test AI domain"
	@echo ""
	@echo "Code quality:"
	@echo "  make lint               - Run linter"
	@echo "  make fmt                - Format code"
	@echo "  make vet                - Vet code"
	@echo ""
	@echo "Docker commands:"
	@echo "  make docker-build       - Build Docker images"
	@echo "  make docker-up          - Start Docker containers"
	@echo "  make docker-down        - Stop Docker containers"
	@echo "  make docker-logs        - Show Docker logs"
	@echo ""
	@echo "Other commands:"
	@echo "  make generate-idl       - Generate code from IDL files (by domain)"
	@echo "  make tidy               - Tidy Go modules"
	@echo "  make deps               - Download dependencies"
	@echo "  make clean              - Clean build artifacts"
	@echo "  make info               - Show project information"
	@echo "  make help               - Show this help message"
