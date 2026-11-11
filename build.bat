@echo off
setlocal
cd /d "%~dp0"
where go >nul 2>&1
if errorlevel 1 (
    echo Install Go 1.22 or newer first: https://go.dev/dl/
    pause
    exit /b 1
)
go test ./...
if errorlevel 1 exit /b 1
go vet ./...
if errorlevel 1 exit /b 1
go build -trimpath -o dist\mc-server-manager.exe .
if errorlevel 1 exit /b 1
echo Build ready: dist\mc-server-manager.exe
pause
