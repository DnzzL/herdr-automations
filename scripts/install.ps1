# Installs herdr-automations on Windows, or updates it:
#
#   powershell -c "irm https://raw.githubusercontent.com/DnzzL/herdr-automations/main/scripts/install.ps1 | iex"
#
# Into %LOCALAPPDATA%\Programs\herdr-automations, added to your PATH. A
# running scheduler's exe is renamed aside, and the scheduler re-executes onto
# the new one within a minute. No admin needed. HERDR_AUTOMATIONS_VERSION=0.12.0
# picks a release (else the latest); HERDR_AUTOMATIONS_RELEASES_URL points
# elsewhere (tests).

# Its own scope: under `irm | iex` it would otherwise set the caller's variables.
& {
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

# throw, not exit: under `irm | iex`, exit would close the person's window.
function Fail($why) { throw "herdr-automations: $why" }

$releases = if ($env:HERDR_AUTOMATIONS_RELEASES_URL) { $env:HERDR_AUTOMATIONS_RELEASES_URL.TrimEnd("/") } else { "https://github.com/DnzzL/herdr-automations/releases" }
$base = if ($env:HERDR_AUTOMATIONS_VERSION) { "$releases/download/v$($env:HERDR_AUTOMATIONS_VERSION.TrimStart('v'))" } else { "$releases/latest/download" }
$dir = Join-Path $env:LOCALAPPDATA "Programs\herdr-automations"
$arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
if ($arch -ne "AMD64") { Fail "no build for $arch Windows (x64 only)" }
$asset = "herdr-automations_windows_amd64.exe"
if (-not (Get-Command curl.exe -ErrorAction SilentlyContinue)) { Fail "needs curl.exe (Windows 10 1803 or newer)" }

$tmp = Join-Path ([IO.Path]::GetTempPath()) "herdr-automations-install-$PID"
New-Item -ItemType Directory -Force $tmp | Out-Null
try {
    $new = Join-Path $tmp $asset
    curl.exe -fsSL "$base/$asset" -o $new
    if ($LASTEXITCODE) { Fail "download failed: $base/$asset" }
    curl.exe -fsSL "$base/checksums.txt" -o "$tmp\checksums.txt"
    if ($LASTEXITCODE) { Fail "download failed: $base/checksums.txt" }
    # sha256sum's format: "<hash>  <name>". No line for the asset is a refusal too.
    $want = $null
    foreach ($line in Get-Content "$tmp\checksums.txt") {
        $hash, $name = $line -split "\s+\*?", 2
        if ($name -eq $asset) { $want = $hash }
    }
    # .NET, not Get-FileHash: that one fails when PowerShell 7 starts 5.1.
    $stream = [IO.File]::OpenRead($new)
    try { $got = -join ([Security.Cryptography.SHA256]::Create().ComputeHash($stream) | ForEach-Object { $_.ToString("x2") }) } finally { $stream.Close() }
    if (-not $want -or $got -ne $want) { Fail "checksum mismatch for $asset, refusing the download; try again" }
    $version = & $new version
    if ($LASTEXITCODE) { Fail "the downloaded herdr-automations does not run on this machine" }

    # A running exe can be renamed, not overwritten: the scheduler sees its
    # path change content and re-executes onto the new one. The old exe goes
    # now if nothing runs it any more, else on the next install.
    New-Item -ItemType Directory -Force $dir | Out-Null
    $exe = Join-Path $dir "herdr-automations.exe"
    Remove-Item -Force "$exe.old" -ErrorAction SilentlyContinue
    if (Test-Path $exe) { Rename-Item $exe "herdr-automations.exe.old" }
    Move-Item $new $exe
    Remove-Item -Force "$exe.old" -ErrorAction SilentlyContinue

    # The raw user PATH, so %VARIABLES% in it stay unexpanded and keep their type.
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey("Environment", $true)
    $raw = [string]$key.GetValue("Path", "", [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    $listed = $raw -split ";" | Where-Object { $_.Trim().TrimEnd("\") -ieq $dir.TrimEnd("\") }
    if (-not $listed) {
        $kind = if ($key.GetValueNames() -contains "Path") { $key.GetValueKind("Path") } else { [Microsoft.Win32.RegistryValueKind]::ExpandString }
        $key.SetValue("Path", $(if ($raw) { "$($raw.TrimEnd(';'));$dir" } else { $dir }), $kind)
        # Setting any user variable tells running programs the environment changed.
        [Environment]::SetEnvironmentVariable("HERDR_AUTOMATIONS_INSTALLING", "1", "User")
        [Environment]::SetEnvironmentVariable("HERDR_AUTOMATIONS_INSTALLING", $null, "User")
        Write-Host "Added $dir to your PATH: new terminals have herdr-automations."
    }
    $key.Close()
    if (($env:Path -split ";") -notcontains $dir) { $env:Path += ";$dir" }
    Write-Host "herdr-automations $version installed into $dir."
    Write-Host "Start the scheduler at login: herdr-automations service install"
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
}
