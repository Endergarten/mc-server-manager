$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
Push-Location $projectRoot
try {
    # ID 3 is used by Wails for the window and taskbar icon.
    & go run github.com/tc-hib/go-winres@v0.3.3 make --arch amd64
    if ($LASTEXITCODE -ne 0) { throw 'Icon resource generation failed' }
} finally { Pop-Location }
