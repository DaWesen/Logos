
# 批量替换导入路径的PowerShell脚本
$projectDir = "c:\Users\misono mika\Desktop\Logos"
$files = Get-ChildItem -Path $projectDir -Recurse -Filter "*.go"

Write-Host "开始处理 $($files.Count) 个文件..." -ForegroundColor Cyan

foreach ($file in $files) {
    $content = Get-Content $file.FullName -Raw
    $modified = $false

    # 替换 internal/ai/ -> internal/service/ai/
    if ($content -match 'Logos/internal/ai/') {
        $content = $content -replace 'Logos/internal/ai/', 'Logos/internal/service/ai/'
        $modified = $true
    }

    # 替换 internal/messaging/ -> internal/service/messaging/
    if ($content -match 'Logos/internal/messaging/') {
        $content = $content -replace 'Logos/internal/messaging/', 'Logos/internal/service/messaging/'
        $modified = $true
    }

    # 替换 internal/platform/ -> internal/service/platform/
    if ($content -match 'Logos/internal/platform/') {
        $content = $content -replace 'Logos/internal/platform/', 'Logos/internal/service/platform/'
        $modified = $true
    }

    if ($modified) {
        Set-Content -Path $file.FullName -Value $content -NoNewline
        Write-Host "已修改: $($file.FullName)" -ForegroundColor Green
    }
}

Write-Host "完成！" -ForegroundColor Cyan
