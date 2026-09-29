# One-line installer for the tpt-cortex native tools (cortex-daemon +
# cortex-engine) on Windows. Usage:
#
#   irm https://github.com/tpt-solutions/tpt-pwa/releases/latest/download/install.ps1 | iex
#
# or, pinned to a version:  $env:TPT_VERSION="v0.2.0"; irm ... | iex
# Installs into ~\.tpt\bin by default (override with TPT_INSTALL_DIR) and
# verifies every download against the published SHA256SUMS.
# Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
$ErrorActionPreference = "Stop"

$Repo = "tpt-solutions/tpt-pwa"
$Version = if ($env:TPT_VERSION) { $env:TPT_VERSION } else { "latest" }
$InstallDir = if ($env:TPT_INSTALL_DIR) { $env:TPT_INSTALL_DIR } else { "$HOME\.tpt\bin" }

$Arch = if ([Environment]::Is64BitOperatingSystem -and (Get-CimInstance Win32_Processor).Architecture -eq 12) { "arm64" } else { "amd64" }
$Target = "windows-$Arch"

if ($Version -eq "latest") {
    $Version = (Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest").tag_name
    if (-not $Version) { throw "could not determine the latest release" }
}
Write-Host "installing tpt-cortex $Version for $Target -> $InstallDir"

$Tmp = Join-Path ([IO.Path]::GetTempPath()) ("tpt-install-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $Tmp | Out-Null
try {
    $Base = "https://github.com/$Repo/releases/download/$Version"
    $Archive = "tpt-cortex-$Target.zip"

    Invoke-WebRequest "$Base/SHA256SUMS-$Target.txt" -OutFile "$Tmp\SHA256SUMS.txt"
    Invoke-WebRequest "$Base/$Archive" -OutFile "$Tmp\$Archive"

    # The sums file hashes the UNPACKED files: extract, verify every binary
    # against its published hash, and only then install.
    Expand-Archive "$Tmp\$Archive" -DestinationPath "$Tmp\out"
    Get-ChildItem "$Tmp\out" -File | Where-Object { $_.Name -ne "SHA256SUMS.txt" } | ForEach-Object {
        $name = $_.Name
        $line = Get-Content "$Tmp\SHA256SUMS.txt" | Where-Object { $_ -match [regex]::Escape($name) }
        if (-not $line) { throw "no published checksum for $name -- refusing to install" }
        $wantHash = ($line -split "\s+") | Select-Object -First 1
        $gotHash = (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower()
        if ($gotHash -ne $wantHash) {
            throw "checksum verification FAILED for $name -- refusing to install"
        }
    }

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item "$Tmp\out\*" $InstallDir -Force
}
finally {
    Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}

if (($env:Path -split ";") -notcontains $InstallDir) {
    Write-Host ""
    Write-Host "Add $InstallDir to your PATH:"
    Write-Host "  [Environment]::SetEnvironmentVariable('Path', `"$InstallDir;`$([Environment]::GetEnvironmentVariable('Path', 'User'))`", 'User')"
}
Write-Host ""
Write-Host "Verify the installation:"
Write-Host "  $InstallDir\cortex-daemon.exe doctor"
Write-Host "  $InstallDir\cortex-engine.exe"
