#!/bin/bash

RED='\033[31m'
GREEN='\033[32m'
YELLOW='\033[33m'
CYAN='\033[36m'
BOLD='\033[1m'
RESET='\033[0m'

PASS=0
FAIL=0
TOTAL=0

check_pass() {
    TOTAL=$((TOTAL + 1))
    PASS=$((PASS + 1))
    echo -e "  ${GREEN}✓ PASS${RESET}  $1"
}

check_fail() {
    TOTAL=$((TOTAL + 1))
    FAIL=$((FAIL + 1))
    echo -e "  ${RED}✗ FAIL${RESET}  $1"
    if [ -n "$2" ]; then
        echo "$2" | head -10 | sed 's/^/        /'
    fi
}

echo ""
echo -e "${YELLOW}${BOLD}========================================${RESET}"
echo -e "${YELLOW}${BOLD}  Logos 项目一键测试${RESET}"
echo -e "${YELLOW}${BOLD}========================================${RESET}"
echo ""

cd "$(dirname "$0")/.." || exit 1

echo -e "${CYAN}${BOLD}[1/4] Go Vet 静态检查${RESET}"
echo -e "${CYAN}─────────────────────────────────────${RESET}"
VET_OUTPUT=$(go vet ./... 2>&1)
if [ $? -eq 0 ]; then
    check_pass "go vet ./..."
else
    check_fail "go vet ./..." "$VET_OUTPUT"
fi
echo ""

echo -e "${CYAN}${BOLD}[2/4] 编译检查 (go build ./cmd/...)${RESET}"
echo -e "${CYAN}─────────────────────────────────────${RESET}"
BUILD_OUTPUT=$(go build ./cmd/... 2>&1)
if [ $? -eq 0 ]; then
    check_pass "go build ./cmd/..."
else
    check_fail "go build ./cmd/..." "$BUILD_OUTPUT"
fi
echo ""

echo -e "${CYAN}${BOLD}[3/4] 逐服务编译检查${RESET}"
echo -e "${CYAN}─────────────────────────────────────${RESET}"

TMPDIR_BUILD=$(mktemp -d)
trap "rm -rf $TMPDIR_BUILD" EXIT

SERVICES=(
    "Gateway:./cmd/platform/gateway/"
    "User:./cmd/platform/user/"
    "Billing:./cmd/platform/billing/"
    "Monitoring:./cmd/platform/monitoring/"
    "Chat:./cmd/messaging/chat/"
    "IM:./cmd/messaging/im/"
    "Contact:./cmd/messaging/contact/"
    "Message:./cmd/messaging/message/"
    "Bot:./cmd/ai/bot/"
    "Knowledge:./cmd/ai/knowledge/"
    "Search:./cmd/ai/search/"
    "Vector:./cmd/ai/vector/"
    "Summary:./cmd/ai/summary/"
    "MCP:./cmd/ai/mcp/"
    "Moderation:./cmd/ai/moderation/"
    "Question:./cmd/ai/question/"
    "Recommend:./cmd/ai/recommend/"
    "Extraction:./cmd/ai/extraction/"
    "Collection:./cmd/ai/collection/"
    "Process:./cmd/ai/process/"
)

for entry in "${SERVICES[@]}"; do
    NAME="${entry%%:*}"
    PKG="${entry#*:}"
    ERR_OUTPUT=$(go build -o "$TMPDIR_BUILD/$NAME" "$PKG" 2>&1)
    if [ $? -eq 0 ]; then
        check_pass "$NAME"
    else
        check_fail "$NAME" "$ERR_OUTPUT"
    fi
done
echo ""

echo -e "${CYAN}${BOLD}[4/4] gRPC 健康检查注册验证${RESET}"
echo -e "${CYAN}─────────────────────────────────────${RESET}"
HEALTH_CHECK=$(grep -r "grpc_health_v1" pkg/grpcserver/ 2>/dev/null)
if [ -n "$HEALTH_CHECK" ]; then
    check_pass "gRPC 健康检查已注册到 StartServer"
else
    check_fail "gRPC 健康检查未找到"
fi
echo ""

echo -e "${YELLOW}${BOLD}========================================${RESET}"
echo -e "${YELLOW}${BOLD}  汇总${RESET}"
echo -e "${YELLOW}${BOLD}========================================${RESET}"
echo ""
echo -e "  总计: ${BOLD}$TOTAL${RESET}  通过: ${GREEN}${BOLD}$PASS${RESET}  失败: ${RED}${BOLD}$FAIL${RESET}"
echo ""

if [ $FAIL -eq 0 ]; then
    echo -e "  ${GREEN}${BOLD}全部通过 ✓${RESET}"
else
    echo -e "  ${RED}${BOLD}存在失败项 ✗${RESET}"
    exit 1
fi
echo ""
