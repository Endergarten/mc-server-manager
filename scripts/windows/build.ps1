param([string]$WebViewRuntimeFolder = '')
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
Push-Location $projectRoot
try {
    & go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Tests failed' }
    & go vet -tags 'desktop,production,wv2runtime.embed' ./...
    if ($LASTEXITCODE -ne 0) { throw 'Checks failed' }
    & go build -tags 'desktop,production,wv2runtime.embed' -trimpath -ldflags '-H windowsgui' -o dist\mc-server-manager.exe .
    if ($LASTEXITCODE -ne 0) { throw 'Build failed' }
    $packageRoot = Join-Path $projectRoot 'dist\packages\windows-amd64'
    New-Item -ItemType Directory -Force -Path $packageRoot | Out-Null
    Copy-Item -LiteralPath 'dist\mc-server-manager.exe','README.md','LICENSE' -Destination $packageRoot -Force
    $notices = [Text.StringBuilder]::new()
    [void]$notices.AppendLine('Third-party licenses included in this Go/Wails distribution.')
    $modules = & go list -m -f '{{.Path}}|{{.Version}}|{{.Dir}}' all
    if ($LASTEXITCODE -ne 0) { throw 'Could not list module licenses' }
    foreach ($module in $modules) {
        $parts = $module.Split('|', 3)
        if ($parts.Length -lt 3 -or -not $parts[2]) { continue }
        $licenses = Get-ChildItem -LiteralPath $parts[2] -File -ErrorAction SilentlyContinue | Where-Object Name -Match '^(LICENSE|COPYING|NOTICE)'
        foreach ($license in $licenses) {
            [void]$notices.AppendLine("`n--- $($parts[0]) $($parts[1]) / $($license.Name) ---")
            [void]$notices.AppendLine((Get-Content -LiteralPath $license.FullName -Raw))
        }
    }
    $toolchainRoot = & go env GOROOT
    [void]$notices.AppendLine("`n--- Go toolchain/runtime ---")
    [void]$notices.AppendLine((Get-Content -LiteralPath (Join-Path $toolchainRoot 'LICENSE') -Raw))
    $noticePath = Join-Path $packageRoot 'THIRD_PARTY_NOTICES.txt'
    [IO.File]::WriteAllText($noticePath, $notices.ToString(), [Text.UTF8Encoding]::new($false))
    $archiveItems = @((Join-Path $packageRoot 'mc-server-manager.exe'), (Join-Path $packageRoot 'README.md'), (Join-Path $packageRoot 'LICENSE'), $noticePath)
    if ($WebViewRuntimeFolder) {
        $runtimeRoot = [IO.Path]::GetFullPath($WebViewRuntimeFolder)
        if (-not (Test-Path -LiteralPath (Join-Path $runtimeRoot 'msedgewebview2.exe'))) { throw 'Select the complete Microsoft Fixed Version Runtime directory' }
        $runtimeDest = Join-Path $packageRoot 'runtime\webview2'
        New-Item -ItemType Directory -Force -Path $runtimeDest | Out-Null
        Get-ChildItem -LiteralPath $runtimeRoot -Force | ForEach-Object { Copy-Item -LiteralPath $_.FullName -Destination $runtimeDest -Recurse -Force }
        $archiveItems += (Join-Path $packageRoot 'runtime')
    }
    $archive = Join-Path $projectRoot 'dist\mc-server-manager-windows-amd64.zip'
    Compress-Archive -LiteralPath $archiveItems -DestinationPath $archive -Force
    Write-Output "Ready: $archive"
} finally { Pop-Location }
