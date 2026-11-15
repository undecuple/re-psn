<#
Tests for scripts/install.ps1, with no network and no change to the real PATH. It dot-sources install.ps1, which then
only defines its functions, and replaces Test-Platform, Get-Https, Get-ReleaseList, Get-UserPath and Set-UserPath:
downloads come from a fake release in a temporary folder (any URL outside the release download path fails the test),
and the user PATH is a variable. LOCALAPPDATA points into the temporary folder. Written for PowerShell 5.1 and 7 and run on 7 only, also
on Linux (the real platform check is then tested to refuse).

usage: pwsh -NoProfile -File scripts/test-install.ps1
#>
Set-StrictMode -Version 3
$ErrorActionPreference = 'Stop'

. (Join-Path $PSScriptRoot 'install.ps1')

$script:pass = 0
$script:fail = 0
function Check([string]$name, [bool]$cond) {
    if ($cond) { $script:pass++; Write-Output "ok   $name" } else { $script:fail++; Write-Output "FAIL $name" }
}
function Fails([scriptblock]$block, [string]$text) {
    try { & $block | Out-Null; return $false } catch { return $_.Exception.Message -like "*$text*" }
}

$T = Join-Path ([IO.Path]::GetTempPath()) ('re-psn-test-' + [Guid]::NewGuid().ToString('N'))
$F = Join-Path $T 'fake'
New-Item -ItemType Directory -Path $F | Out-Null
$realLocal = $env:LOCALAPPDATA
try {
    # The fake release: v9.9.9 is a draft; v0.4.0 is the newest published one; v0.5.0 has a wrong hash; v0.6.0 no line.
    foreach ($tag in 'v0.4.0', 'v0.5.0', 'v0.6.0') {
        $n = "re-psn-$tag-windows-amd64"
        $b = Join-Path $T "build\$tag\$n"
        New-Item -ItemType Directory -Path (Join-Path $b 'examples') | Out-Null
        Set-Content -LiteralPath (Join-Path $b 're-psn.exe') -Value "exe $tag"
        Set-Content -LiteralPath (Join-Path $b 'LICENSE') -Value 'license'
        Set-Content -LiteralPath (Join-Path $b 'NOTICE.md') -Value 'notice'
        Set-Content -LiteralPath (Join-Path $b 'examples\re-psn.env.example') -Value 'RE_PSN_HTTP='
        New-Item -ItemType Directory -Path (Join-Path $F $tag) | Out-Null
        $zip = Join-Path $F "$tag\$n.zip"
        Compress-Archive -Path $b -DestinationPath $zip
        $h = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLowerInvariant()
        if ($tag -eq 'v0.5.0') { $h = '0' * 64 }
        $lines = @("$('1' * 64)  re-psn-$tag-linux-amd64.tar.gz")
        if ($tag -ne 'v0.6.0') { $lines += "$h  $n.zip" }
        [IO.File]::WriteAllText((Join-Path $F "$tag\SHA256SUMS"), (($lines -join "`n") + "`n"))
    }
    # v0.8.0 carries the app archive (the app archives start after v0.4.0).
    $an = 're-psn-app-v0.8.0-windows-amd64'
    $ab = Join-Path $T "build\app\$an"
    New-Item -ItemType Directory -Path $ab | Out-Null
    Set-Content -LiteralPath (Join-Path $ab 're-psn-app.exe') -Value 'app v0.8.0'
    $hostFixture = Join-Path $T 'synthetic-server-v0.8.0.exe'
    [IO.File]::WriteAllBytes($hostFixture, [Text.Encoding]::UTF8.GetBytes("synthetic server bytes v0.8.0`n"))
    Copy-Item -LiteralPath $hostFixture -Destination (Join-Path $ab 're-psn-host.exe')
    Set-Content -LiteralPath (Join-Path $ab 'LICENSE') -Value 'license'
    Set-Content -LiteralPath (Join-Path $ab 'NOTICE.md') -Value 'notice'
    Set-Content -LiteralPath (Join-Path $ab 'README.md') -Value 'app readme'
    New-Item -ItemType Directory -Path (Join-Path $F 'v0.8.0') | Out-Null
    $azip = Join-Path $F "v0.8.0\$an.zip"
    Compress-Archive -Path $ab -DestinationPath $azip
    $ah = (Get-FileHash -Algorithm SHA256 -LiteralPath $azip).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText((Join-Path $F 'v0.8.0\SHA256SUMS'), "$ah  $an.zip`n")
    # A checksum-valid old/incomplete app payload, missing only the host component.
    $missingName = 're-psn-app-v0.9.0-windows-amd64'
    $missingDir = Join-Path $T "build\missing\$missingName"
    New-Item -ItemType Directory -Path $missingDir | Out-Null
    foreach ($name in 're-psn-app.exe', 'LICENSE', 'NOTICE.md', 'README.md') {
        Copy-Item -LiteralPath (Join-Path $ab $name) -Destination $missingDir
    }
    New-Item -ItemType Directory -Path (Join-Path $F 'v0.9.0') | Out-Null
    $missingZip = Join-Path $F "v0.9.0\$missingName.zip"
    Compress-Archive -Path $missingDir -DestinationPath $missingZip
    $missingHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $missingZip).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText((Join-Path $F 'v0.9.0\SHA256SUMS'), "$missingHash  $missingName.zip`n")

    # An app checksum failure must preserve an already installed pair as well.
    New-Item -ItemType Directory -Path (Join-Path $F 'v0.10.0') | Out-Null
    $badName = 're-psn-app-v0.10.0-windows-amd64.zip'
    Copy-Item -LiteralPath $azip -Destination (Join-Path $F "v0.10.0\$badName")
    [IO.File]::WriteAllText((Join-Path $F 'v0.10.0\SHA256SUMS'), "$('0' * 64)  $badName`n")
    $script:releases = '[{"tag_name":"v9.9.9","draft":true},{"tag_name":"v0.4.0","draft":false,"prerelease":true},{"tag_name":"v0.3.0","draft":false}]' | ConvertFrom-Json
    $script:calls = @()
    $script:userPathValue = 'C:\Other'

    function Test-Platform { }
    function Get-ReleaseList { $script:calls += 'list'; return $script:releases }
    function Get-UserPath { return $script:userPathValue }
    function Set-UserPath([string]$value) { $script:userPathValue = $value }
    function Get-Https([string]$url, [string]$out) {
        $script:calls += $url
        $prefix = 'https://github.com/undecuple/re-psn/releases/download/'
        if (-not $url.StartsWith($prefix)) { throw "unexpected URL $url" }
        $src = Join-Path $F ($url.Substring($prefix.Length))
        if (-not (Test-Path -LiteralPath $src)) { throw "404 $url" }
        Copy-Item -LiteralPath $src -Destination $out
    }
    function Reset {
        $script:calls = @()
        $script:userPathValue = 'C:\Other'
        $local = Join-Path $T 'local'
        if (Test-Path -LiteralPath $local) { Remove-Item -LiteralPath $local -Recurse -Force }
        New-Item -ItemType Directory -Path $local | Out-Null
        $env:LOCALAPPDATA = $local
        return $local
    }
    function Empty([string]$p) { return -not (Get-ChildItem -LiteralPath $p -Force) }

    $local = Reset
    $bin = Join-Path $local 're-psn\bin'
    $out = Install-RePsn -Version '' -Dir '' -AddToPath $false -DryRun $false
    Check 'newest non-draft release installed' ((Get-Content -LiteralPath (Join-Path $bin 're-psn.exe')) -eq 'exe v0.4.0')
    Check 'says which release' (@($out) -contains 'Newest release: v0.4.0')
    Check 'only the list and release URLs fetched' (($script:calls -join ' ') -eq 'list https://github.com/undecuple/re-psn/releases/download/v0.4.0/SHA256SUMS https://github.com/undecuple/re-psn/releases/download/v0.4.0/re-psn-v0.4.0-windows-amd64.zip')
    Check 'examples copied' (Test-Path -LiteralPath (Join-Path $local 're-psn\v0.4.0\examples\re-psn.env.example'))
    Check 'no temporary file left' (@(Get-ChildItem -LiteralPath $bin -Force).Count -eq 1)
    Check 'PATH not changed without -AddToPath' ($script:userPathValue -eq 'C:\Other')
    Check 'prints the PATH command' ((@($out) -join "`n") -like "*SetEnvironmentVariable('Path'*;$bin', 'User')*")

    Set-Content -LiteralPath (Join-Path $bin 're-psn.exe') -Value 'old'
    $script:calls = @()
    $out = Install-RePsn -Version '0.4.0' -Dir '' -AddToPath $true -DryRun $false
    Check 'pinned version skips the release list' (-not ($script:calls -contains 'list'))
    Check 'existing exe replaced' ((Get-Content -LiteralPath (Join-Path $bin 're-psn.exe')) -eq 'exe v0.4.0' -and @(Get-ChildItem -LiteralPath $bin -Force).Count -eq 1)
    Check '-AddToPath appends to the user PATH only' ($script:userPathValue -eq "C:\Other;$bin")
    Check 'examples folder kept on a second install' (@(@($out) -like 'Kept *').Count -eq 1)

    $local = Reset
    Check 'checksum mismatch refused' (Fails { Install-RePsn -Version 'v0.5.0' -Dir '' -AddToPath $false -DryRun $false } 'checksum mismatch')
    Check 'checksum mismatch: nothing installed' (Empty $local)
    Check 'missing SHA256SUMS line refused' (Fails { Install-RePsn -Version 'v0.6.0' -Dir '' -AddToPath $false -DryRun $false } 'no line for re-psn-v0.6.0-windows-amd64.zip')
    Check 'missing line: nothing installed' (Empty $local)
    Check 'bad version refused' (Fails { Install-RePsn -Version 'v1.2' -Dir '' -AddToPath $false -DryRun $false } 'not vMAJOR.MINOR.PATCH')

    $local = Reset
    $out = Install-RePsn -Version '' -Dir (Join-Path $local 'custom') -AddToPath $true -DryRun $true
    Check 'dry run: no download, nothing written' ($script:calls.Count -eq 0 -and (Empty $local) -and $script:userPathValue -eq 'C:\Other')
    Check 'dry run: prints the install path' ((@($out) -join "`n") -like "*$(Join-Path $local 'custom')*re-psn.exe*")

    $local = Reset
    $null = Install-RePsn -Version '' -Dir (Join-Path $local 'custom') -AddToPath $false -DryRun $false
    Check '-Dir: installed there' (Test-Path -LiteralPath (Join-Path $local 'custom\re-psn.exe'))

    # -App: the desktop app instead of the server.
    $local = Reset
    $out = Install-RePsn -Version 'v0.8.0' -Dir '' -AddToPath $false -DryRun $false -App $true
    Check '-App: installs re-psn-app.exe, not the server' ((Get-Content -LiteralPath (Join-Path $bin 're-psn-app.exe')) -eq 'app v0.8.0' -and -not (Test-Path -LiteralPath (Join-Path $bin 're-psn.exe')))
    Check '-App: only the app zip and SHA256SUMS fetched' (($script:calls -join ' ') -eq 'https://github.com/undecuple/re-psn/releases/download/v0.8.0/SHA256SUMS https://github.com/undecuple/re-psn/releases/download/v0.8.0/re-psn-app-v0.8.0-windows-amd64.zip')
    Check '-App: README.md and NOTICE.md copied' ((Test-Path -LiteralPath (Join-Path $local 're-psn-app\v0.8.0\README.md')) -and (Test-Path -LiteralPath (Join-Path $local 're-psn-app\v0.8.0\NOTICE.md')))
    Check '-App: no server folder created' (-not (Test-Path -LiteralPath (Join-Path $local 're-psn\v0.8.0')))
    Check '-App: host component installed byte for byte' ((Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $bin 're-psn-host.exe')).Hash -eq (Get-FileHash -Algorithm SHA256 -LiteralPath $hostFixture).Hash)
    Check '-App: no temporary file left' (@(Get-ChildItem -LiteralPath $bin -Force).Count -eq 2)
    Check '-App: says how to start it, no server hints' ((@($out) -join "`n") -like '*Start the app:*' -and (@($out) -join "`n") -notlike '*status page*')
    $local = Reset
    Check '-App on a release without app archives refused' (Fails { Install-RePsn -Version 'v0.4.0' -Dir '' -AddToPath $false -DryRun $false -App $true } 'start after v0.4.0')
    Check '-App on an old release: nothing installed' (Empty $local)
    $local = Reset
    $out = Install-RePsn -Version 'v0.8.0' -Dir '' -AddToPath $false -DryRun $true -App $true
    Check '-App dry run: no download, nothing written' ($script:calls.Count -eq 0 -and (Empty $local))
    Check '-App dry run: app zip URL and path' ((@($out) -join "`n") -like '*re-psn-app-v0.8.0-windows-amd64.zip*re-psn-app.exe*')
    Check '-App dry run: host path and honest pair limit' ((@($out) -join "`n") -like '*re-psn-host.exe*not crash-atomic*')

    $local = Reset
    New-Item -ItemType Directory -Path $bin | Out-Null
    Set-Content -LiteralPath (Join-Path $bin 're-psn-app.exe') -Value 'old app'
    Set-Content -LiteralPath (Join-Path $bin 're-psn-host.exe') -Value 'old host'
    Set-Content -LiteralPath (Join-Path $bin 're-psn.exe') -Value 'standalone stays'
    Check '-App missing host refused before replacement' (Fails { Install-RePsn -Version 'v0.9.0' -Dir '' -AddToPath $false -DryRun $false -App $true } 'no matching re-psn-host.exe')
    Check '-App missing host: both old components preserved' ((Get-Content -LiteralPath (Join-Path $bin 're-psn-app.exe')) -eq 'old app' -and (Get-Content -LiteralPath (Join-Path $bin 're-psn-host.exe')) -eq 'old host')
    Check '-App missing host: standalone preserved' ((Get-Content -LiteralPath (Join-Path $bin 're-psn.exe')) -eq 'standalone stays')
    Check '-App checksum mismatch refused' (Fails { Install-RePsn -Version 'v0.10.0' -Dir '' -AddToPath $false -DryRun $false -App $true } 'checksum mismatch')
    Check '-App checksum mismatch: old pair preserved' ((Get-Content -LiteralPath (Join-Path $bin 're-psn-app.exe')) -eq 'old app' -and (Get-Content -LiteralPath (Join-Path $bin 're-psn-host.exe')) -eq 'old host')


    $lockPath = Join-Path $bin '.re-psn-app.install.lock'
    Set-Content -LiteralPath $lockPath -Value 'other owner'
    Check '-App existing update lock refused' (Fails { Install-RePsn -Version 'v0.8.0' -Dir '' -AddToPath $false -DryRun $false -App $true } 'update lock exists')
    Check '-App other update lock retained' ((Get-Content -LiteralPath $lockPath) -eq 'other owner')
    Remove-Item -LiteralPath $lockPath -Force

    $held = [IO.File]::Open((Join-Path $bin 're-psn-app.exe'), [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
    try {
        Check '-App locked file refused before either replacement' (Fails { Install-RePsn -Version 'v0.8.0' -Dir '' -AddToPath $false -DryRun $false -App $true } 'locked or unwritable')
    } finally { $held.Dispose() }
    Check '-App locked file: old pair unchanged' ((Get-Content -LiteralPath (Join-Path $bin 're-psn-app.exe')) -eq 'old app' -and (Get-Content -LiteralPath (Join-Path $bin 're-psn-host.exe')) -eq 'old host')

    # Inject a failure after the host replacement, to prove pair rollback.
    $script:realMove = ${function:Move-InstallPayload}
    function Move-InstallPayload([string]$Source, [string]$Target) {
        if ([IO.Path]::GetFileName($Source) -eq 'new-re-psn-app.exe') { throw 'synthetic second replacement failure' }
        & $script:realMove $Source $Target
    }
    try {
        Check '-App second replacement failure is reported' (Fails { Install-RePsn -Version 'v0.8.0' -Dir '' -AddToPath $false -DryRun $false -App $true } 'synthetic second replacement failure')
    } finally { Set-Item function:Move-InstallPayload -Value $script:realMove }
    Check '-App second replacement failure: old bytes restored' ((Get-Content -LiteralPath (Join-Path $bin 're-psn-app.exe')) -eq 'old app' -and (Get-Content -LiteralPath (Join-Path $bin 're-psn-host.exe')) -eq 'old host')
    Check '-App rollback leaves no staging files' (@(Get-ChildItem -LiteralPath $bin -Force).Count -eq 3)
    function Move-InstallPayload([string]$Source, [string]$Target) {
        if ([IO.Path]::GetFileName($Source) -in @('new-re-psn-app.exe', 'old-re-psn-host.exe')) { throw 'synthetic replacement/rollback failure' }
        & $script:realMove $Source $Target
    }
    try {
        Check '-App rollback failure explicitly reported' (Fails { Install-RePsn -Version 'v0.8.0' -Dir '' -AddToPath $false -DryRun $false -App $true } 'rollback incomplete')
    } finally { Set-Item function:Move-InstallPayload -Value $script:realMove }
    $recovery = @(Get-ChildItem -LiteralPath $bin -Force -Directory -Filter '.re-psn-app.update.*')
    Check '-App failed rollback keeps recovery and lock' ($recovery.Count -eq 1 -and (Test-Path -LiteralPath $lockPath))
    Check '-App failed rollback keeps original host bytes' ((Get-Content -LiteralPath (Join-Path $recovery[0].FullName 'old-re-psn-host.exe')) -eq 'old host')
    Move-InstallPayload (Join-Path $recovery[0].FullName 'old-re-psn-host.exe') (Join-Path $bin 're-psn-host.exe')
    Remove-Item -LiteralPath $recovery[0].FullName -Recurse -Force
    Remove-Item -LiteralPath $lockPath -Force

    $null = Install-RePsn -Version 'v0.8.0' -Dir '' -AddToPath $false -DryRun $false -App $true
    Check '-App valid update: host byte identity' ((Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $bin 're-psn-host.exe')).Hash -eq (Get-FileHash -Algorithm SHA256 -LiteralPath $hostFixture).Hash)
    Check '-App valid update: standalone not overwritten' ((Get-Content -LiteralPath (Join-Path $bin 're-psn.exe')) -eq 'standalone stays')

    Remove-Item -LiteralPath (Join-Path $bin 're-psn-app.exe')
    New-Item -ItemType Directory -Path (Join-Path $bin 're-psn-app.exe') | Out-Null
    Check '-App directory target refused' (Fails { Install-RePsn -Version 'v0.8.0' -Dir '' -AddToPath $false -DryRun $false -App $true } 'regular non-link file')


    $onWindows = ($PSVersionTable.PSEdition -eq 'Desktop') -or ((Test-Path variable:IsWindows) -and $IsWindows)
    if (-not $onWindows) {
        . (Join-Path $PSScriptRoot 'install.ps1')   # the real Test-Platform again
        Check 'not Windows: refused' (Fails { Test-Platform } 'macOS is not supported')
    }
} finally {
    $env:LOCALAPPDATA = $realLocal
    Remove-Item -LiteralPath $T -Recurse -Force -ErrorAction SilentlyContinue
}
Write-Output "$script:pass passed, $script:fail failed"
if ($script:fail -ne 0) { exit 1 }
