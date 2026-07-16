<#
.SYNOPSIS
    Install PaperAgent on Windows (PowerShell 5.0+ / 7+)
.DESCRIPTION
    Downloads and installs the latest PaperAgent release for Windows.
    Supports both PowerShell 5 (built-in) and PowerShell 7 (cross-platform).

    Downloads to temporary files first, verifies, then atomically replaces
    existing installations. Safe to re-run for upgrades.
.EXAMPLE
    # One-liner (run in PowerShell as normal user):
    irm https://raw.githubusercontent.com/happyTonakai/PaperAgent/main/install.ps1 | iex

    # With custom install directory:
    $env:INSTALL_DIR = "C:\tools"; irm https://raw.githubusercontent.com/happyTonakai/PaperAgent/main/install.ps1 | iex

    # Pin a specific version:
    $env:VERSION = "v1.2.0"; irm https://raw.githubusercontent.com/happyTonakai/PaperAgent/main/install.ps1 | iex
#>

function Install-PaperAgent {
    $ErrorActionPreference = 'Stop'
    $Repo = "happyTonakai/PaperAgent"
    $InstallDir = ($env:INSTALL_DIR, "$env:USERPROFILE\.local\bin" | Select-Object -First 1)

    # ── Ensure TLS 1.2 (PS5 on older Windows defaults to TLS 1.0) ────
    if ([Net.ServicePointManager]::SecurityProtocol -notmatch 'Tls12') {
        try {
            [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.ServicePointManager]::SecurityProtocolType::Tls12
        } catch {
            # Older .NET — proceed anyway, may fail on HTTPS
        }
    }

    # ── Detect architecture ──────────────────────────────────────────
    # PROCESSOR_ARCHITEW6432 is set only when a 32-bit process runs on
    # 64-bit Windows (e.g. default PS5 x86 on modern hardware).
    $NativeArch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }

    switch -regex ($NativeArch) {
        'AMD64|x86_64' { $GoArch = 'amd64' }
        'ARM64|arm64'  { $GoArch = 'arm64' }
        default {
            throw "Unsupported architecture: $NativeArch (only amd64 and arm64 are supported)"
        }
    }

    # release.yml only publishes windows/amd64
    if ($GoArch -ne 'amd64') {
        throw "PaperAgent does not publish a Windows ARM64 binary. Build from source at https://github.com/$Repo"
    }

    # ── Resolve version ──────────────────────────────────────────────
    if ($env:VERSION) {
        $Tag = $env:VERSION
    } else {
        Write-Host 'Detecting latest release...' -ForegroundColor Cyan
        try {
            $Release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing -ErrorAction Stop
            $Tag = $Release.tag_name
        } catch {
            throw "Failed to detect the latest release: $_"
        }
        if ([string]::IsNullOrEmpty($Tag)) {
            throw 'Failed to detect the latest release tag.'
        }
    }

    # ── Prepare paths ───────────────────────────────────────────────
    $Suffix = '.exe'
    $BinaryName = "paperagent_windows_${GoArch}${Suffix}"
    $BinaryName2 = "arxiv2md_windows_${GoArch}${Suffix}"
    $Url = "https://github.com/$Repo/releases/download/$Tag/$BinaryName"
    $Url2 = "https://github.com/$Repo/releases/download/$Tag/$BinaryName2"
    $OutFile = Join-Path $InstallDir "paperagent${Suffix}"
    $OutFile2 = Join-Path $InstallDir "arxiv2md${Suffix}"

    New-Item -ItemType Directory -Path $InstallDir -Force -ErrorAction SilentlyContinue | Out-Null

    # ── Check if PaperAgent is currently running ─────────────────────
    $RunningProc = Get-Process -Name 'paperagent' -ErrorAction SilentlyContinue
    if ($RunningProc) {
        throw "PaperAgent is currently running (PID $($RunningProc.Id)). Please quit it from the system tray before upgrading."
    }

    # ── Create temp file paths (same dir as target, same filesystem) ─
    $DestDir = Split-Path -Parent $OutFile
    $TempFile = Join-Path $DestDir ".paperagent.$([System.IO.Path]::GetRandomFileName())"
    $TempFile2 = Join-Path $DestDir ".arxiv2md.$([System.IO.Path]::GetRandomFileName())"

    try {
        # ── Download both ──────────────────────────────────────────────
        Write-Host "Downloading $BinaryName ($Tag)..." -ForegroundColor Cyan
        Invoke-WebRequest -Uri $Url -OutFile $TempFile -UseBasicParsing -ErrorAction Stop

        Write-Host "Downloading $BinaryName2 ($Tag)..." -ForegroundColor Cyan
        Invoke-WebRequest -Uri $Url2 -OutFile $TempFile2 -UseBasicParsing -ErrorAction Stop

        # ── SHA256 verification (if checksums.txt exists in release) ───
        $ChecksumsUrl = "https://github.com/$Repo/releases/download/$Tag/checksums.txt"
        $ChecksumsText = $null
        try {
            $ChecksumsText = Invoke-RestMethod -Uri $ChecksumsUrl -UseBasicParsing -ErrorAction Stop
        } catch {
            Write-Host '  checksums.txt not found; skipping SHA256 verification.' -ForegroundColor Yellow
        }

        if ($null -ne $ChecksumsText) {
            foreach ($pair in @(
                    @{ Label = $BinaryName; File = $TempFile; Name = $BinaryName }
                    @{ Label = $BinaryName2; File = $TempFile2; Name = $BinaryName2 }
                )) {
                $ExpectedLine = $ChecksumsText -split "`n" | Where-Object { $_ -match "\b$($pair.Name)$" } | Select-Object -First 1
                if ($ExpectedLine) {
                    $ExpectedHash = ($ExpectedLine -split '\s+')[0]
                    $ActualHash = (Get-FileHash -Algorithm SHA256 -Path $pair.File).Hash.ToLower()
                    if ($ActualHash -ne $ExpectedHash.ToLower()) {
                        throw "SHA256 mismatch for $($pair.Label). Expected $ExpectedHash, got $ActualHash. The download may be corrupted."
                    }
                    Write-Host "  SHA256 verified: $ExpectedHash" -ForegroundColor Green
                }
            }
        }

        # ── Verify both binaries in temp (before replacing) ────────────
        Write-Host ''
        Write-Host "Verifying $BinaryName..." -ForegroundColor Cyan
        $VersionOutput = & $TempFile -version 2>&1
        if ($LASTEXITCODE -ne 0) {
            throw "paperagent verification failed (exit code $LASTEXITCODE)"
        }
        $VersionOutput | ForEach-Object { Write-Host $_ }

        Write-Host "Verifying $BinaryName2..." -ForegroundColor Cyan
        & $TempFile2 --help 2>&1 | Out-Null
        if ($LASTEXITCODE -ne 0 -and $LASTEXITCODE -ne 1) {
            throw "arxiv2md verification failed (exit code $LASTEXITCODE)"
        }

        # ── Atomically replace both ────────────────────────────────────
        Write-Host ''
        Write-Host "Installing..." -ForegroundColor Cyan
        Move-Item -LiteralPath $TempFile -Destination $OutFile -Force -ErrorAction Stop
        $TempFile = $null  # clear so finally won't delete the moved file

        Move-Item -LiteralPath $TempFile2 -Destination $OutFile2 -Force -ErrorAction Stop
        $TempFile2 = $null  # clear so finally won't delete the moved file
    } finally {
        if ($TempFile -and (Test-Path -LiteralPath $TempFile)) {
            Remove-Item -LiteralPath $TempFile -Force -ErrorAction SilentlyContinue
        }
        if ($TempFile2 -and (Test-Path -LiteralPath $TempFile2)) {
            Remove-Item -LiteralPath $TempFile2 -Force -ErrorAction SilentlyContinue
        }
    }

    # ── Check PATH ───────────────────────────────────────────────────
    $Paths = $env:Path -split ';'
    $InPath = ($Paths -contains $InstallDir) -or ($Paths -contains "$InstallDir\")
    if (-not $InPath) {
        Write-Host ''
        Write-Host "⚠  $InstallDir is not in your PATH." -ForegroundColor Yellow
        Write-Host "   Add it by running the following in PowerShell:" -ForegroundColor Yellow
        Write-Host ''
        $UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        if ([string]::IsNullOrEmpty($UserPath)) {
            $NewPath = "${InstallDir};"
        } elseif ($UserPath.EndsWith(';')) {
            $NewPath = "${UserPath}${InstallDir};"
        } else {
            $NewPath = "${UserPath};${InstallDir};"
        }
        Write-Host "   [Environment]::SetEnvironmentVariable('Path', '$NewPath', 'User')" -ForegroundColor Cyan
        Write-Host ''
        Write-Host "   Then restart your terminal. Or add it via System Settings → Advanced → Environment Variables." -ForegroundColor Yellow
    }

    # ── Done ─────────────────────────────────────────────────────────
    Write-Host ''
    Write-Host "Installed paperagent to $OutFile" -ForegroundColor Green
    Write-Host "Installed arxiv2md to $OutFile2" -ForegroundColor Green
    Write-Host ''
    Write-Host "PaperAgent installed successfully!" -ForegroundColor Green
}

# Wrap in a function so throw inside does not exit the PowerShell host
# when invoked via `irm ... | iex`. No top-level exit here.
Install-PaperAgent
