[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$sourceDirectory = Split-Path -Parent $PSCommandPath
$installDirectory = Join-Path $env:LOCALAPPDATA "Programs\forcey"

New-Item -ItemType Directory -Path $installDirectory -Force | Out-Null

Copy-Item `
    -LiteralPath (Join-Path $sourceDirectory "forcey.ps1") `
    -Destination (Join-Path $installDirectory "forcey.ps1") `
    -Force

Copy-Item `
    -LiteralPath (Join-Path $sourceDirectory "forcey.cmd") `
    -Destination (Join-Path $installDirectory "forcey.cmd") `
    -Force

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
$pathParts = @(
    $userPath -split ";" |
        Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
)

$alreadyInstalled = $pathParts | Where-Object {
    $_.TrimEnd("\").Equals(
        $installDirectory.TrimEnd("\"),
        [System.StringComparison]::OrdinalIgnoreCase
    )
}

if (-not $alreadyInstalled) {
    $newPath = if ([string]::IsNullOrWhiteSpace($userPath)) {
        $installDirectory
    }
    else {
        $userPath.TrimEnd(";") + ";" + $installDirectory
    }

    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
}

Write-Host ""
Write-Host "✨ forcey installed to:" -ForegroundColor Magenta
Write-Host "   $installDirectory"
Write-Host ""
Write-Host "Open a new CMD or PowerShell window, then run:"
Write-Host '   forcey "C:\path\to\stubborn-folder"' -ForegroundColor Cyan
Write-Host ""
