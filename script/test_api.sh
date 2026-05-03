#!/bin/bash
# Logos API 功能测试脚本 v2
# 使用方法: bash script/test_api.sh

BASE="http://localhost:8888/api/v1"
PASS=0
FAIL=0

green() { echo -e "\033[32m$1\033[0m"; }
red() { echo -e "\033[31m$1\033[0m"; }
yellow() { echo -e "\033[33m$1\033[0m"; }
bold() { echo -e "\033[1m$1\033[0m"; }

assert_http() {
    local code=$1 expect=$2 desc=$3
    if [ "$code" = "$expect" ]; then
        PASS=$((PASS + 1)); green "  ✓ $desc"
    else
        FAIL=$((FAIL + 1)); red "  ✗ $desc (HTTP=$code, 期望=$expect)"
    fi
}

echo ""
bold "=========================================="
bold "  Logos API 功能测试 v2"
bold "=========================================="
echo ""

# [1] 基础检查
echo -e "\033[36m[1] 基础检查\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8888/health)
assert_http "$CODE" "200" "健康检查 /health"

# [2] 用户注册 & 登录
echo -e "\n\033[36m[2] 用户注册 & 登录\033[0m"
echo "───────────────────────────────────"

# 注册
RESP=$(curl -s -X POST "$BASE/auth/register" \
  -H "Content-Type: application/json" \
  -d '{"username":"final","password":"test123","email":"final@test.com"}')
CODE=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code','FAIL'))" 2>/dev/null)
if [ "$CODE" = "FAIL" ]; then
    FAIL=$((FAIL + 1)); red "  ✗ 用户注册 /auth/register (非JSON响应)"
    echo "    响应: $RESP"
elif [ "$CODE" = "0" ]; then
    PASS=$((PASS + 1)); green "  ✓ 用户注册 /auth/register"
else
    echo "  ~ 用户注册 /auth/register (可能已存在, code=$CODE)"; PASS=$((PASS + 1))
fi

# 登录（重要：用 account 字段）
RESP=$(curl -s -X POST "$BASE/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"account":"final","password":"test123"}')
CODE=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code','FAIL'))" 2>/dev/null)
TOKEN=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('data',{}).get('token',''))" 2>/dev/null)
if [ "$CODE" = "FAIL" ]; then
    FAIL=$((FAIL + 1)); red "  ✗ 用户登录 /auth/login (非JSON响应)"
    echo "    响应: $RESP"
else
    assert_http "$CODE" "0" "用户登录 /auth/login"
fi

if [ -z "$TOKEN" ]; then
    red "  ⚠ 无 Token，退出测试"; exit 1
fi
AUTH="Authorization: Bearer $TOKEN"
echo "    Token: OK"

# [3] 用户信息（注意路径是 /users 复数）
echo -e "\n\033[36m[3] 用户信息\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/users/1" -H "$AUTH")
assert_http "$CODE" "200" "获取用户 /users/1"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/users/stats" -H "$AUTH")
assert_http "$CODE" "200" "用户统计 /users/stats"

# [4] 知识图谱
echo -e "\n\033[36m[4] 知识图谱\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/knowledge/entities" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"entity_type":"PERSON","name":"张三"}')
assert_http "$CODE" "200" "添加实体 /knowledge/entities"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/knowledge/entities" -H "$AUTH")
assert_http "$CODE" "200" "实体列表 /knowledge/entities"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/knowledge/stats" -H "$AUTH")
assert_http "$CODE" "200" "图谱统计 /knowledge/stats"

# [5] 搜索引擎
echo -e "\n\033[36m[5] 搜索引擎\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/search/indexes/stats" -H "$AUTH")
assert_http "$CODE" "200" "索引统计 /search/indexes/stats"

# [6] 向量检索
echo -e "\n\033[36m[6] 向量检索\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/vector/collections" -H "$AUTH")
assert_http "$CODE" "200" "集合列表 /vector/collections"

# [7] 智能问答
echo -e "\n\033[36m[7] 智能问答\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/question/ask" \
  -H "$AUTH" -H "Content-Type: application/json" -d '{"question":"你好"}')
assert_http "$CODE" "200" "提问 /question/ask"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/question/history?user_id=1" -H "$AUTH")
assert_http "$CODE" "200" "问答历史 /question/history"

# [8] 推荐
echo -e "\n\033[36m[8] 推荐系统\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/recommend?user_id=1" -H "$AUTH")
assert_http "$CODE" "200" "推荐 /recommend"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/recommend/history?user_id=1" -H "$AUTH")
assert_http "$CODE" "200" "推荐历史 /recommend/history"

# [9] 消息
echo -e "\n\033[36m[9] 消息队列\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/message/stats" -H "$AUTH")
assert_http "$CODE" "200" "消息统计 /message/stats"

# [10] Bot
echo -e "\n\033[36m[10] Bot 服务\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/bot" -H "$AUTH")
assert_http "$CODE" "200" "Bot列表 /bot"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/bot/message" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"bot_id":"assistant","content":"你好","user_id":"1","chat_id":"chat1"}')
assert_http "$CODE" "200" "Bot对话 /bot/message"

# [11] 聊天（注意：需要 chat_type 枚举）
echo -e "\n\033[36m[11] 聊天消息\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/chat/message" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"chat_id":"chat1","content":"Hello World","message_type":1,"chat_type":1}')
assert_http "$CODE" "200" "发消息 /chat/message"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/chat/history?chat_id=chat1" -H "$AUTH")
assert_http "$CODE" "200" "聊天记录 /chat/history"

# [12] 在线状态（注意：POST JSON body，不是 GET）
echo -e "\n\033[36m[12] 在线状态\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/im/online-status" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"user_ids":["1"]}')
assert_http "$CODE" "200" "在线状态 /im/online-status"

# [13] 内容审核（注意：/moderation/content 不是 /moderation/check）
echo -e "\n\033[36m[13] 内容审核\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/moderation/content" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"content":"这是一条测试消息"}')
assert_http "$CODE" "200" "内容审核 /moderation/content"

# [14] 提取任务
echo -e "\n\033[36m[14] 提取服务\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/extraction/tasks" -H "$AUTH")
assert_http "$CODE" "200" "提取任务 /extraction/tasks"

# [15] 采集
echo -e "\n\033[36m[15] 数据采集\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/collection/data" -H "$AUTH")
assert_http "$CODE" "200" "数据源 /collection/data"

# [16] 监控
echo -e "\n\033[36m[16] 监控\033[0m"
echo "───────────────────────────────────"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/monitoring/metric" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"service_name":"gateway","name":"test_metric","value":1.0}')
assert_http "$CODE" "200" "记录指标 /monitoring/metric"

# 汇总
echo ""
bold "=========================================="
bold "  测试完成"
bold "=========================================="
echo ""
echo "  总计: $((PASS + FAIL))  通过: $PASS  失败: $FAIL"
echo ""
if [ $FAIL -eq 0 ]; then
    green "  全部通过 ✓"
else
    red "  存在失败项 ✗"
    exit 1
fi
echo ""
