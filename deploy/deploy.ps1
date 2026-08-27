# deploy.ps1 — Build hailsDotGO for Linux and upload to VPS
#
# Usage:
#   .\deploy\deploy.ps1 -VpsUser root -VpsHost 1.2.3.4
#   .\deploy\deploy.ps1 -VpsUser root -VpsHost 1.2.3.4 -SshPort 2222
#
param(
    [Parameter(Mandatory=$true)]
    [string]$VpsUser,

    [Parameter(Mandatory=$true)]
    [string]$VpsHost,

    [string]$SshPort = "22"
)

$ErrorActionPreference = "Stop"
$Target = "${VpsUser}@${VpsHost}"
$SshArgs = @("-p", $SshPort)
$ScpArgs = @("-P", $SshPort)

# ── 1. Build TypeScript ──────────────────────────────────────────────────────
Write-Host "`n==> Building TypeScript..." -ForegroundColor Cyan
npm run build
if ($LASTEXITCODE -ne 0) { throw "esbuild failed" }

# ── 2. Cross-compile Go for Linux amd64 ─────────────────────────────────────
Write-Host "`n==> Cross-compiling Go binary for Linux..." -ForegroundColor Cyan
$env:GOOS       = "linux"
$env:GOARCH     = "amd64"
$env:CGO_ENABLED = "0"
go build -ldflags="-s -w" -o hailsDotGO-linux .
$env:GOOS = ""; $env:GOARCH = ""; $env:CGO_ENABLED = ""
if ($LASTEXITCODE -ne 0) { throw "go build failed" }

# ── 3. Upload files ──────────────────────────────────────────────────────────
Write-Host "`n==> Uploading binary..." -ForegroundColor Cyan
scp @ScpArgs hailsDotGO-linux "${Target}:/opt/hailsdotgo/hailsDotGO"

Write-Host "==> Uploading templates..." -ForegroundColor Cyan
scp @ScpArgs -r templates "${Target}:/opt/hailsdotgo/"

Write-Host "==> Uploading static assets..." -ForegroundColor Cyan
scp @ScpArgs -r static "${Target}:/opt/hailsdotgo/"

# ── 4. Fix permissions and restart service ───────────────────────────────────
Write-Host "`n==> Setting permissions and restarting service..." -ForegroundColor Cyan
ssh @SshArgs $Target @"
chmod +x /opt/hailsdotgo/hailsDotGO
chown -R hailsdotgo:hailsdotgo /opt/hailsdotgo
systemctl restart hailsdotgo
systemctl is-active hailsdotgo
"@

Write-Host "`n==> Done! Visit https://pogo.hails.app" -ForegroundColor Green
