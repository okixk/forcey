[CmdletBinding()]
param(
    [string]$Version = "1.0.0"
)

$ErrorActionPreference = "Stop"
$root = $PSScriptRoot
$dist = Join-Path $root "dist"
$release = Join-Path $dist "release"
$archive = Join-Path $dist "forcey-windows-x64.zip"

Remove-Item -LiteralPath $dist -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $release -Force | Out-Null

$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

go build `
    -buildvcs=false `
    -trimpath `
    -ldflags "-s -w -X main.version=$Version" `
    -o (Join-Path $release "forcey.exe") `
    .\cmd\forcey

Copy-Item (Join-Path $root "README.md") $release
Copy-Item (Join-Path $root "LICENSE") $release
Copy-Item (Join-Path $root "THIRD_PARTY.md") $release

Compress-Archive `
    -Path (Join-Path $release "*") `
    -DestinationPath $archive `
    -CompressionLevel Optimal

$exePath = Join-Path $release "forcey.exe"
$exeHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $exePath).Hash.ToLowerInvariant()
$archiveHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
$checksums = @(
    "$exeHash  forcey.exe"
    "$archiveHash  forcey-windows-x64.zip"
)
[IO.File]::WriteAllLines(
    (Join-Path $dist "SHA256SUMS.txt"),
    $checksums,
    [Text.UTF8Encoding]::new($false)
)
Write-Host "Built: $archive"
Write-Host "SHA256: $archiveHash"
