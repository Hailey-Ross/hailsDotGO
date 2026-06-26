# deploy.ps1 -- full build + deploy using OpenSSH (ssh + scp)
# Requires SSH key at %USERPROFILE%\.ssh\hailsdotgo with access to the VPS.
# Uses a SHA256 manifest (deploy-manifest.json) to skip unchanged files.
# The service is only stopped if something actually needs uploading.

$ErrorActionPreference = "Stop"
$root      = $PSScriptRoot
$startTime = Get-Date

function Elapsed {
    $e = (Get-Date) - $startTime
    if ($e.TotalSeconds -lt 60) { return "$([int]$e.TotalSeconds)s" }
    return "$([int]$e.TotalMinutes)m $($e.Seconds)s"
}

function FmtBytes([long]$bytes) {
    if ($bytes -ge 1MB) { return "$([math]::Round($bytes / 1MB, 1)) MB" }
    if ($bytes -ge 1KB) { return "$([math]::Round($bytes / 1KB, 1)) KB" }
    return "$bytes B"
}

# -- Load .env -------------------------------------------------------
$envPath = Join-Path $root ".env"
Get-Content $envPath | ForEach-Object {
    if ($_ -match "^VPS_HOST=(.+)$")          { $script:vpsHost        = $matches[1].Trim() }
    if ($_ -match "^VPS_USER=(.+)$")          { $script:vpsUser        = $matches[1].Trim() }
    if ($_ -match "^DB_HOST=(.+)$")           { $script:dbHost         = $matches[1].Trim() }
    if ($_ -match "^DB_USER=(.+)$")           { $script:dbUser         = $matches[1].Trim() }
    if ($_ -match "^DB_PASS=(.+)$")           { $script:dbPass         = $matches[1].Trim() }
    if ($_ -match "^DB_NAME=(.+)$")           { $script:dbName         = $matches[1].Trim() }
    if ($_ -match "^SUPERADMIN_USER=(.+)$")   { $script:superadminUser  = $matches[1].Trim() }
    if ($_ -match "^CSRF_KEY=(.+)$")          { $script:csrfKey         = $matches[1].Trim() }
    if ($_ -match "^PAYPAL_CLIENT_ID=(.+)$")     { $script:ppClientID  = $matches[1].Trim() }
    if ($_ -match "^PAYPAL_CLIENT_SECRET=(.+)$") { $script:ppSecret    = $matches[1].Trim() }
    if ($_ -match "^PAYPAL_MODE=(.+)$")          { $script:ppMode      = $matches[1].Trim() }
    if ($_ -match "^PAYPAL_WEBHOOK_ID=(.+)$")    { $script:ppWebhookID = $matches[1].Trim() }
    if ($_ -match "^GITHUB_TOKEN=(.+)$")         { $script:ghToken     = $matches[1].Trim() }
    if ($_ -match "^GITHUB_REPO=(.+)$")          { $script:ghRepo      = $matches[1].Trim() }
    if ($_ -match "^FCM_PROJECT_ID=(.+)$")       { $script:fcmProjectID = $matches[1].Trim() }
    if ($_ -match "^FCM_CREDENTIALS_JSON=(.+)$") { $script:fcmCredJSON  = $matches[1].Trim() }
    if ($_ -match "^APNS_KEY_PATH=(.+)$")        { $script:apnsKeyPath  = $matches[1].Trim() }
    if ($_ -match "^APNS_KEY_ID=(.+)$")          { $script:apnsKeyID    = $matches[1].Trim() }
    if ($_ -match "^APNS_TEAM_ID=(.+)$")         { $script:apnsTeamID   = $matches[1].Trim() }
    if ($_ -match "^APNS_BUNDLE_ID=(.+)$")       { $script:apnsBundleID = $matches[1].Trim() }
    if ($_ -match "^APNS_PRODUCTION=(.+)$")      { $script:apnsProd     = $matches[1].Trim() }
}
if (-not $script:vpsHost)        { throw ".env missing VPS_HOST" }
if (-not $script:vpsUser)        { throw ".env missing VPS_USER" }
if (-not $script:dbHost)         { throw ".env missing DB_HOST" }
if (-not $script:superadminUser) { throw ".env missing SUPERADMIN_USER" }
if (-not $script:csrfKey)        { throw ".env missing CSRF_KEY (generate with: openssl rand -hex 32)" }

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
            Write-Host "    attempt $i/$attempts failed (round $r)" -ForegroundColor Yellow
        }
        if ($r -lt $rounds) {
            $wait = Get-Random -Minimum 20 -Maximum 31
            Write-Host "    waiting ${wait}s before retry..." -ForegroundColor Yellow
            Start-Sleep -Seconds $wait
        }
    }
    throw "Upload failed after $rounds rounds: $src"
}

# SCP is unreliable for the large binary on this server.
# Pipe through SSH stdin with compression + keepalive to survive the full transfer.
function PutBinary([string]$src, [string]$dst) {
    $srcPath = Join-Path $root $src
    $keyPath = "$env:USERPROFILE\.ssh\hailsdotgo"
    # -C: compress in transit; ServerAlive* prevents the connection resetting mid-transfer
    $cmdStr  = "ssh -i ""$keyPath"" -C -o BatchMode=yes -o ServerAliveInterval=10 -o ServerAliveCountMax=60 -o TCPKeepAlive=yes $target ""cat > $dst"" < ""$srcPath"""
    $rounds  = 5
    $attempts = 3
    for ($r = 1; $r -le $rounds; $r++) {
        for ($i = 1; $i -le $attempts; $i++) {
            cmd /c $cmdStr
            if ($LASTEXITCODE -eq 0) { return }
            Write-Host "    attempt $i/$attempts failed (round $r)" -ForegroundColor Yellow
        }
        if ($r -lt $rounds) {
            $wait = Get-Random -Minimum 15 -Maximum 26
            Write-Host "    waiting ${wait}s before retry..." -ForegroundColor Yellow
            Start-Sleep -Seconds $wait
        }
    }
    throw "Binary upload failed after $rounds rounds: $src"
}

function PutDir([string]$relDir, [string]$remoteDst) {
    $srcPath = Join-Path $root $relDir
    $parent  = Split-Path $srcPath -Parent
    $leaf    = Split-Path $srcPath -Leaf
    $tmpTar  = Join-Path $env:TEMP "hailsdotgo-dir.tar.gz"
    & tar -czf $tmpTar -C $parent $leaf
    if ($LASTEXITCODE -ne 0) { throw "tar failed: $relDir" }
    Run "mkdir -p $remoteDst"
    $keyPath = "$env:USERPROFILE\.ssh\hailsdotgo"
    $cmdStr  = "ssh -i ""$keyPath"" -C -o BatchMode=yes -o ServerAliveInterval=10 -o ServerAliveCountMax=60 $target ""tar -xzf - -C $remoteDst"" < ""$tmpTar"""
    cmd /c $cmdStr
    if ($LASTEXITCODE -ne 0) { throw "Dir upload failed: $relDir" }
    Remove-Item $tmpTar -ErrorAction SilentlyContinue
}

# -- Build -----------------------------------------------------------
Write-Host "==> Building" -ForegroundColor Cyan

$tsStart = Get-Date
Set-Location $root
npm run build
Write-Host "    TypeScript    $([int]((Get-Date) - $tsStart).TotalSeconds)s" -ForegroundColor DarkGray

$goStart = Get-Date
$env:GOOS = "linux"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
go build -ldflags="-s -w" -o hailsDotGO-linux .
$env:GOOS = ""; $env:GOARCH = ""; $env:CGO_ENABLED = ""
$binSize = FmtBytes (Get-Item (Join-Path $root "hailsDotGO-linux")).Length
Write-Host "    Go binary      $binSize  $([int]((Get-Date) - $goStart).TotalSeconds)s" -ForegroundColor DarkGray

# -- Write app.env ---------------------------------------------------
$appEnvPath    = Join-Path $root "app.env"
$appEnvContent = "DB_HOST=$($script:dbHost)`nDB_USER=$($script:dbUser)`nDB_PASS=$($script:dbPass)`nDB_NAME=$($script:dbName)`nSUPERADMIN_USER=$($script:superadminUser)`nCSRF_KEY=$($script:csrfKey)`nPAYPAL_CLIENT_ID=$($script:ppClientID)`nPAYPAL_CLIENT_SECRET=$($script:ppSecret)`nPAYPAL_MODE=$($script:ppMode)`nPAYPAL_WEBHOOK_ID=$($script:ppWebhookID)`nGITHUB_TOKEN=$($script:ghToken)`nGITHUB_REPO=$($script:ghRepo)`nFCM_PROJECT_ID=$($script:fcmProjectID)`nFCM_CREDENTIALS_JSON=$($script:fcmCredJSON)`nAPNS_KEY_PATH=$($script:apnsKeyPath)`nAPNS_KEY_ID=$($script:apnsKeyID)`nAPNS_TEAM_ID=$($script:apnsTeamID)`nAPNS_BUNDLE_ID=$($script:apnsBundleID)`nAPNS_PRODUCTION=$($script:apnsProd)`n"
$utf8NoBom = New-Object System.Text.UTF8Encoding $false
[System.IO.File]::WriteAllText($appEnvPath, $appEnvContent, $utf8NoBom)

# -- Diff against manifest -------------------------------------------
$manifestPath = Join-Path $root "deploy-manifest.json"
$manifest = @{}
if (Test-Path $manifestPath) {
    $raw = Get-Content $manifestPath -Raw | ConvertFrom-Json
    $raw.PSObject.Properties | ForEach-Object { $manifest[$_.Name] = $_.Value }
}
$newManifest = @{}

function Get-LocalFileHash([string]$rel) {
    return (Get-FileHash (Join-Path $root $rel) -Algorithm SHA256).Hash
}

function DirHash([string]$relDir) {
    $files    = Get-ChildItem (Join-Path $root $relDir) -Recurse -File | Sort-Object FullName
    $combined = ($files | ForEach-Object { (Get-FileHash $_.FullName -Algorithm SHA256).Hash }) -join ""
    $bytes    = [System.Text.Encoding]::UTF8.GetBytes($combined)
    $sha      = [System.Security.Cryptography.SHA256]::Create()
    return [System.BitConverter]::ToString($sha.ComputeHash($bytes)).Replace("-", "")
}

$trackedFiles = @(
    @{ src = "hailsDotGO-linux";           dst = "/opt/hailsdotgo/hailsDotGO";            binary = $true  },
    @{ src = "static\css\main.css";        dst = "/opt/hailsdotgo/static/css/";           binary = $false },
    @{ src = "static\maintenance.html";    dst = "/opt/hailsdotgo/static/";               binary = $false },
    @{ src = "templates\base.html";        dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\home.html";        dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\raids.html";       dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\dps.html";         dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\pvp.html";         dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\events.html";      dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\iv.html";          dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\credits.html";     dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\login.html";       dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\maintenance.html"; dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\register.html";    dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\shinies.html";     dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\admin.html";       dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\settings.html";    dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\trainers.html";    dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\trainer.html";     dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\friends.html";        dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\notifications.html"; dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\raidfinder.html";  dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\store.html";       dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "templates\translate.html";   dst = "/opt/hailsdotgo/templates/";            binary = $false },
    @{ src = "hailsdotgo.service";         dst = "/opt/hailsdotgo/hailsdotgo.service";    binary = $false }
)

# Track every built JS bundle dynamically so a new esbuild entry point can
# never be silently skipped (raidfinder.js was missed by the old hardcoded list).
Get-ChildItem (Join-Path $root "static\js") -Filter *.js -File | Sort-Object Name | ForEach-Object {
    $trackedFiles += @{ src = "static\js\$($_.Name)"; dst = "/opt/hailsdotgo/static/js/"; binary = $false }
}

$trackedDirs = @(
    @{ src = "static\sprites\dreamstone"; dst = "/opt/hailsdotgo/static/sprites"; key = "static/sprites/dreamstone" },
    @{ src = "static\sprites\pex";        dst = "/opt/hailsdotgo/static/sprites"; key = "static/sprites/pex" }
)

$pending    = [System.Collections.Generic.List[object]]::new()
$pendingDirs = [System.Collections.Generic.List[object]]::new()

Write-Host "`n==> Diffing $($trackedFiles.Count) files + $($trackedDirs.Count) dir(s)" -ForegroundColor Cyan
foreach ($f in $trackedFiles) {
    $key  = $f.src.Replace('\', '/')
    $hash = Get-LocalFileHash $f.src
    $newManifest[$key] = $hash
    if ($manifest[$key] -ne $hash) {
        $f.size = FmtBytes (Get-Item (Join-Path $root $f.src)).Length
        $f.changed = $true
        $pending.Add($f)
        Write-Host "    + $($f.src.Replace('\','/'))" -ForegroundColor White
    }
}
foreach ($d in $trackedDirs) {
    $hash = DirHash $d.src
    $newManifest[$d.key] = $hash
    if ($manifest[$d.key] -ne $hash) {
        $d.changed = $true
        $fileCount = (Get-ChildItem (Join-Path $root $d.src) -Recurse -File).Count
        $pendingDirs.Add($d)
        Write-Host "    + $($d.src.Replace('\','/'))/ ($fileCount files)" -ForegroundColor White
    }
}

$skipCount   = $trackedFiles.Count - $pending.Count
$uploadCount = $pending.Count + $pendingDirs.Count

if ($skipCount -gt 0) {
    Write-Host "    $skipCount unchanged" -ForegroundColor DarkGray
}

# -- Nothing to do ---------------------------------------------------
if ($pending.Count -eq 0 -and $pendingDirs.Count -eq 0) {
    Write-Host "`n==> No changes - uploading app.env and exiting" -ForegroundColor Green
    Put "app.env" "/opt/hailsdotgo/app.env"
    Run "chmod 600 /opt/hailsdotgo/app.env"
    Remove-Item $appEnvPath -ErrorAction SilentlyContinue
    $newManifest | ConvertTo-Json | Out-File -FilePath $manifestPath -Encoding utf8
    Write-Host "    Done in $(Elapsed)" -ForegroundColor DarkGray
    exit 0
}

# -- Upload ----------------------------------------------------------
Write-Host "`n==> Uploading $uploadCount file(s)  (stopping service first)" -ForegroundColor Yellow
Run "systemctl stop hailsdotgo"

$idx = 0
foreach ($f in $pending) {
    $idx++
    Write-Host "    [$idx/$uploadCount] $($f.src.Replace('\','/'))" -ForegroundColor Cyan
    if ($f.binary) {
        PutBinary $f.src $f.dst
        Run "chmod +x /opt/hailsdotgo/hailsDotGO"
    } else {
        Put $f.src $f.dst
    }
}
foreach ($d in $pendingDirs) {
    $idx++
    $fileCount = (Get-ChildItem (Join-Path $root $d.src) -Recurse -File).Count
    Write-Host "    [$idx/$uploadCount] $($d.src.Replace('\','/'))/ ($fileCount files)" -ForegroundColor Cyan
    PutDir $d.src $d.dst
}

Write-Host "    [app.env]" -ForegroundColor Cyan
Put "app.env" "/opt/hailsdotgo/app.env"
Run "chmod 600 /opt/hailsdotgo/app.env"

$serviceChanged = $pending | Where-Object { $_.src -eq "hailsdotgo.service" }
if ($serviceChanged) {
    Write-Host "    [systemd reload]" -ForegroundColor Cyan
    Run "cp /opt/hailsdotgo/hailsdotgo.service /etc/systemd/system/hailsdotgo.service && systemctl daemon-reload"
}

Remove-Item $appEnvPath -ErrorAction SilentlyContinue

# -- Start -----------------------------------------------------------
Write-Host "`n==> Starting service" -ForegroundColor Cyan
Run "chown -R hailsdotgo:hailsdotgo /opt/hailsdotgo && systemctl start hailsdotgo"

Start-Sleep -Seconds 3
$status = (& ssh @ssh $target "systemctl is-active hailsdotgo").Trim()

if ($status -eq "active") {
    $newManifest | ConvertTo-Json | Out-File -FilePath $manifestPath -Encoding utf8
    Write-Host ""
    Write-Host "  Live: https://pogo.hails.live" -ForegroundColor Green
    Write-Host "  $uploadCount uploaded, $skipCount unchanged, $(Elapsed) total" -ForegroundColor DarkGray
    Write-Host ""
    Write-Host "==> Recent log" -ForegroundColor Cyan
    $logLines = & ssh @ssh $target "journalctl -u hailsdotgo -n 20 --no-pager"
    $warnings = [System.Collections.Generic.List[string]]::new()
    foreach ($line in $logLines) {
        if (($line -match 'fetched 0 bosses' -and $line -notmatch 'max battles') -or $line -match 'pogodata:.+refresh:') {
            Write-Host $line -ForegroundColor Yellow
            $msg = ($line -replace '^.+hailsDotGO\[\d+\]:\s+[\d/]+ [\d:]+ ', '').Trim()
            $warnings.Add($msg)
        } else {
            Write-Host $line
        }
    }
    if ($warnings.Count -gt 0) {
        Write-Host ""
        Write-Host "  !! $($warnings.Count) data warning(s):" -ForegroundColor Red
        foreach ($w in $warnings) {
            Write-Host "     $w" -ForegroundColor Yellow
        }
    }
} else {
    Write-Host ""
    Write-Host "  Service is $status - fetching logs..." -ForegroundColor Red
    & ssh @ssh $target "journalctl -u hailsdotgo -n 20 --no-pager"
    Write-Host ""
    Write-Host "  Manifest not saved. Next run will retry all changed files." -ForegroundColor Yellow
}
