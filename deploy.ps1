# deploy.ps1 — full build + deploy using OpenSSH (ssh + scp)
# Requires SSH key at %USERPROFILE%\.ssh\hailsdotgo with access to the VPS.
# Uses a SHA256 manifest (deploy-manifest.json) to skip unchanged files.
# The service is only stopped if something actually needs uploading.

$ErrorActionPreference = "Stop"
$root = $PSScriptRoot

# ── Load .env ────────────────────────────────────────────────
$envPath = Join-Path $root ".env"
Get-Content $envPath | ForEach-Object {
    if ($_ -match "^VPS_HOST=(.+)$") { $script:vpsHost = $matches[1].Trim() }
    if ($_ -match "^VPS_USER=(.+)$") { $script:vpsUser = $matches[1].Trim() }
    if ($_ -match "^DB_HOST=(.+)$")         { $script:dbHost        = $matches[1].Trim() }
    if ($_ -match "^DB_USER=(.+)$")         { $script:dbUser        = $matches[1].Trim() }
    if ($_ -match "^DB_PASS=(.+)$")         { $script:dbPass        = $matches[1].Trim() }
    if ($_ -match "^DB_NAME=(.+)$")         { $script:dbName        = $matches[1].Trim() }
    if ($_ -match "^SUPERADMIN_USER=(.+)$") { $script:superadminUser = $matches[1].Trim() }
    if ($_ -match "^OPENWEATHER_KEY=(.+)$") { $script:openweatherKey = $matches[1].Trim() }
}
if (-not $script:vpsHost) { throw ".env missing VPS_HOST" }
if (-not $script:vpsUser) { throw ".env missing VPS_USER" }
if (-not $script:dbHost)         { throw ".env missing DB_HOST" }
if (-not $script:superadminUser) { throw ".env missing SUPERADMIN_USER" }

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
    $cmdStr  = "ssh -i ""$keyPath"" -o BatchMode=yes $target ""cat > $dst"" < ""$srcPath"""
    $rounds  = 5
    $attempts = 3
    for ($r = 1; $r -le $rounds; $r++) {
        for ($i = 1; $i -le $attempts; $i++) {
            cmd /c $cmdStr
            if ($LASTEXITCODE -eq 0) { return }
            Write-Host "    Binary upload round $r attempt $i/$attempts failed" -ForegroundColor Yellow
        }
        if ($r -lt $rounds) {
            $wait = Get-Random -Minimum 15 -Maximum 26
            Write-Host "    Waiting ${wait}s before round $($r+1)..." -ForegroundColor Yellow
            Start-Sleep -Seconds $wait
        }
    }
    throw "Binary upload failed after $rounds rounds: $src"
}

# ── Build ────────────────────────────────────────────────────
Write-Host "==> Building TypeScript..." -ForegroundColor Cyan
Set-Location $root
npm run build

Write-Host "==> Cross-compiling Go for Linux..." -ForegroundColor Cyan
$env:GOOS = "linux"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
go build -ldflags="-s -w" -o hailsDotGO-linux .
$env:GOOS = ""; $env:GOARCH = ""; $env:CGO_ENABLED = ""

# ── Write server-side app.env (UTF-8 without BOM — systemd requires it) ──
$appEnvPath = Join-Path $root "app.env"
$appEnvContent = "DB_HOST=$($script:dbHost)`nDB_USER=$($script:dbUser)`nDB_PASS=$($script:dbPass)`nDB_NAME=$($script:dbName)`nSUPERADMIN_USER=$($script:superadminUser)`nOPENWEATHER_KEY=$($script:openweatherKey)`n"
$utf8NoBom = New-Object System.Text.UTF8Encoding $false
[System.IO.File]::WriteAllText($appEnvPath, $appEnvContent, $utf8NoBom)

# ── Load hash manifest ───────────────────────────────────────
$manifestPath = Join-Path $root "deploy-manifest.json"
$manifest = @{}
if (Test-Path $manifestPath) {
    $raw = Get-Content $manifestPath -Raw | ConvertFrom-Json
    $raw.PSObject.Properties | ForEach-Object { $manifest[$_.Name] = $_.Value }
}
$newManifest = @{}

function FileHash([string]$rel) {
    return (Get-FileHash (Join-Path $root $rel) -Algorithm SHA256).Hash
}

# ── Diff all tracked files against manifest ──────────────────
# app.env is always uploaded (regenerated from secrets); not tracked in manifest.
# Binary is tracked — if Go source didn't change the hash will match and we skip it.

$trackedFiles = @(
    @{ src = "hailsDotGO-linux";           dst = "/opt/hailsdotgo/hailsDotGO";            binary = $true  },
    @{ src = "static\css\main.css";        dst = "/opt/hailsdotgo/static/css/";           binary = $false },
    @{ src = "static\maintenance.html";    dst = "/opt/hailsdotgo/static/";               binary = $false },
    @{ src = "static\js\raids.js";         dst = "/opt/hailsdotgo/static/js/";            binary = $false },
    @{ src = "static\js\dps.js";           dst = "/opt/hailsdotgo/static/js/";            binary = $false },
    @{ src = "static\js\pvp.js";           dst = "/opt/hailsdotgo/static/js/";            binary = $false },
    @{ src = "static\js\events.js";        dst = "/opt/hailsdotgo/static/js/";            binary = $false },
    @{ src = "static\js\shinies.js";       dst = "/opt/hailsdotgo/static/js/";            binary = $false },
    @{ src = "templates\base.html";        dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\home.html";        dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\raids.html";       dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\dps.html";         dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\pvp.html";         dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\events.html";      dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\credits.html";     dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\login.html";       dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\register.html";    dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\shinies.html";     dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\admin.html";       dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\settings.html";    dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\trainers.html";    dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "hailsdotgo.service";         dst = "/opt/hailsdotgo/hailsdotgo.service";    binary = $false }
)

$pending = [System.Collections.Generic.List[object]]::new()

Write-Host "==> Checking for changes..." -ForegroundColor Cyan
foreach ($f in $trackedFiles) {
    $key  = $f.src.Replace('\', '/')
    $hash = FileHash $f.src
    $newManifest[$key] = $hash
    if ($manifest[$key] -ne $hash) {
        $f.changed = $true
        $pending.Add($f)
        Write-Host "  changed  $($f.src)" -ForegroundColor White
    } else {
        Write-Host "  unchanged $($f.src)" -ForegroundColor DarkGray
    }
}

$skipCount   = $trackedFiles.Count - $pending.Count
$uploadCount = $pending.Count

if ($pending.Count -eq 0) {
    Write-Host "`n==> Nothing changed. Uploading app.env only (no service restart)." -ForegroundColor Green
    Put "app.env" "/opt/hailsdotgo/app.env"
    Run "chmod 600 /opt/hailsdotgo/app.env"
    Remove-Item $appEnvPath -ErrorAction SilentlyContinue
    $newManifest | ConvertTo-Json | Out-File -FilePath $manifestPath -Encoding utf8
    Write-Host "==> Done. $skipCount file(s) unchanged, manifest saved." -ForegroundColor Green
    exit 0
}

# ── Deploy ───────────────────────────────────────────────────
Write-Host "`n==> Stopping service ($uploadCount file(s) to upload)..." -ForegroundColor Yellow
Run "systemctl stop hailsdotgo"

foreach ($f in $pending) {
    Write-Host "  uploading $($f.src)" -ForegroundColor Cyan
    if ($f.binary) {
        PutBinary $f.src $f.dst
        Run "chmod +x /opt/hailsdotgo/hailsDotGO"
    } elseif ($f.src -eq "hailsdotgo.service") {
        Put $f.src $f.dst
    } else {
        Put $f.src $f.dst
    }
}

# Service file: reload systemd if it was updated
$serviceChanged = $pending | Where-Object { $_.src -eq "hailsdotgo.service" }
Write-Host "==> Uploading app config..." -ForegroundColor Cyan
Put "app.env" "/opt/hailsdotgo/app.env"
Run "chmod 600 /opt/hailsdotgo/app.env"
if ($serviceChanged) {
    Run "cp /opt/hailsdotgo/hailsdotgo.service /etc/systemd/system/hailsdotgo.service && systemctl daemon-reload"
}

# Clean up local temp file
Remove-Item $appEnvPath -ErrorAction SilentlyContinue

Write-Host "==> Starting service..." -ForegroundColor Cyan
Run "chown -R hailsdotgo:hailsdotgo /opt/hailsdotgo && systemctl start hailsdotgo"

Start-Sleep -Seconds 3
$status = (& ssh @ssh $target "systemctl is-active hailsdotgo").Trim()
if ($status -eq "active") {
    # Only save manifest on successful deploy
    $newManifest | ConvertTo-Json | Out-File -FilePath $manifestPath -Encoding utf8
    Write-Host "`n==> Live at https://pogo.hails.live" -ForegroundColor Green
    Write-Host "==> $uploadCount uploaded, $skipCount unchanged." -ForegroundColor Green
} else {
    Write-Host "`n==> Service status: $status - check logs on VPS" -ForegroundColor Red
    Write-Host "==> Manifest NOT saved (deploy failed). Next run will retry all changed files." -ForegroundColor Yellow
}
