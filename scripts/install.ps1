<#
Install the re-psn server (or, with -App, the desktop app) on Windows (amd64) from a GitHub release of undecuple/re-psn, for the current user only.
Written for Windows PowerShell 5.1 and PowerShell 7; tested on PowerShell 7 only. No administrator rights needed.

usage: powershell -ExecutionPolicy Bypass -File install.ps1 [-Version vX.Y.Z] [-Dir DIR] [-AddToPath] [-App] [-DryRun] [-Help]

What it does, in order:
  1. Checks the system: Windows on amd64. There is no Windows ARM64 build; on Linux use install.sh, and macOS has
     no builds at all.
  2. Picks the release: -Version or RE_PSN_VERSION, else the newest release that is not a draft, pre-releases
     included (every release is a pre-release for now). That one lookup reads
     https://api.github.com/repos/undecuple/re-psn/releases; with a pinned version it is skipped.
  3. Downloads re-psn-vX.Y.Z-windows-amd64.zip (with -App, re-psn-app-vX.Y.Z-windows-amd64.zip, which releases
     after v0.4.0 carry) and SHA256SUMS from
     https://github.com/undecuple/re-psn/releases/download/vX.Y.Z/ into a temporary folder, over HTTPS with
     TLS 1.2 or newer. No other host is named here; GitHub redirects the download to its own file servers.
  4. Checks the zip with Get-FileHash against its line in SHA256SUMS. A missing line or a different hash stops it
     before anything is written outside the temporary folder.
  5. Installs re-psn.exe into DIR (default $env:LOCALAPPDATA\re-psn\bin): copied to a temporary name in DIR, then
     moved over the old file in one step. The zip's LICENSE, NOTICE.md and examples\ go to
     $env:LOCALAPPDATA\re-psn\vX.Y.Z\ unless that folder is already there. With -App it installs re-psn-app.exe
     and copies LICENSE, NOTICE.md and README.md to $env:LOCALAPPDATA\re-psn-app\vX.Y.Z\ instead.
  6. Says what it did and how to start the server. It changes PATH only with -AddToPath, and then only the
     user PATH; otherwise it prints the command that would add DIR.

-DryRun prints these steps with the URLs and paths and then stops: no lookup, no download, nothing written.
scripts/test-install.ps1 dot-sources this file (which then defines the functions without running them) and
replaces the platform check, the download functions and the user PATH functions with ones that work on a fake
release and a fake PATH in a temporary folder.
#>
[CmdletBinding()]
param(
    [string]$Version = $env:RE_PSN_VERSION,
    [string]$Dir = '',
    [switch]$AddToPath,
    [switch]$App,
    [switch]$DryRun,
    [switch]$Help
)

Set-StrictMode -Version 3
$ErrorActionPreference = 'Stop'

$Repo = 'undecuple/re-psn'
$Api = "https://api.github.com/repos/$Repo/releases?per_page=20"
$Dl = "https://github.com/$Repo/releases/download"

function Show-Usage {
    @'
usage: install.ps1 [-Version vX.Y.Z] [-Dir DIR] [-AddToPath] [-App] [-DryRun] [-Help]

Installs the re-psn server for the current user from a GitHub release (Windows amd64).

  -Version vX.Y.Z  install this release (or set RE_PSN_VERSION); default: the newest release,
                   pre-releases included
  -Dir DIR         install into DIR; default: $env:LOCALAPPDATA\re-psn\bin
  -AddToPath       add DIR to the user PATH (nothing else changes PATH)
  -App             install the desktop app (re-psn-app.exe) instead of the server; needs a release after v0.4.0
  -DryRun          print the steps and stop; download and write nothing
  -Help            show this help

The download is checked against SHA256SUMS before anything is installed. No administrator rights needed.
'@
}

function Test-Platform {
    # $IsWindows exists from PowerShell 6; Windows PowerShell 5.1 (Desktop) runs only on Windows.
    $onWindows = ($PSVersionTable.PSEdition -eq 'Desktop') -or ((Test-Path variable:IsWindows) -and $IsWindows)
    if (-not $onWindows) {
        throw 'this script is for Windows. On Linux use install.sh; macOS is not supported (re-psn has no macOS builds).'
    }
    $cpu = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $cpu = $env:PROCESSOR_ARCHITEW6432 }  # 32-bit PowerShell on 64-bit Windows
    if ($cpu -ne 'AMD64') {
        throw "CPU $cpu is not supported: the Windows build is for amd64 (x64) only."
    }
}

function Get-Tag([string]$v) {
    if (-not $v) { return '' }
    if (-not $v.StartsWith('v')) { $v = "v$v" }
    if ($v -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') { throw "version '$v' is not vMAJOR.MINOR.PATCH" }
    return $v
}

function Enable-Tls12 {
    # Windows PowerShell 5.1 may default to TLS 1.0; PowerShell 7 already uses the system's TLS 1.2 or newer.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
}

function Get-Https([string]$url, [string]$out) {
    if (-not $url.StartsWith('https://')) { throw "refusing a URL that is not https: $url" }
    $ProgressPreference = 'SilentlyContinue'
    Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $out
}

function Get-ReleaseList {
    $ProgressPreference = 'SilentlyContinue'
    return Invoke-RestMethod -UseBasicParsing -Uri $Api -Headers @{ Accept = 'application/vnd.github+json' }
}

function Select-Newest($releases) {
    foreach ($r in @($releases)) {
        if (-not $r.draft) { return (Get-Tag ([string]$r.tag_name)) }
    }
    throw "no published release found at $Api"
}

function Get-UserPath { return [Environment]::GetEnvironmentVariable('Path', 'User') }
function Set-UserPath([string]$value) { [Environment]::SetEnvironmentVariable('Path', $value, 'User') }

function Get-ExpectedHash([string]$sumsFile, [string]$name) {
    $found = @()
    foreach ($line in Get-Content -LiteralPath $sumsFile) {
        $f = $line.Trim() -split '\s+', 2
        if ($f.Count -eq 2 -and ($f[1] -eq $name -or $f[1] -eq "*$name")) { $found += $f[0] }
    }
    if ($found.Count -eq 0) { throw "SHA256SUMS has no line for $name; nothing was installed." }
    if ($found.Count -gt 1 -or $found[0] -notmatch '^[0-9a-fA-F]{64}$') {
        throw "SHA256SUMS has more than one line, or a malformed one, for $name; nothing was installed."
    }
    return $found[0].ToLowerInvariant()
}

function Install-RePsn([string]$Version, [string]$Dir, [bool]$AddToPath, [bool]$DryRun, [bool]$App = $false) {
    Test-Platform
    $pkg = if ($App) { 're-psn-app' } else { 're-psn' }
    $tag = Get-Tag $Version
    if (-not $env:LOCALAPPDATA) { throw 'LOCALAPPDATA is not set.' }
    $base = Join-Path $env:LOCALAPPDATA $pkg
    if (-not $Dir) { $Dir = Join-Path (Join-Path $env:LOCALAPPDATA 're-psn') 'bin' }
    $Dir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Dir).TrimEnd('\', '/')
    $userPath = Get-UserPath
    $onPath = @("$userPath;$env:Path" -split ';' | ForEach-Object { $_.TrimEnd('\', '/') }) -contains $Dir

    if ($DryRun) {
        $v = if ($tag) { $tag } else { '<newest release>' }
        $zip = "$pkg-$v-windows-amd64.zip"
        Write-Output 'Dry run: nothing is downloaded or written.'
        Write-Output '  system:   Windows amd64'
        if ($tag) { Write-Output "  release:  $tag (pinned)" } else { Write-Output "  release:  the newest non-draft release, read from $Api" }
        Write-Output "  download: $Dl/$v/$zip"
        Write-Output "            $Dl/$v/SHA256SUMS"
        Write-Output "  check:    Get-FileHash of $zip against its line in SHA256SUMS; stop on a mismatch or no line"
        Write-Output "  install:  $(Join-Path $Dir "$pkg.exe") (moved into place)"
        if ($App) { Write-Output "  extras:   $(Join-Path $base $v) (LICENSE, NOTICE.md, README.md)" }
        else { Write-Output "  examples: $(Join-Path $base "$v\examples")" }
        if (-not $onPath) {
            if ($AddToPath) { Write-Output "  PATH:     $Dir would be added to the user PATH" }
            else { Write-Output "  PATH:     $Dir is not on PATH; the command to add it would be printed" }
        }
        return
    }

    Enable-Tls12
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('re-psn-install-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    $part = ''
    try {
        if (-not $tag) {
            $tag = Select-Newest (Get-ReleaseList)
            Write-Output "Newest release: $tag"
        } else {
            Write-Output "Release: $tag"
        }
        $zip = "$pkg-$tag-windows-amd64.zip"
        Write-Output "Downloading $Dl/$tag/$zip"
        $sums = Join-Path $tmp 'SHA256SUMS'
        $zipPath = Join-Path $tmp $zip
        try { Get-Https "$Dl/$tag/SHA256SUMS" $sums } catch { throw "could not download SHA256SUMS for ${tag}: $($_.Exception.Message)" }
        try { Get-Https "$Dl/$tag/$zip" $zipPath } catch {
            $hint = if ($App) { ' (the app archives start after v0.4.0)' } else { '' }
            throw "could not download ${zip}${hint}: $($_.Exception.Message)"
        }

        $want = Get-ExpectedHash $sums $zip
        $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $zipPath).Hash.ToLowerInvariant()
        if ($got -ne $want) { throw "checksum mismatch for $zip (expected $want, got $got); nothing was installed." }
        Write-Output "Checksum OK: $want"

        $x = Join-Path $tmp 'x'
        Expand-Archive -LiteralPath $zipPath -DestinationPath $x
        $extras = Join-Path $x "$pkg-$tag-windows-amd64"
        $src = Join-Path $extras "$pkg.exe"
        if (-not (Test-Path -LiteralPath $src -PathType Leaf)) { throw "the zip has no $pkg.exe where expected; nothing was installed." }

        # A temporary name in the same folder, then one move over the old file.
        New-Item -ItemType Directory -Force -Path $Dir | Out-Null
        $target = Join-Path $Dir "$pkg.exe"
        $part = Join-Path $Dir ".$pkg.exe.part.$PID"
        Copy-Item -LiteralPath $src -Destination $part
        if (Test-Path -LiteralPath $target) {
            try { [IO.File]::Replace($part, $target, [NullString]::Value) }
            catch { throw "could not replace $target (stop a running $pkg first): $($_.Exception.Message)" }
        } else {
            [IO.File]::Move($part, $target)
        }
        $part = ''
        Write-Output "Installed $target"

        $shared = Join-Path $base $tag
        if (Test-Path -LiteralPath $shared) {
            Write-Output "Kept $shared as it was (the files for $tag are already there)"
        } else {
            $stage = Join-Path $tmp 'share'
            New-Item -ItemType Directory -Path $stage | Out-Null
            $files = if ($App) { 'LICENSE', 'NOTICE.md', 'README.md' } else { 'LICENSE', 'NOTICE.md', 'examples' }
            foreach ($f in $files) {
                $p = Join-Path $extras $f
                if (Test-Path -LiteralPath $p) { Copy-Item -LiteralPath $p -Destination $stage -Recurse }
            }
            New-Item -ItemType Directory -Force -Path $base | Out-Null
            Move-Item -LiteralPath $stage -Destination $shared
            if ($App) { Write-Output "Copied LICENSE, NOTICE.md and README.md to $shared" }
            else { Write-Output "Copied LICENSE, NOTICE.md and examples\ to $shared" }
        }

        Write-Output ''
        $what = if ($App) { 'Start the app:          ' } else { 'Start the server:       ' }
        if ($onPath) {
            Write-Output "$what$pkg"
        } elseif ($AddToPath) {
            $new = if ($userPath) { "$($userPath.TrimEnd(';'));$Dir" } else { $Dir }
            Set-UserPath $new
            $env:Path = "$env:Path;$Dir"
            Write-Output "Added $Dir to the user PATH (new terminals see it)."
            Write-Output "$what$pkg"
        } else {
            Write-Output "$Dir is not on your PATH. To add it to the user PATH, run this once (or rerun with -AddToPath):"
            Write-Output "  [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';$Dir', 'User')"
            Write-Output "$what& '$target'"
        }
        if ($App) {
            Write-Output 'The app is a terminal program; run it in a terminal (Windows Terminal or PowerShell).'
            Write-Output "Its README:              $(Join-Path $shared 'README.md')"
            return
        }
        Write-Output 'With the status page:   add -http 127.0.0.1:36101 and open http://127.0.0.1:36101/'
        Write-Output 'All flags:              re-psn -h'
        Write-Output "Examples (settings file, service, proxies): $(Join-Path $shared 'examples')"
    } finally {
        if ($part -and (Test-Path -LiteralPath $part)) { Remove-Item -LiteralPath $part -Force }
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

# Dot-sourced (the tests do this): define the functions only.
if ($MyInvocation.InvocationName -eq '.') { return }

if ($Help) { Show-Usage; exit 0 }
try {
    Install-RePsn -Version $Version -Dir $Dir -AddToPath $AddToPath.IsPresent -DryRun $DryRun.IsPresent -App $App.IsPresent
} catch {
    [Console]::Error.WriteLine("re-psn install: $($_.Exception.Message)")
    exit 1
}
