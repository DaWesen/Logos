#!/usr/bin/env pwsh
<#
.SYNOPSIS
    Logos 综合压测脚本

.DESCRIPTION
    支持多种压测场景：
    - http:     HTTP API 压测
    - ws:       WebSocket 压测
    - full:     综合压测（HTTP + WebSocket 同时）
    - smoke:    快速冒烟测试

.EXAMPLE
    .\bench.ps1 http
    .\bench.ps1 ws -URL http://localhost:8888 -Users 200 -Duration 60s
    .\bench.ps1 full
    .\bench.ps1 smoke
#>

param(
    [Parameter(Position=0)]
    [ValidateSet("http", "ws", "full", "smoke")]
    [string]$Mode = "http",

    [string]$URL = "http://localhost:8888",
    [int]$Users = 100,
    [string]$Duration = "30s",
    [int]$RPS = 1
)

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectDir = Split-Path -Parent (Split-Path -Parent $ScriptDir)

function Build-BenchTools {
    Write-Host "编译压测工具..." -ForegroundColor Yellow
    
    if (-not (Test-Path "$ScriptDir\http_bench.exe")) {
        Write-Host "  编译 HTTP 压测工具..." -ForegroundColor White
        Push-Location "$ScriptDir"
        go build -o http_bench.exe .
        Pop-Location
    }
    
    if (-not (Test-Path "$ScriptDir\ws\ws_bench.exe")) {
        Write-Host "  编译 WebSocket 压测工具..." -ForegroundColor White
        Push-Location "$ScriptDir\ws"
        go build -o ..\ws_bench.exe .
        Pop-Location
    }
    
    Write-Host "  编译完成" -ForegroundColor Green
}

function Run-HTTPBench {
    param([string]$Scenario = "mixed")
    Write-Host ""
    Write-Host "========== HTTP API 压测 ==========" -ForegroundColor Cyan
    Write-Host "目标: $URL" -ForegroundColor White
    Write-Host "场景: $Scenario" -ForegroundColor White
    Write-Host "并发: $Users" -ForegroundColor White
    Write-Host "时长: $Duration" -ForegroundColor White
    Write-Host "====================================" -ForegroundColor Cyan
    Write-Host ""
    & "$ScriptDir\http_bench.exe" -url $URL -users $Users -duration $Duration -rps $RPS $Scenario
}

function Run-WSBench {
    Write-Host ""
    Write-Host "========== WebSocket 压测 ==========" -ForegroundColor Cyan
    Write-Host "目标: $URL" -ForegroundColor White
    Write-Host "连接数: $Users" -ForegroundColor White
    Write-Host "时长: $Duration" -ForegroundColor White
    Write-Host "====================================" -ForegroundColor Cyan
    Write-Host ""
    & "$ScriptDir\ws_bench.exe" -url $URL -conns $Users -duration $Duration -rate $RPS
}

function Run-SmokeTest {
    Write-Host ""
    Write-Host "========== 冒烟测试 ==========" -ForegroundColor Cyan
    
    $tests = @(
        @{ Name = "健康检查"; URL = "$URL/health"; Method = "GET" },
        @{ Name = "用户注册"; URL = "$URL/api/v1/auth/register"; Method = "POST"; Body = '{"username":"smoke_test","password":"Test123456!"}' }
    )
    
    foreach ($test in $tests) {
        Write-Host "  测试: $($test.Name) ... " -NoNewline
        try {
            if ($test.Method -eq "GET") {
                $resp = Invoke-WebRequest -Uri $test.URL -TimeoutSec 5 -UseBasicParsing
            } else {
                $resp = Invoke-WebRequest -Uri $test.URL -Method POST -Body $test.Body -ContentType "application/json" -TimeoutSec 5 -UseBasicParsing
            }
            if ($resp.StatusCode -eq 200 -or $resp.StatusCode -eq 201) {
                Write-Host "OK ($($resp.StatusCode))" -ForegroundColor Green
            } else {
                Write-Host "WARN ($($resp.StatusCode))" -ForegroundColor Yellow
            }
        } catch {
            Write-Host "FAIL ($($_.Exception.Message))" -ForegroundColor Red
        }
    }
    Write-Host "================================" -ForegroundColor Cyan
}

Build-BenchTools

switch ($Mode) {
    "http" {
        Write-Host ""
        Write-Host "可用场景: health, login, get_user, chat_history, send_message, bot_list, billing, monitoring, contacts, mixed" -ForegroundColor White
        Run-HTTPBench -Scenario "mixed"
    }
    "ws" {
        Run-WSBench
    }
    "full" {
        Write-Host ""
        Write-Host "启动综合压测（HTTP + WebSocket 同时）..." -ForegroundColor Yellow
        
        $httpJob = Start-Job -ScriptBlock {
            param($scriptDir, $url, $users, $duration)
            & "$scriptDir\http_bench.exe" -url $url -users $users -duration $duration -rps 1 "mixed"
        } -ArgumentList $ScriptDir, $URL, $Users, $Duration
        
        $wsJob = Start-Job -ScriptBlock {
            param($scriptDir, $url, $users, $duration)
            & "$scriptDir\ws_bench.exe" -url $url -conns $users -duration $duration -rate 1
        } -ArgumentList $ScriptDir, $URL, $Users, $Duration
        
        Write-Host "  HTTP 压测 Job ID: $($httpJob.Id)" -ForegroundColor White
        Write-Host "  WebSocket 压测 Job ID: $($wsJob.Id)" -ForegroundColor White
        Write-Host ""
        Write-Host "  等待压测完成..." -ForegroundColor Yellow
        
        Wait-Job $httpJob, $wsJob
        
        Write-Host ""
        Write-Host "--- HTTP 压测结果 ---" -ForegroundColor Cyan
        Receive-Job $httpJob
        Write-Host ""
        Write-Host "--- WebSocket 压测结果 ---" -ForegroundColor Cyan
        Receive-Job $wsJob
        
        Remove-Job $httpJob, $wsJob
    }
    "smoke" {
        Run-SmokeTest
    }
}
