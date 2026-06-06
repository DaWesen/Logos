#!/usr/bin/env pwsh
$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectDir = Split-Path -Parent $ScriptDir

Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "  Logos Kubernetes Deployment (kind)" -ForegroundColor Cyan
Write-Host "=========================================" -ForegroundColor Cyan

$InfraImages = @(
    "docker.1panel.live/bitnami/etcd:latest",
    "docker.1ms.run/library/postgres:latest",
    "docker.1ms.run/library/redis:latest",
    "docker.1panel.live/minio/minio:latest",
    "milvusdb/milvus:latest",
    "docker.m.daocloud.io/elasticsearch:8.19.5",
    "docker.1ms.run/neo4j:latest",
    "wurstmeister/kafka:latest",
    "wurstmeister/zookeeper:latest"
)

function Check-Prerequisites {
    Write-Host "[1/8] Checking prerequisites..." -ForegroundColor Yellow
    foreach ($cmd in @("kind", "kubectl", "docker")) {
        if (-not (Get-Command $cmd -ErrorAction SilentlyContinue)) {
            Write-Host "ERROR: $cmd is not installed. Please install it first." -ForegroundColor Red
            exit 1
        }
    }
    Write-Host "  All prerequisites met." -ForegroundColor Green
}

function Create-Cluster {
    Write-Host "[2/8] Creating kind cluster..." -ForegroundColor Yellow
    $clusters = kind get clusters 2>$null
    if ($clusters -contains "logos") {
        Write-Host "  Cluster 'logos' already exists, skipping." -ForegroundColor Green
    } else {
        kind create cluster --config "$ScriptDir/kind-cluster.yaml"
        Write-Host "  Cluster created." -ForegroundColor Green
    }
}

function Build-And-Load-Image {
    Write-Host "[3/8] Building Docker image..." -ForegroundColor Yellow
    docker build -t logos:latest $ProjectDir
    Write-Host "  Loading image into kind..." -ForegroundColor Yellow
    kind load docker-image logos:latest --name logos
    Write-Host "  Image loaded." -ForegroundColor Green
}

function Load-Infra-Images {
    Write-Host "[4/8] Loading infrastructure images into kind..." -ForegroundColor Yellow
    foreach ($img in $InfraImages) {
        Write-Host "  Loading $img ..." -ForegroundColor White
        kind load docker-image $img --name logos 2>$null
        if ($LASTEXITCODE -ne 0) {
            Write-Host "  WARNING: Failed to load $img, K8s will try to pull from registry." -ForegroundColor DarkYellow
        }
    }
    Write-Host "  Infrastructure images loaded." -ForegroundColor Green
}

function Deploy-Namespace {
    Write-Host "[5/8] Creating namespace..." -ForegroundColor Yellow
    kubectl apply -f "$ScriptDir\namespace.yaml"
}

function Deploy-Infra {
    Write-Host "[6/8] Deploying infrastructure..." -ForegroundColor Yellow
    kubectl apply -f "$ScriptDir\infra\etcd.yaml"
    kubectl apply -f "$ScriptDir\infra\postgres.yaml"
    kubectl apply -f "$ScriptDir\infra\redis.yaml"
    kubectl apply -f "$ScriptDir\infra\kafka.yaml"
    kubectl apply -f "$ScriptDir\infra\minio.yaml"
    kubectl apply -f "$ScriptDir\infra\milvus.yaml"
    kubectl apply -f "$ScriptDir\infra\elasticsearch.yaml"
    kubectl apply -f "$ScriptDir\infra\neo4j.yaml"
    Write-Host "  Waiting for infrastructure to be ready..." -ForegroundColor Yellow
    kubectl wait deployment --all --for=condition=available -n logos --timeout=120s 2>$null
}

function Deploy-Config {
    Write-Host "[7/8] Deploying ConfigMap and Secrets..." -ForegroundColor Yellow
    kubectl apply -f "$ScriptDir\configmap.yaml"
}

function Deploy-Services {
    Write-Host "[8/8] Deploying microservices..." -ForegroundColor Yellow
    Get-ChildItem "$ScriptDir\services\*.yaml" | ForEach-Object {
        kubectl apply -f $_.FullName
    }
    Write-Host "  Waiting for services to be ready..." -ForegroundColor Yellow
    kubectl wait deployment --all --for=condition=available -n logos --timeout=180s 2>$null
}

function Show-Status {
    Write-Host ""
    Write-Host "=========================================" -ForegroundColor Cyan
    Write-Host "  Deployment Complete!" -ForegroundColor Green
    Write-Host "=========================================" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "  Gateway: http://localhost:30080" -ForegroundColor White
    Write-Host ""
    Write-Host "  Useful commands:" -ForegroundColor White
    Write-Host "    kubectl get pods -n logos"
    Write-Host "    kubectl logs -f deployment/gateway -n logos"
    Write-Host "    kubectl get svc -n logos"
    Write-Host ""
    kubectl get pods -n logos
}

Check-Prerequisites
Create-Cluster
Build-And-Load-Image
Load-Infra-Images
Deploy-Namespace
Deploy-Infra
Deploy-Config
Deploy-Services
Show-Status
