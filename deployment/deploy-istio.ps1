#!/usr/bin/env pwsh
$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectDir = Split-Path -Parent $ScriptDir

Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "  Logos Kubernetes + Istio Deployment" -ForegroundColor Cyan
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
    Write-Host "[1/10] Checking prerequisites..." -ForegroundColor Yellow
    foreach ($cmd in @("kind", "kubectl", "docker")) {
        if (-not (Get-Command $cmd -ErrorAction SilentlyContinue)) {
            Write-Host "ERROR: $cmd is not installed. Please install it first." -ForegroundColor Red
            exit 1
        }
    }
    
    $istioInstalled = Get-Command istioctl -ErrorAction SilentlyContinue
    if (-not $istioInstalled) {
        Write-Host "WARNING: istioctl is not installed. Will attempt to download it..." -ForegroundColor DarkYellow
    }
    Write-Host "  All prerequisites met." -ForegroundColor Green
}

function Create-Cluster {
    Write-Host "[2/10] Creating kind cluster..." -ForegroundColor Yellow
    $clusters = kind get clusters 2>$null
    if ($clusters -contains "logos") {
        Write-Host "  Cluster 'logos' already exists, skipping." -ForegroundColor Green
    } else {
        kind create cluster --config "$ScriptDir/kind-cluster.yaml"
        Write-Host "  Cluster created." -ForegroundColor Green
    }
}

function Install-Istio {
    Write-Host "[3/10] Installing Istio..." -ForegroundColor Yellow
    $istioInstalled = Get-Command istioctl -ErrorAction SilentlyContinue
    if (-not $istioInstalled) {
        Write-Host "  Downloading istioctl..." -ForegroundColor White
        $istioVersion = "1.24.0"
        $downloadUrl = "https://github.com/istio/istio/releases/download/$istioVersion/istio-$istioVersion-win.zip"
        $zipPath = "$ScriptDir/istio.zip"
        
        Invoke-WebRequest -Uri $downloadUrl -OutFile $zipPath
        Expand-Archive -Path $zipPath -DestinationPath "$ScriptDir" -Force
        $env:PATH += ";$ScriptDir/istio-$istioVersion/bin"
        Remove-Item $zipPath
    }
    
    istioctl install --set profile=demo -y
    Write-Host "  Istio installed." -ForegroundColor Green
}

function Build-And-Load-Image {
    Write-Host "[4/10] Building Docker image..." -ForegroundColor Yellow
    docker build -t logos:latest $ProjectDir
    Write-Host "  Loading image into kind..." -ForegroundColor Yellow
    kind load docker-image logos:latest --name logos
    Write-Host "  Image loaded." -ForegroundColor Green
}

function Load-Infra-Images {
    Write-Host "[5/10] Loading infrastructure images into kind..." -ForegroundColor Yellow
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
    Write-Host "[6/10] Creating namespace..." -ForegroundColor Yellow
    kubectl apply -f "$ScriptDir\namespace.yaml"
}

function Deploy-Infra {
    Write-Host "[7/10] Deploying infrastructure..." -ForegroundColor Yellow
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
    Write-Host "[8/10] Deploying ConfigMap and Secrets..." -ForegroundColor Yellow
    kubectl apply -f "$ScriptDir\configmap.yaml"
}

function Deploy-Services {
    Write-Host "[9/10] Deploying microservices..." -ForegroundColor Yellow
    Get-ChildItem "$ScriptDir\services\*.yaml" | ForEach-Object {
        if (-not $_.Name.Contains("canary")) {
            kubectl apply -f $_.FullName
        }
    }
    Write-Host "  Waiting for services to be ready..." -ForegroundColor Yellow
    kubectl wait deployment --all --for=condition=available -n logos --timeout=180s 2>$null
}

function Deploy-Istio-Gateway {
    Write-Host "[10/10] Deploying Istio gateway..." -ForegroundColor Yellow
    kubectl apply -f "$ScriptDir\istio-gateway.yaml"
    Write-Host "  Istio gateway deployed." -ForegroundColor Green
}

function Show-Status {
    Write-Host ""
    Write-Host "=========================================" -ForegroundColor Cyan
    Write-Host "  Deployment Complete!" -ForegroundColor Green
    Write-Host "=========================================" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "  Gateway: http://localhost:80" -ForegroundColor White
    Write-Host "  Kiali: http://localhost:20001" -ForegroundColor White
    Write-Host "  Grafana: http://localhost:3000" -ForegroundColor White
    Write-Host ""
    Write-Host "  Useful commands:" -ForegroundColor White
    Write-Host "    kubectl get pods -n logos"
    Write-Host "    kubectl logs -f deployment/gateway -n logos -c gateway"
    Write-Host "    kubectl get svc -n logos"
    Write-Host ""
    Write-Host "  Canary deployment:" -ForegroundColor White
    Write-Host "    See README_CANARY.md for instructions"
    Write-Host ""
    kubectl get pods -n logos
}

Check-Prerequisites
Create-Cluster
Install-Istio
Build-And-Load-Image
Load-Infra-Images
Deploy-Namespace
Deploy-Infra
Deploy-Config
Deploy-Services
Deploy-Istio-Gateway
Show-Status
