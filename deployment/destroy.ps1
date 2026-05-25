#!/usr/bin/env pwsh
$ErrorActionPreference = "Stop"

Write-Host "Destroying Logos Kubernetes deployment..." -ForegroundColor Yellow

$clusters = kind get clusters 2>$null
if ($clusters -contains "logos") {
    kind delete cluster --name logos
    Write-Host "Cluster 'logos' deleted." -ForegroundColor Green
} else {
    Write-Host "Cluster 'logos' not found." -ForegroundColor Gray
}
