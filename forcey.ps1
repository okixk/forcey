[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string]$TargetPath,

    [Alias("y")]
    [switch]$Yes,

    [Alias("n")]
    [switch]$DryRun,

    [switch]$AllowSystem,

    [Alias("h")]
    [switch]$Help
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

if (Get-Variable PSNativeCommandUseErrorActionPreference -ErrorAction SilentlyContinue) {
    $PSNativeCommandUseErrorActionPreference = $false
}

function Show-Usage {
    @"
forcey - forcibly delete a locked file or directory on Windows

Usage:
  forcey <path>
  forcey <path> -y
  forcey <path> -n
  forcey <path> -AllowSystem

Options:
  -y, -Yes          Skip the DELETE confirmation.
  -n, -DryRun       Show locking handles without closing or deleting anything.
  -AllowSystem      Permit targets under Windows, Program Files, or ProgramData.
  -h, -Help         Show this help.

Examples:
  forcey ".\Soundboard-Windows"
  forcey "C:\Users\oki\Downloads\stubborn-folder" -y
  forcey ".\locked-file.exe" -n

What it does:
  1. Finds open handles inside the target using Microsoft Sysinternals Handle.
  2. Forcibly closes those handles.
  3. Deletes the target.
"@
}

function Write-Info([string]$Message) {
    Write-Host "[forcey] $Message" -ForegroundColor Cyan
}

function Write-Warn([string]$Message) {
    Write-Host "[forcey] $Message" -ForegroundColor Yellow
}

function Write-Fail([string]$Message) {
    Write-Host "[forcey] $Message" -ForegroundColor Red
}

function Normalize-Path([string]$Path) {
    $full = [System.IO.Path]::GetFullPath($Path)

    if ($full.Length -gt 3) {
        return $full.TrimEnd("\")
    }

    return $full
}

function Test-PathInsideOrEqual([string]$Candidate, [string]$Parent) {
    $candidatePath = Normalize-Path $Candidate
    $parentPath = Normalize-Path $Parent

    return (
        $candidatePath.Equals($parentPath, [System.StringComparison]::OrdinalIgnoreCase) -or
        $candidatePath.StartsWith(
            $parentPath + "\",
            [System.StringComparison]::OrdinalIgnoreCase
        )
    )
}

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)

    return $principal.IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator
    )
}

function Restart-Elevated(
    [string]$ResolvedTarget,
    [bool]$SkipConfirmation,
    [bool]$PreviewOnly,
    [bool]$PermitSystem
) {
    $engine = (Get-Process -Id $PID).Path
    $script = $PSCommandPath.Replace("'", "''")
    $target = $ResolvedTarget.Replace("'", "''")

    $command = "& '$script' -TargetPath '$target'"

    if ($SkipConfirmation) {
        $command += " -Yes"
    }

    if ($PreviewOnly) {
        $command += " -DryRun"
    }

    if ($PermitSystem) {
        $command += " -AllowSystem"
    }

    $encoded = [Convert]::ToBase64String(
        [Text.Encoding]::Unicode.GetBytes($command)
    )

    Write-Info "Administrator rights are required; opening an elevated window."

    $process = Start-Process `
        -FilePath $engine `
        -Verb RunAs `
        -ArgumentList "-NoProfile -ExecutionPolicy Bypass -EncodedCommand $encoded" `
        -Wait `
        -PassThru

    exit $process.ExitCode
}

function Get-HandleExecutable {
    $cacheDirectory = Join-Path $env:LOCALAPPDATA "forcey\bin"
    $executableName = if ([Environment]::Is64BitOperatingSystem) {
        "handle64.exe"
    }
    else {
        "handle.exe"
    }

    $downloadUrl = "https://live.sysinternals.com/$executableName"
    $handleExecutable = Join-Path $cacheDirectory $executableName

    if (-not (Test-Path -LiteralPath $handleExecutable)) {
        New-Item -ItemType Directory -Path $cacheDirectory -Force | Out-Null
        Write-Info "Downloading Microsoft Sysinternals $executableName (first run only)."

        $temporaryFile = "$handleExecutable.download"
        Remove-Item -LiteralPath $temporaryFile -Force -ErrorAction SilentlyContinue

        Invoke-WebRequest -Uri $downloadUrl -OutFile $temporaryFile

        $signature = Get-AuthenticodeSignature -LiteralPath $temporaryFile

        if ($signature.Status -in @("HashMismatch", "NotSigned")) {
            Remove-Item -LiteralPath $temporaryFile -Force -ErrorAction SilentlyContinue
            throw "The downloaded Sysinternals executable failed signature validation."
        }

        Move-Item -LiteralPath $temporaryFile -Destination $handleExecutable -Force
    }

    return $handleExecutable
}

function Get-LockingHandles(
    [string]$HandleExecutable,
    [string]$ResolvedTarget
) {
    $rawOutput = & $HandleExecutable -accepteula -nobanner $ResolvedTarget 2>&1
    $results = [System.Collections.Generic.List[object]]::new()

    foreach ($entry in $rawOutput) {
        $line = [string]$entry

        if (
            $line -match
            '^(?<Process>.+?)\s+pid:\s*(?<ProcessId>\d+)\s+type:\s*(?<Type>\S+)\s+(?<Handle>[0-9A-Fa-f]+):\s*(?<Path>.+)$'
        ) {
            $lockedPath = $Matches.Path.Trim()

            try {
                $isRelevant = Test-PathInsideOrEqual $lockedPath $ResolvedTarget
            }
            catch {
                $isRelevant = $lockedPath.StartsWith(
                    $ResolvedTarget,
                    [System.StringComparison]::OrdinalIgnoreCase
                )
            }

            if ($isRelevant) {
                $results.Add(
                    [pscustomobject]@{
                        Process   = $Matches.Process.Trim()
                        ProcessId = [int]$Matches.ProcessId
                        Handle    = $Matches.Handle
                        Type      = $Matches.Type
                        Path      = $lockedPath
                    }
                )
            }
        }
    }

    return @(
        $results |
            Sort-Object ProcessId, Handle, Path -Unique
    )
}

function Remove-Target([string]$ResolvedTarget) {
    $item = Get-Item -LiteralPath $ResolvedTarget -Force

    if ($item.PSIsContainer) {
        & $env:ComSpec /d /c "rd /s /q `"$ResolvedTarget`""
    }
    else {
        & $env:ComSpec /d /c "del /f /q `"$ResolvedTarget`""
    }
}

if ($Help -or [string]::IsNullOrWhiteSpace($TargetPath)) {
    Show-Usage
    exit 0
}

$isWindowsPlatform = if (Get-Variable IsWindows -ErrorAction SilentlyContinue) {
    $IsWindows
}
else {
    $env:OS -eq "Windows_NT"
}

if (-not $isWindowsPlatform) {
    Write-Fail "forcey is intended for Windows."
    exit 1
}

try {
    $expandedTarget = [Environment]::ExpandEnvironmentVariables($TargetPath)
    $resolvedTarget = Normalize-Path $expandedTarget
}
catch {
    Write-Fail "Invalid path: $TargetPath"
    exit 2
}

if (-not (Test-Path -LiteralPath $resolvedTarget)) {
    Write-Fail "Target does not exist: $resolvedTarget"
    exit 3
}

$driveRoot = Normalize-Path ([System.IO.Path]::GetPathRoot($resolvedTarget))
$userProfilesRoot = Normalize-Path (Split-Path -Parent $env:USERPROFILE)

$alwaysProtected = @(
    $driveRoot,
    $userProfilesRoot,
    (Normalize-Path $env:USERPROFILE)
) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }

foreach ($protectedPath in $alwaysProtected) {
    if ($resolvedTarget.Equals(
        $protectedPath,
        [System.StringComparison]::OrdinalIgnoreCase
    )) {
        Write-Fail "Refusing to delete protected path: $resolvedTarget"
        exit 4
    }
}

if (Test-PathInsideOrEqual $PSCommandPath $resolvedTarget) {
    Write-Fail "Refusing to delete forcey's own installation while it is running."
    exit 4
}

$systemParents = @(
    $env:SystemRoot,
    $env:ProgramFiles,
    ${env:ProgramFiles(x86)},
    $env:ProgramData
) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }

if (-not $AllowSystem) {
    foreach ($systemParent in $systemParents) {
        if (Test-PathInsideOrEqual $resolvedTarget $systemParent) {
            Write-Fail "The target is inside a protected system location."
            Write-Fail "Use -AllowSystem only when you are absolutely certain."
            exit 4
        }
    }
}

if (-not (Test-IsAdministrator)) {
    Restart-Elevated `
        -ResolvedTarget $resolvedTarget `
        -SkipConfirmation $Yes.IsPresent `
        -PreviewOnly $DryRun.IsPresent `
        -PermitSystem $AllowSystem.IsPresent
}

# Avoid holding the target through this process's current working directory.
Set-Location "$env:SystemDrive\"

try {
    $handleExecutable = Get-HandleExecutable
}
catch {
    Write-Fail $_.Exception.Message
    exit 5
}

Write-Info "Target: $resolvedTarget"
$locks = @(Get-LockingHandles $handleExecutable $resolvedTarget)

if ($locks.Count -eq 0) {
    Write-Info "No matching open handles were found."
}
else {
    Write-Warn "Found $($locks.Count) locking handle(s):"
    $locks |
        Select-Object Process, ProcessId, Handle, Type, Path |
        Format-Table -AutoSize |
        Out-Host
}

if ($DryRun) {
    Write-Info "Dry run complete. Nothing was changed."
    exit 0
}

$criticalProcesses = @(
    "System",
    "Registry",
    "smss.exe",
    "csrss.exe",
    "wininit.exe",
    "services.exe",
    "lsass.exe"
)

$criticalLocks = @(
    $locks |
        Where-Object { $_.Process -in $criticalProcesses }
)

if ($criticalLocks.Count -gt 0 -and -not $AllowSystem) {
    Write-Fail "A critical Windows process holds part of the target."
    Write-Fail "Run with -AllowSystem only if you accept possible system instability."
    exit 6
}

if (-not $Yes) {
    Write-Warn "This permanently deletes the target and can crash processes using it."
    $confirmation = Read-Host "Type DELETE to continue"

    if ($confirmation -cne "DELETE") {
        Write-Info "Cancelled."
        exit 0
    }
}

foreach ($lock in $locks) {
    Write-Warn (
        "Closing handle {0} in {1} (PID {2})" -f
        $lock.Handle,
        $lock.Process,
        $lock.ProcessId
    )

    & $handleExecutable `
        -accepteula `
        -nobanner `
        -c $lock.Handle `
        -p $lock.ProcessId `
        -y |
        Out-Host
}

Start-Sleep -Milliseconds 200

try {
    Remove-Target $resolvedTarget
}
catch {
    Write-Warn "The first deletion attempt failed: $($_.Exception.Message)"
}

if (Test-Path -LiteralPath $resolvedTarget) {
    # One quick retry catches handles that disappeared just after being closed.
    Start-Sleep -Milliseconds 300

    try {
        Remove-Target $resolvedTarget
    }
    catch {
        # Final verification below reports the useful result.
    }
}

if (Test-Path -LiteralPath $resolvedTarget) {
    Write-Fail "Deletion failed; the target still exists."

    $remainingLocks = @(Get-LockingHandles $handleExecutable $resolvedTarget)

    if ($remainingLocks.Count -gt 0) {
        Write-Warn "Remaining handles:"
        $remainingLocks |
            Select-Object Process, ProcessId, Handle, Type, Path |
            Format-Table -AutoSize |
            Out-Host
    }

    exit 7
}

Write-Host ""
Write-Host "✨ forcey deleted it. Brutally, but cutely." -ForegroundColor Magenta
exit 0
