@echo off
chcp 65001 >nul 2>&1
setlocal enabledelayedexpansion

set PASS=0
set FAIL=0
set TOTAL=0

echo.
echo ========================================
echo   Logos 项目一键测试
echo ========================================
echo.

cd /d "%~dp0\.."

echo [1/4] Go Vet 静态检查
echo ─────────────────────────────────────
set /a TOTAL+=1
go vet ./... >nul 2>&1
if %errorlevel% equ 0 (
    set /a PASS+=1
    echo   [PASS]  go vet ./...
) else (
    set /a FAIL+=1
    echo   [FAIL]  go vet ./...
    go vet ./... 2>&1 | findstr /n "." | findstr "^[1-9]" >nul && go vet ./... 2>&1
)
echo.

echo [2/4] 编译检查 (go build ./cmd/...)
echo ─────────────────────────────────────
set /a TOTAL+=1
go build ./cmd/... >nul 2>&1
if %errorlevel% equ 0 (
    set /a PASS+=1
    echo   [PASS]  go build ./cmd/...
) else (
    set /a FAIL+=1
    echo   [FAIL]  go build ./cmd/...
    go build ./cmd/... 2>&1
)
echo.

echo [3/4] 逐服务编译检查
echo ─────────────────────────────────────

set TMPDIR=%TEMP%\logos-build-test
if not exist "%TMPDIR%" mkdir "%TMPDIR%"

call :build_svc "Gateway"   "./cmd/platform/gateway/"
call :build_svc "User"      "./cmd/platform/user/"
call :build_svc "Billing"   "./cmd/platform/billing/"
call :build_svc "Monitoring" "./cmd/platform/monitoring/"
call :build_svc "Chat"      "./cmd/messaging/chat/"
call :build_svc "IM"        "./cmd/messaging/im/"
call :build_svc "Contact"   "./cmd/messaging/contact/"
call :build_svc "Message"   "./cmd/messaging/message/"
call :build_svc "Bot"       "./cmd/ai/bot/"
call :build_svc "Knowledge" "./cmd/ai/knowledge/"
call :build_svc "Search"    "./cmd/ai/search/"
call :build_svc "Vector"    "./cmd/ai/vector/"
call :build_svc "Summary"   "./cmd/ai/summary/"
call :build_svc "MCP"       "./cmd/ai/mcp/"
call :build_svc "Moderation" "./cmd/ai/moderation/"
call :build_svc "Question"  "./cmd/ai/question/"
call :build_svc "Recommend" "./cmd/ai/recommend/"
call :build_svc "Extraction" "./cmd/ai/extraction/"
call :build_svc "Collection" "./cmd/ai/collection/"
call :build_svc "Process"   "./cmd/ai/process/"
echo.

echo [4/4] gRPC 健康检查注册验证
echo ─────────────────────────────────────
set /a TOTAL+=1
findstr /r "grpc_health_v1" pkg\grpcserver\server.go >nul 2>&1
if %errorlevel% equ 0 (
    set /a PASS+=1
    echo   [PASS]  gRPC 健康检查已注册到 StartServer
) else (
    set /a FAIL+=1
    echo   [FAIL]  gRPC 健康检查未找到
)
echo.

echo ========================================
echo   汇总
echo ========================================
echo.
echo   总计: %TOTAL%  通过: %PASS%  失败: %FAIL%
echo.

if %FAIL% equ 0 (
    echo   全部通过
) else (
    echo   存在失败项
    exit /b 1
)
echo.

rd /s /q "%TMPDIR%" 2>nul
exit /b 0

:build_svc
set /a TOTAL+=1
set SVCNAME=%~1
set SVCPKG=%~2
go build -o "%TMPDIR%\%SVCNAME%.exe" %SVCPKG% >nul 2>&1
if %errorlevel% equ 0 (
    set /a PASS+=1
    echo   [PASS]  %SVCNAME%
) else (
    set /a FAIL+=1
    echo   [FAIL]  %SVCNAME%
    go build -o "%TMPDIR%\%SVCNAME%.exe" %SVCPKG% 2>&1
)
exit /b 0
