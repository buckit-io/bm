<#
.SYNOPSIS
    Per-user installer for bm (Buckit Manager) on Windows.

.DESCRIPTION
    Downloads the latest stable bm.exe for this architecture, verifies its
    SHA-256 against the published release pointer, installs it to a per-user
    directory, and adds that directory to the user PATH. No admin rights and
    no system-wide install — bm is a personal-tool binary.

.EXAMPLE
    irm https://buckit-io.github.io/bm/install.ps1 | iex

.NOTES
    Environment overrides:
      BM_INSTALL_DIR    install directory (default: $env:LOCALAPPDATA\Programs\bm)
      BM_PAGES_BASE     gh-pages base URL  (default: https://buckit-io.github.io/bm)
      BM_RELEASE_BASE   release download base
#>

$ErrorActionPreference = 'Stop'

$PagesBase   = if ($env:BM_PAGES_BASE)   { $env:BM_PAGES_BASE }   else { 'https://buckit-io.github.io/bm' }
$ReleaseBase = if ($env:BM_RELEASE_BASE) { $env:BM_RELEASE_BASE } else { 'https://github.com/buckit-io/bm/releases/download' }
$InstallDir  = if ($env:BM_INSTALL_DIR)  { $env:BM_INSTALL_DIR }  else { Join-Path $env:LOCALAPPDATA 'Programs\bm' }

function Fail($msg) {
    Write-Error "install.ps1: $msg"
    exit 1
}

function Info($msg) {
    Write-Host "==> $msg"
}

# Detect architecture. Only amd64 is published for Windows today.
$arch = $env:PROCESSOR_ARCHITECTURE
switch ($arch) {
    'AMD64' { $platform = 'windows-amd64' }
    default { Fail "unsupported architecture '$arch'. Only amd64 is published for Windows." }
}
Info "platform: $platform"

$pointerUrl = "$PagesBase/manager/bm/release/$platform/bm.sha256sum"
Info 'resolving latest release'
try {
    # Use Invoke-RestMethod here: it returns the small text payload directly and
    # is the same cmdlet used by the documented `irm ... | iex` bootstrap.
    $pointer = (Invoke-RestMethod -Uri $pointerUrl -ErrorAction Stop).Trim()
} catch {
    Fail "could not fetch release pointer at ${pointerUrl}: $($_.Exception.Message)"
}

# Pointer format: "<sha256>  bm.exe.<tag>"
$parts   = $pointer -split '\s+'
$wantSha = $parts[0]
$name    = $parts[1]
$tag     = $name -replace '^bm\.exe\.', ''
if ($tag -notmatch '^RELEASE\.') { Fail "unexpected release pointer payload: $pointer" }
if (-not $wantSha)               { Fail 'release pointer missing checksum' }
Info "latest stable: $tag"

$asset       = "bm-$platform.$tag.exe"
$downloadUrl = "$ReleaseBase/$tag/$asset"

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("bm-install-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
    $binTmp = Join-Path $tmp 'bm.exe'
    Info "downloading $asset"
    try {
        Invoke-WebRequest -UseBasicParsing -Uri $downloadUrl -OutFile $binTmp -ErrorAction Stop
    } catch {
        Fail "download failed at ${downloadUrl}: $($_.Exception.Message)"
    }

    $gotSha = (Get-FileHash -Algorithm SHA256 -Path $binTmp).Hash.ToLower()
    if ($gotSha -ne $wantSha.ToLower()) {
        Fail "checksum mismatch (expected $wantSha, got $gotSha) - refusing to install"
    }
    Info 'sha256 verified'

    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    $dest = Join-Path $InstallDir 'bm.exe'
    Move-Item -Force -Path $binTmp -Destination $dest
    Info "installed bm to $dest"
} finally {
    Remove-Item -Recurse -Force -Path $tmp -ErrorAction SilentlyContinue
}

# Add the install dir to the user PATH if it isn't already there.
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not $userPath) { $userPath = '' }
$onPath = ($userPath -split ';') -contains $InstallDir
if (-not $onPath) {
    $newPath = if ($userPath) { "$userPath;$InstallDir" } else { $InstallDir }
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    $env:Path = "$env:Path;$InstallDir"
    Info "added $InstallDir to your user PATH (restart your shell to pick it up)"
}

Write-Host ''
& (Join-Path $InstallDir 'bm.exe') version
Write-Host ''
Write-Host "Run 'bm web' to start the manager."
