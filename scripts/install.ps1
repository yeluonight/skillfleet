<#
.SYNOPSIS
  SkillFleet installer for Windows (PowerShell). Downloads a prebuilt
  agent or server binary from GitHub Releases for the current
  architecture, verifies its SHA256, and installs it.

.DESCRIPTION
  Usage:
    irm https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.ps1 | iex
    $env:SKILLFLEET_COMPONENT = "server"; irm https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.ps1 | iex

  Environment overrides:
    SKILLFLEET_COMPONENT  agent (default) | server
    SKILLFLEET_VERSION    a release tag (default: latest)
    INSTALL_DIR           target dir (default: %LOCALAPPDATA%\Programs\skillfleet)

  SQLite is pure Go, so the binaries are static and dependency-free.
#>

$ErrorActionPreference = "Stop"

$Repo = "yeluonight/skillfleet"
$Component = if ($env:SKILLFLEET_COMPONENT) { $env:SKILLFLEET_COMPONENT } else { "agent" }
$Version = if ($env:SKILLFLEET_VERSION) { $env:SKILLFLEET_VERSION } else { "latest" }

if ($Component -ne "agent" -and $Component -ne "server") {
    Write-Error "SKILLFLEET_COMPONENT must be 'agent' or 'server', got '$Component'"
}

# --- detect architecture ---
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default { Write-Error "unsupported architecture '$($env:PROCESSOR_ARCHITECTURE)'" }
}

$asset = "skillfleet-$Component-windows-$arch.exe"

# --- resolve download base ---
$base = if ($Version -eq "latest") {
    "https://github.com/$Repo/releases/latest/download"
} else {
    "https://github.com/$Repo/releases/download/$Version"
}

# --- install dir ---
$installDir = if ($env:INSTALL_DIR) {
    $env:INSTALL_DIR
} else {
    Join-Path $env:LOCALAPPDATA "Programs\skillfleet"
}
New-Item -ItemType Directory -Force -Path $installDir | Out-Null

$tmp = New-Item -ItemType Directory -Force -Path (Join-Path $env:TEMP ([System.Guid]::NewGuid().ToString()))
try {
    Write-Host "==> downloading $asset ($Version)"
    $assetPath = Join-Path $tmp $asset
    Invoke-WebRequest -Uri "$base/$asset" -OutFile $assetPath -UseBasicParsing

    # --- verify SHA256 ---
    $sumsPath = Join-Path $tmp "SHA256SUMS"
    try {
        Invoke-WebRequest -Uri "$base/SHA256SUMS" -OutFile $sumsPath -UseBasicParsing
        $want = (Select-String -Path $sumsPath -Pattern ([regex]::Escape($asset)) |
            Select-Object -First 1).Line -split '\s+' | Select-Object -First 1
        if ($want) {
            $got = (Get-FileHash -Algorithm SHA256 -Path $assetPath).Hash.ToLower()
            if ($got -ne $want.ToLower()) {
                Write-Error "checksum mismatch for ${asset}: want $want got $got"
            }
            Write-Host "==> checksum ok"
        }
    } catch {
        Write-Warning "could not fetch/verify SHA256SUMS; skipping checksum verification"
    }

    # --- install ---
    $dest = Join-Path $installDir "skillfleet-$Component.exe"
    Move-Item -Force -Path $assetPath -Destination $dest
    Write-Host "==> installed $dest"

    if ($env:PATH -notlike "*$installDir*") {
        Write-Host "note: $installDir is not on your PATH; add it to use 'skillfleet-$Component' directly"
    }
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

# --- next-step hints ---
if ($Component -eq "agent") {
    Write-Host @"

Next steps:
  1. skillfleet-agent enroll <server-url> <token>     # token from the WebUI
  2. Approve the device in the WebUI (Devices page).
  3. skillfleet-agent                                  # heartbeat + candidate roots + inventory + jobs
  4. In the WebUI Devices / Roots area, register a candidate root for this device.

CLI fallback when no candidate is shown:
  skillfleet-agent roots scan
  skillfleet-agent roots add -tool claude-code -scope user -path `$HOME\.claude\skills
  skillfleet-agent roots list
"@
} else {
    Write-Host @"

Next steps:
  skillfleet-server                                    # starts on :7890; prints a setup code
  Open http://<host>:7890 and complete setup with that code.
"@
}
