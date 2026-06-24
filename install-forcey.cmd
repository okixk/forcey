@echo off
setlocal

where pwsh.exe >nul 2>&1
if %errorlevel% equ 0 (
    pwsh.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0install-forcey.ps1"
) else (
    powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0install-forcey.ps1"
)

if not %errorlevel% equ 0 (
    echo.
    echo Installation failed.
    pause
    exit /b %errorlevel%
)

echo.
pause
