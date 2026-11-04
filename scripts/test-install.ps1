# CI's proof of install.ps1 on Windows (ADR 0013): it installs releases built
# here, served from a file:// URL through HERDR_AUTOMATIONS_RELEASES_URL, into
# this machine's real %LOCALAPPDATA% and user PATH. Throws on the first broken
# promise. Run from anywhere: pwsh scripts/test-install.ps1
$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot
$work = Join-Path ([IO.Path]::GetTempPath()) "herdr-automations-test-install"
$releases = Join-Path $work "releases"
$asset = "herdr-automations_windows_amd64.exe"
$dir = Join-Path $env:LOCALAPPDATA "Programs\herdr-automations"
$exe = Join-Path $dir "herdr-automations.exe"
$env:HERDR_AUTOMATIONS_RELEASES_URL = ([Uri]$releases).AbsoluteUri
$env:HERDR_PLUGIN_STATE_DIR = Join-Path $work "state"
$env:HERDR_PLUGIN_CONFIG_DIR = Join-Path $work "config"
New-Item -ItemType Directory -Force $env:HERDR_PLUGIN_STATE_DIR, $env:HERDR_PLUGIN_CONFIG_DIR | Out-Null

# GitHub's layout: latest/download/<asset>, and download/v<version>/<asset>.
function Release($version, $at) {
    $out = Join-Path $releases $at
    New-Item -ItemType Directory -Force $out | Out-Null
    go build -C $root -ldflags "-X main.Version=$version" -o (Join-Path $out $asset) .
    if ($LASTEXITCODE) { throw "go build failed" }
    $hash = (Get-FileHash (Join-Path $out $asset) -Algorithm SHA256).Hash.ToLower()
    Set-Content (Join-Path $out "checksums.txt") "$hash  $asset"
}
# As a person runs it: `irm | iex` in Windows PowerShell. A throw exits 1.
function Install {
    powershell -NoProfile -Command "Get-Content -Raw '$root\scripts\install.ps1' | Invoke-Expression" | Out-Host
    $LASTEXITCODE -eq 0
}
function Expect($what, $ok) { if (-not $ok) { throw "FAIL: $what" }; Write-Host "ok: $what" }
function Scheduler { if ((& $exe service status | Out-String) -match "pid (\d+)") { [int]$Matches[1] } else { 0 } }

$daemon = $null
try {
    Release "ci-1" "latest\download"
    Expect "a first install succeeds" (Install)
    Expect "the installed exe prints its version" ((& $exe version) -eq "ci-1")

    Set-Content (Join-Path $releases "latest\download\checksums.txt") "$('0' * 64)  $asset"
    Expect "a checksum mismatch is refused" (-not (Install))
    Expect "a refused install leaves the installed exe alone" ((& $exe version) -eq "ci-1")

    $daemon = Start-Process $exe -ArgumentList "daemon", "--standby" -PassThru -NoNewWindow `
        -RedirectStandardError (Join-Path $work "first.log")
    $deadline = (Get-Date).AddSeconds(30)
    while ((Scheduler) -ne $daemon.Id -and (Get-Date) -lt $deadline) { Start-Sleep 1 }
    Expect "the installed exe runs the scheduler" ((Scheduler) -eq $daemon.Id)

    Release "ci-2" "download\vci-2"
    $env:HERDR_AUTOMATIONS_VERSION = "ci-2"
    Expect "a reinstall while the scheduler runs succeeds" (Install)
    Expect "the version asked for is installed" ((& $exe version) -eq "ci-2")
    # A tick to notice the change, then the copy waits up to a tick for the lock.
    $deadline = (Get-Date).AddSeconds(120)
    while (((Scheduler) -in 0, $daemon.Id) -and (Get-Date) -lt $deadline) { Start-Sleep 2 }
    $copy = Scheduler
    Expect "the scheduler hands over to a new process" ($copy -notin 0, $daemon.Id -and $daemon.HasExited)
    Expect "the new scheduler runs the installed exe" ((Get-Process -Id $copy).Path -eq $exe)

    $raw = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey("Environment").GetValue("Path", "", "DoNotExpandEnvironmentNames")
    Expect "the user PATH lists the folder once" (@($raw -split ";" | Where-Object { $_ -ieq $dir }).Count -eq 1)
} finally {
    $running = try { Scheduler } catch { 0 }
    if ($running) { Stop-Process -Id $running -Force -ErrorAction SilentlyContinue }
    if ($daemon -and -not $daemon.HasExited) { Stop-Process -Id $daemon.Id -Force -ErrorAction SilentlyContinue }
    # The first scheduler's log, then its copy's (startDetached writes that one).
    Get-Content (Join-Path $work "first.log"), (Join-Path $env:HERDR_PLUGIN_STATE_DIR "daemon.log") -ErrorAction SilentlyContinue
}
