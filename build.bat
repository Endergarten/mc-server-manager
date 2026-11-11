@echo off
setlocal
cd /d "%~dp0"
where go >nul 2>&1
if errorlevel 1 (
    echo Install Go 1.25 or newer: https://go.dev/dl/
    pause
    exit /b 1
)
powershell -NoProfile -File "%~dp0scripts\windows\build.ps1"
if errorlevel 1 (
    pause
    exit /b 1
)
pause
