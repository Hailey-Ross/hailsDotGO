# deploy.ps1 — full build + deploy using OpenSSH (ssh + scp)
# Requires SSH key at %USERPROFILE%\.ssh\hailsdotgo with access to the VPS.

$ErrorActionPreference = "Stop"
$root = $PSScriptRoot

# ── Load .env ────────────────────────────────────────────────
$envPath = Join-Path $root ".env"
Get-Content $envPath | ForEach-Object {
    if ($_ -match "^VPS_HOST=(.+)$") { $script:vpsHost = $matches[1].Trim() }
    if ($_ -match "^VPS_USER=(.+)$") { $script:vpsUser = $matches[1].Trim() }
}
if (-not $script:vpsHost) { throw ".env missing VPS_HOST" }
if (-not $script:vpsUser) { throw ".env missing VPS_USER" }

$target = "$($script:vpsUser)@$($script:vpsHost)"
$key    = "$env:USERPROFILE\.ssh\hailsdotgo"
$ssh    = @("-i", $key, "-o", "BatchMode=yes")

function Run([string]$cmd) {
    & ssh @ssh $target $cmd
    if ($LASTEXITCODE -ne 0) { throw "Remote command failed: $cmd" }
}

function Put([string]$src, [string]$dst) {
    $rounds   = 3
    $attempts = 3
    for ($r = 1; $r -le $rounds; $r++) {
        for ($i = 1; $i -le $attempts; $i++) {
            & scp @ssh (Join-Path $root $src) "${target}:${dst}"
            if ($LASTEXITCODE -eq 0) { return }
            Write-Host "    Round $r attempt $i/$attempts failed" -ForegroundColor Yellow
        }
        if ($r -lt $rounds) {
            $wait = Get-Random -Minimum 20 -Maximum 31
            Write-Host "    All $attempts attempts failed - waiting ${wait}s before round $($r+1)..." -ForegroundColor Yellow
            Start-Sleep -Seconds $wait
        }
    }
    throw "Upload failed after $rounds rounds: $src"
}

# SCP is unreliable for the large binary on this server.
# Use cmd.exe stdin redirection to pipe the file through SSH; avoids SCP entirely.
function PutBinary([string]$src, [string]$dst) {
    $srcPath = Join-Path $root $src
    $keyPath = "$env:USERPROFILE\.ssh\hailsdotgo"
    # cmd /c with "" as escaped quotes; < redirects binary stdin through ssh to cat on the server
    $cmdStr = "ssh -i ""$keyPath"" -o BatchMode=yes $target ""cat > $dst"" < ""$srcPath"""
    cmd /c $cmdStr
    if ($LASTEXITCODE -ne 0) { throw "Binary upload failed: $src" }
}

# ── Build ────────────────────────────────────────────────────
Write-Host "==> Building TypeScript..." -ForegroundColor Cyan
Set-Location $root
npm run build

Write-Host "==> Cross-compiling Go for Linux..." -ForegroundColor Cyan
$env:GOOS = "linux"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
go build -ldflags="-s -w" -o hailsDotGO-linux .
$env:GOOS = ""; $env:GOARCH = ""; $env:CGO_ENABLED = ""

# ── Deploy ───────────────────────────────────────────────────
Write-Host "==> Stopping service..." -ForegroundColor Yellow
Run "systemctl stop hailsdotgo"

Write-Host "==> Uploading binary..." -ForegroundColor Cyan
PutBinary "hailsDotGO-linux" "/opt/hailsdotgo/hailsDotGO"
Run "chmod +x /opt/hailsdotgo/hailsDotGO"

Write-Host "==> Uploading static assets..." -ForegroundColor Cyan
Put "static\js\raids.js"      "/opt/hailsdotgo/static/js/"
Put "static\js\dps.js"        "/opt/hailsdotgo/static/js/"
Put "static\js\pvp.js"        "/opt/hailsdotgo/static/js/"
Put "static\js\events.js"     "/opt/hailsdotgo/static/js/"
Put "static\css\main.css"     "/opt/hailsdotgo/static/css/"
Put "static\maintenance.html" "/opt/hailsdotgo/static/"

Write-Host "==> Uploading templates..." -ForegroundColor Cyan
Put "templates\base.html"      "/opt/hailsdotgo/templates/"
Put "templates\home.html"      "/opt/hailsdotgo/templates/"
Put "templates\raids.html"     "/opt/hailsdotgo/templates/"
Put "templates\dps.html"       "/opt/hailsdotgo/templates/"
Put "templates\pvp.html"       "/opt/hailsdotgo/templates/"
Put "templates\events.html"    "/opt/hailsdotgo/templates/"
Put "templates\changelog.html" "/opt/hailsdotgo/templates/"

Write-Host "==> Starting service..." -ForegroundColor Cyan
Run "chown -R hailsdotgo:hailsdotgo /opt/hailsdotgo && systemctl start hailsdotgo"

Start-Sleep -Seconds 3
$status = (& ssh @ssh $target "systemctl is-active hailsdotgo").Trim()
if ($status -eq "active") {
    Write-Host "`n==> Live at https://pogo.hails.live" -ForegroundColor Green
} else {
    Write-Host "`n==> Service status: $status - check logs on VPS" -ForegroundColor Red
}
