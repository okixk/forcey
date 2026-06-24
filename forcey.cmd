@echo off
setlocal

where pwsh.exe >nul 2>&1
if %errorlevel% equ 0 (
    pwsh.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0forcey.ps1" %*
) else (
    powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0forcey.ps1" %*
)

exit /b %errorlevel%
