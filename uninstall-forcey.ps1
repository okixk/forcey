[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$installDirectory = Join-Path $env:LOCALAPPDATA "Programs\forcey"
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")

$remainingParts = @(
    $userPath -split ";" |
        Where-Object {
            -not [string]::IsNullOrWhiteSpace($_) -and
            -not $_.TrimEnd("\").Equals(
                $installDirectory.TrimEnd("\"),
                [System.StringComparison]::OrdinalIgnoreCase
            )
        }
)

[Environment]::SetEnvironmentVariable(
    "Path",
    ($remainingParts -join ";"),
    "User"
)

Write-Host "forcey was removed from your user PATH."

if (Test-Path -LiteralPath $installDirectory) {
    $escapedDirectory = $installDirectory.Replace('"', '""')

    Start-Process `
        -FilePath $env:ComSpec `
        -WindowStyle Hidden `
        -ArgumentList "/d /c timeout /t 1 /nobreak >nul & rd /s /q `"$escapedDirectory`""
}

Write-Host "Open a new terminal for the PATH change to take effect."
