#!/bin/bash

# Logos 微服务系统 - Docker 管理脚本
# 架构: Platform / Messaging / AI 三大领域

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

print_banner() {
    echo -e "${CYAN}"
    echo "  ╔════════════════════════════════════════════════════╗"
    echo "  ║     Logos - AI-Powered Instant Messaging          ║"
    echo "  ║            Docker 管理面板                         ║"
    echo "  ╚════════════════════════════════════════════════════╝"
    echo -e "${NC}"
}

show_menu() {
    print_banner
    echo -e "  ${GREEN}[1]${NC} 启动基础设施 (etcd, PG, Redis, Kafka...)"
    echo -e "  ${GREEN}[2]${NC} 启动监控栈 (Prometheus, Jaeger)"
    echo -e "  ${GREEN}[3]${NC} 启动所有微服务 (14个服务)"
    echo -e "  ${GREEN}[4]${NC} 启动完整系统 (全部服务) ⭐推荐"
    echo -e "  ${YELLOW}[5]${NC} 停止所有服务"
    echo -e "  ${YELLOW}[6]${NC} 重启所有服务"
    echo -e "  ${BLUE}[7]${NC} 查看服务状态"
    echo -e "  ${BLUE}[8]${NC} 查看实时日志"
    echo -e "  ${RED}[9]${NC} 清理数据卷和镜像"
    echo -e "  ${RED}[0]${NC} 退出"
    echo ""
}

start_infra() {
    echo -e "${YELLOW}[*] 正在启动基础设施...${NC}"
    docker-compose up -d etcd postgres redis minio milvus kafka zookeeper elasticsearch neo4j
    echo -e "${GREEN}✓ 基础设施启动完成！${NC}"
    show_infra_urls
}

start_monitoring() {
    echo -e "${YELLOW}[*] 正在启动监控栈...${NC}"
    docker-compose up -d prometheus jaeger otel-collector
    echo -e "${GREEN}✓ 监控栈启动完成！${NC}"
    show_monitoring_urls
}

start_services() {
    echo -e "${YELLOW}[*] 正在启动所有微服务...${NC}"
    docker-compose up -d \
        gateway user-service monitoring-service \
        im-service chat-service contact-service message-service \
        knowledge-service search-service vector-service question-service recommend-service extraction-service collection-service
    echo -e "${GREEN}✓ 所有微服务启动完成！${NC}"
    show_service_ports
}

start_all() {
    echo -e "${YELLOW}[*] 正在启动完整 Logos 系统（包含所有组件）...${NC}"
    docker-compose up -d
    echo ""
    echo -e "${GREEN}============================================${NC}"
    echo -e "${GREEN}   Logos 平台已就绪${NC}"
    echo -e "${GREEN}============================================${NC}"
    echo ""
    show_all_urls
}

stop_all() {
    echo -e "${YELLOW}[*] 正在停止所有服务...${NC}"
    docker-compose down
    echo -e "${GREEN}✓ 所有服务已停止${NC}"
}

restart_all() {
    stop_all
    start_all
}

show_status() {
    echo -e "${BLUE}[*] 服务状态:${NC}"
    echo ""
    docker-compose ps
}

show_logs() {
    echo -e "${BLUE}[*] 实时日志 (按 Ctrl+C 退出):${NC}"
    echo ""
    docker-compose logs -f --tail=100
}

clean_all() {
    echo -e "${RED}⚠️  警告: 此操作将删除所有数据卷和容器！${NC}"
    read -p "确认清理? (y/n): " confirm
    if [ "$confirm" = "y" ]; then
        echo -e "${YELLOW}[*] 正在清理...${NC}"
        docker-compose down -v
        docker system prune -f
        echo -e "${GREEN}✓ 清理完成${NC}"
    fi
}

show_infra_urls() {
    echo ""
    echo -e "${BLUE}基础设施访问地址:${NC}"
    echo "  - etcd:          http://localhost:2379"
    echo "  - PostgreSQL:    localhost:5432 (logos/logos123456)"
    echo "  - Redis:         localhost:6379 (redis123456)"
    echo "  - Milvus:        localhost:19530"
    echo "  - Kafka:         localhost:9092"
    echo "  - Elasticsearch: http://localhost:9200"
    echo "  - Neo4j:         http://localhost:7474 (neo4j/neo4j123456)"
    echo "  - Minio:         http://localhost:9001 (minioadmin/minioadmin123)"
}

show_monitoring_urls() {
    echo ""
    echo -e "${BLUE}监控栈访问地址:${NC}"
    echo "  - Prometheus:  http://localhost:9090"
    echo "  - Jaeger:      http://localhost:16686"
}

show_service_ports() {
    echo ""
    echo -e "${CYAN}[Platform 领域]${NC}"
    echo "  - Gateway:       :8888 (API入口)"
    echo "  - User:          :9001"
    echo "  - Monitoring:    :9010"
    echo ""
    echo -e "${CYAN}[Messaging 领域]${NC}"
    echo "  - IM:            :9011 (连接管理)"
    echo "  - Chat:          :9012 (会话管理)"
    echo "  - Contact:       :9013 (联系人)"
    echo "  - Message:       :9009 (消息存储)"
    echo ""
    echo -e "${CYAN}[AI 领域]${NC}"
    echo "  - Knowledge:     :9002 (知识库)"
    echo "  - Search:        :9003 (搜索)"
    echo "  - Vector:        :9004 (向量检索)"
    echo "  - Question:      :9005 (智能问答)"
    echo "  - Recommend:     :9006 (推荐)"
    echo "  - Extraction:    :9007 (文档提取)"
    echo "  - Collection:    :9008 (知识集合)"
}

show_all_urls() {
    echo -e "${CYAN}核心入口:${NC}"
    echo "  API Gateway:    http://localhost:8888"
    echo "  Prometheus:     http://localhost:9090"
    echo "  Jaeger:         http://localhost:16686"
    echo ""
    show_infra_urls
}

# 主逻辑
if [ $# -eq 0 ]; then
    show_menu
    read -p "请输入选项 [0-9]: " choice
else
    choice=$1
fi

case $choice in
    1) start_infra ;;
    2) start_monitoring ;;
    3) start_services ;;
    4) start_all ;;
    5) stop_all ;;
    6) restart_all ;;
    7) show_status ;;
    8) show_logs ;;
    9) clean_all ;;
    0) echo "感谢使用 Logos 微服务系统！" && exit 0 ;;
    *) echo -e "${RED}无效选项${NC}" && exit 1 ;;
esac

echo ""
read -p "按回车键继续..."
