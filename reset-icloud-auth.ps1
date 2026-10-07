[CmdletBinding(SupportsShouldProcess)]
param(
    [switch]$Restart
)

$ErrorActionPreference = 'Stop'
$packageFamily = 'AppleInc.iCloud_nzyj5cx40ttqa'
$publisher = Join-Path $env:LOCALAPPDATA 'Publishers\nzyj5cx40ttqa\com.apple.AuthKitWin'
$localCache = Join-Path $env:LOCALAPPDATA "Packages\$packageFamily\LocalCache\Local"
$backupRoot = Join-Path $PSScriptRoot 'state\icloud-reset-backups'
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$backup = Join-Path $backupRoot $stamp

Write-Host "Stopping official iCloud processes..."
Get-Process iCloudHome,iCloudCKKS,APSDaemon -ErrorAction SilentlyContinue |
    Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 2

New-Item -ItemType Directory -Force -Path $backup | Out-Null

function Backup-And-Remove([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) { return }
    $relative = $Path.Replace($env:LOCALAPPDATA, '').TrimStart('\/:')
    $dest = Join-Path $backup $relative
    $parent = Split-Path -Parent $dest
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
    if ($PSCmdlet.ShouldProcess($Path, 'Move to rollback backup')) {
        Move-Item -LiteralPath $Path -Destination $dest -Force
        Write-Host "Backed up: $Path"
    }
}

# ckd.dat is the AuthKit publisher cache that held the stale account session.
Backup-And-Remove (Join-Path $publisher 'ckd.dat')
Backup-And-Remove (Join-Path $publisher 'ckd.lck')

# WebView authentication state can preserve a failed login page/session.
$webview = Join-Path $localCache 'WebViewUserDataDirs'
Backup-And-Remove $webview

Write-Host "Backup created at: $backup"
if ($Restart) {
    Start-Process explorer.exe -ArgumentList "shell:AppsFolder\$packageFamily!iCloud"
    Write-Host 'iCloud restarted. Complete the normal sign-in in its window.'
} else {
    Write-Host 'Run with -Restart to launch iCloud after the reset.'
}
