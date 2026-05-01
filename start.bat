@echo off
chcp 65001 >nul 2>&1
title Logos - AI-Powered Instant Messaging

:::menu
cls
echo.
echo  ╔════════════════════════════════════════════════════╗
echo  ║     Logos - AI-Powered Instant Messaging          ║
echo  ║            Docker 管理面板                         ║
echo  ╠════════════════════════════════════════════════════╣
echo  ║                                                    ║
echo  ║   [1] 启动基础设施 (etcd, PG, Redis, Kafka...)     ║
echo  ║   [2] 启动监控栈 (Prometheus, Jaeger)              ║
echo  ║   [3] 启动所有微服务 (17个服务)                     ║
echo  ║   [4] 启动完整系统 (全部服务)                       ║
echo  ║   [5] 停止所有服务                                  ║
echo  ║   [6] 重启所有服务                                  ║
echo  ║   [7] 查看服务状态                                  ║
echo  ║   [8] 查看日志                                      ║
echo  ║   [9] 构建镜像                                      ║
echo  ║   [a] 清理Docker缓存                                ║
echo  ║   [b] 清理数据卷和镜像                              ║
echo  ║   [0] 退出                                          ║
echo  ║                                                    ║
echo  ╚════════════════════════════════════════════════════╝
echo.
set /p choice=请输入选项 [0-9,a-b]:

if "%choice%"=="1" goto infra
if "%choice%"=="2" goto monitoring
if "%choice%"=="3" goto services
if "%choice%"=="4" goto all
if "%choice%"=="5" goto stop
if "%choice%"=="6" goto restart
if "%choice%"=="7" goto status
if "%choice%"=="8" goto logs
if "%choice%"=="9" goto build
if "%choice%"=="a" goto clean_cache
if "%choice%"=="b" goto clean
if "%choice%"=="0" goto end

echo 无效选项，请重新选择
timeout /t 2 >nul
goto menu

:::infra
echo.
echo [*] 正在启动基础设施...
docker-compose up -d etcd postgres redis minio milvus kafka zookeeper elasticsearch neo4j
echo.
echo ✓ 基础设施启动完成！
echo.
echo 访问地址:
echo   - etcd:          http://localhost:2379
echo   - PostgreSQL:    localhost:5432 (logos/logos123456)
echo   - Redis:         localhost:6379 (redis123456)
echo   - Milvus:        localhost:19530
echo   - Kafka:         localhost:9092
echo   - Elasticsearch: http://localhost:9200
echo   - Neo4j:         http://localhost:7474 (neo4j/neo4j123456)
echo   - Minio:         http://localhost:9001 (minioadmin/minioadmin123)
goto wait

:::monitoring
echo.
echo [*] 正在启动监控栈...
docker-compose up -d prometheus jaeger otel-collector
echo.
echo ✓ 监控栈启动完成！
echo.
echo 访问地址:
echo   - Prometheus:  http://localhost:9090
echo   - Jaeger:      http://localhost:16686
goto wait

:::services
echo.
echo [*] 正在启动所有微服务...
docker-compose up -d gateway user-service monitoring-service billing-service im-service chat-service contact-service message-service knowledge-service search-service vector-service question-service recommend-service extraction-service collection-service bot-service process-service
echo.
echo ✓ 所有微服务启动完成！
echo.
echo [Platform 领域]
echo   - Gateway:       :8888 (API入口)
echo   - User:          :9001
echo   - Monitoring:    :9010
echo   - Billing:       :9015 (计费)
echo.
echo [Messaging 领域]
echo   - IM:            :9011 (连接管理)
echo   - Chat:          :9012 (会话管理)
echo   - Contact:       :9013 (联系人)
echo   - Message:       :9009 (消息存储)
echo.
echo [AI 领域]
echo   - Knowledge:     :9002 (知识库)
echo   - Search:        :9003 (搜索)
echo   - Vector:        :9004 (向量检索)
echo   - Question:      :9005 (智能问答)
echo   - Recommend:     :9006 (推荐)
echo   - Extraction:    :9007 (文档提取)
echo   - Collection:    :9008 (知识集合)
echo   - Bot:           :9014 (AI助手)
echo   - Process:       :8090 (文档处理)
goto wait

:::all
echo.
echo [*] 正在启动完整 Logos 系统（包含所有组件）...
docker-compose up -d
echo.
echo ============================================
echo   Logos 平台已就绪
echo ============================================
echo.
echo 核心入口:
echo   API Gateway:    http://localhost:8888
echo   Prometheus:     http://localhost:9090
echo   Jaeger:         http://localhost:16686
echo.
echo 数据库连接:
echo   PostgreSQL:     localhost:5432
echo   Redis:          localhost:6379
echo   Milvus:         localhost:19530
echo   Elasticsearch:  localhost:9200
echo   Neo4j:          localhost:7687
echo   Kafka:          localhost:9092
echo.
goto wait

:::stop
echo.
echo [*] 正在停止所有服务...
docker-compose down
echo ✓ 所有服务已停止
goto wait

:::restart
echo.
echo [*] 正在重启所有服务...
docker-compose down
docker-compose up -d
echo ✓ 所有服务已重启
goto wait

:::status
echo.
echo [*] 服务状态:
echo.
docker-compose ps
goto wait

:::logs
echo.
echo [*] 实时日志 (按 Ctrl+C 退出):
echo.
docker-compose logs -f --tail=100
goto end

:::build
echo.
echo [*] 构建 Docker 镜像（无缓存）...
docker build --no-cache -t logos-app .
echo.
if %ERRORLEVEL% EQU 0 (
    echo ✓ 镜像构建成功！
    echo.
    echo 现在可以使用 docker-compose up -d 启动服务
) else (
    echo ✗ 构建失败！
    echo.
    echo 常见问题排查：
    echo   1. 运行选项 [a] 清理 Docker 缓存后重试
    echo   2. 检查 go.mod 中的 Go 版本要求
    echo   3. 确保网络连接正常
)
goto wait

:::clean_cache
echo.
echo ⚠️  正在清理 Docker 构建缓存...
echo.
docker builder prune -f
echo.
docker system prune -f
echo.
echo ✓ Docker 缓存已清理！
echo.
echo 建议：现在重新构建镜像
echo   docker build --no-cache -t logos-app .
goto wait

:::clean
echo.
echo ⚠️  警告: 此操作将删除所有数据卷、容器和未使用的镜像！
set /p confirm=确认清理? (y/n):
if /i not "%confirm%"=="y" goto menu

echo.
echo [*] 正在清理...
docker-compose down -v
docker system prune -a -f
echo ✓ 清理完成
echo.
echo 提示：重新拉取镜像可能需要一些时间
goto wait

:::wait
echo.
set /p any=按回车键返回主菜单...
goto menu

:::end
echo.
echo 感谢使用 Logos 微服务系统！
timeout /t 2 >nul
