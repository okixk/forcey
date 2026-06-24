[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern("^[^/]+/[^/]+$")]
    [string]$Repository,

    [string]$Version = "1.0.0",

    [string]$ReleaseDate = (Get-Date -Format "yyyy-MM-dd"),

    [switch]$Submit
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$identifier = "okix.forcey"
$owner = $Repository.Split("/")[0]
$releaseUrl = "https://github.com/$Repository/releases/download/v$Version/forcey-windows-x64.zip"
$repositoryUrl = "https://github.com/$Repository"
$manifestDirectory = Join-Path $root "winget\manifests\o\okix\forcey\$Version"
$tempDirectory = Join-Path ([IO.Path]::GetTempPath()) "forcey-winget-$Version"
$archive = Join-Path $tempDirectory "forcey-windows-x64.zip"
$expanded = Join-Path $tempDirectory "expanded"

Remove-Item -LiteralPath $tempDirectory -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $tempDirectory -Force | Out-Null
New-Item -ItemType Directory -Path $manifestDirectory -Force | Out-Null

Write-Host "Downloading release asset..."
Invoke-WebRequest -Uri $releaseUrl -OutFile $archive
Expand-Archive -LiteralPath $archive -DestinationPath $expanded -Force

if (-not (Test-Path -LiteralPath (Join-Path $expanded "forcey.exe"))) {
    throw "The release archive does not contain forcey.exe at its root."
}

$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash
$encoding = [Text.UTF8Encoding]::new($false)

$versionManifest = @"
# yaml-language-server: `$schema=https://aka.ms/winget-manifest.version.1.12.0.schema.json

PackageIdentifier: $identifier
PackageVersion: $Version
DefaultLocale: en-US
ManifestType: version
ManifestVersion: 1.12.0
"@

$installerManifest = @"
# yaml-language-server: `$schema=https://aka.ms/winget-manifest.installer.1.12.0.schema.json

PackageIdentifier: $identifier
PackageVersion: $Version
InstallerLocale: en-US
InstallerType: zip
NestedInstallerType: portable
Scope: user
Commands:
- forcey
NestedInstallerFiles:
- RelativeFilePath: forcey.exe
  PortableCommandAlias: forcey
ArchiveBinariesDependOnPath: true
Installers:
- Architecture: x64
  InstallerUrl: $releaseUrl
  InstallerSha256: $hash
  MinimumOSVersion: 10.0.17763.0
  UpgradeBehavior: uninstallPrevious
ReleaseDate: $ReleaseDate
ManifestType: installer
ManifestVersion: 1.12.0
"@

$localeManifest = @"
# yaml-language-server: `$schema=https://aka.ms/winget-manifest.defaultLocale.1.12.0.schema.json

PackageIdentifier: $identifier
PackageVersion: $Version
PackageLocale: en-US
Publisher: okix
PublisherUrl: https://github.com/$owner
PublisherSupportUrl: $repositoryUrl/issues
PackageName: forcey
PackageUrl: $repositoryUrl
License: GNU Affero General Public License v3.0 only
LicenseUrl: $repositoryUrl/blob/v$Version/LICENSE
Copyright: Copyright (C) 2026 okix
ShortDescription: Progressively delete stubborn files and directories on Windows.
Description: forcey is a Windows command-line tool that permanently deletes stubborn files and directories while using only as much force as necessary. It escalates from normal deletion to attribute cleanup, elevation, permission repair, ownership changes, and finally locked-handle closure.
Moniker: forcey
Tags:
- cli
- delete
- filesystem
- locked-files
- windows
ReleaseNotes: Initial public release.
ReleaseNotesUrl: $repositoryUrl/releases/tag/v$Version
ManifestType: defaultLocale
ManifestVersion: 1.12.0
"@

[IO.File]::WriteAllText(
    (Join-Path $manifestDirectory "$identifier.yaml"),
    $versionManifest.TrimStart(),
    $encoding
)
[IO.File]::WriteAllText(
    (Join-Path $manifestDirectory "$identifier.installer.yaml"),
    $installerManifest.TrimStart(),
    $encoding
)
[IO.File]::WriteAllText(
    (Join-Path $manifestDirectory "$identifier.locale.en-US.yaml"),
    $localeManifest.TrimStart(),
    $encoding
)

Write-Host "Manifests created: $manifestDirectory"
Write-Host "Installer SHA256: $hash"

if (Get-Command winget -ErrorAction SilentlyContinue) {
    winget validate $manifestDirectory
    if ($LASTEXITCODE -ne 0) {
        throw "WinGet manifest validation failed."
    }
} else {
    Write-Warning "winget was not found; validate the manifest on a Windows machine before submission."
}

if ($Submit) {
    if (-not (Get-Command wingetcreate -ErrorAction SilentlyContinue)) {
        throw "wingetcreate is not installed. Run: winget install Microsoft.WingetCreate"
    }
    wingetcreate submit $manifestDirectory
    if ($LASTEXITCODE -ne 0) {
        throw "WinGet manifest submission failed."
    }
}
