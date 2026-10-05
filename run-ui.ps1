param(
    [string]$StateDir = (Join-Path $PSScriptRoot 'state'),
    [string]$ADILibrary = (Join-Path $PSScriptRoot '.cache\openbubbles-store\rust_lib_bluebubbles.dll'),
    [string]$Python = 'python',
    [ValidateRange(1024,65535)][int]$Port = 18741,
    [switch]$NoOpen
)

$ErrorActionPreference = 'Stop'
$StateDir = [IO.Path]::GetFullPath($StateDir)
$ADILibrary = [IO.Path]::GetFullPath($ADILibrary)
$taskURL = 'http://127.0.0.1:' + $Port
$taskExisting = $null
try { $taskExisting = Invoke-WebRequest -Uri $taskURL -UseBasicParsing -TimeoutSec 2 } catch { }
if ($taskExisting) {
    if ($taskExisting.Headers['X-AppleServices-UI'] -ne '1') { throw 'The selected port belongs to another application.' }
    Write-Host ('Dashboard already running: ' + $taskURL)
    if (-not $NoOpen) { Start-Process $taskURL }
    return
}
$taskDashboard = Join-Path $PSScriptRoot 'bin\dashboard.exe'
$taskWorker = Join-Path $PSScriptRoot 'bin\trusted-device-gui.exe'
New-Item -ItemType Directory -Force -Path (Join-Path $PSScriptRoot 'bin') | Out-Null
New-Item -ItemType Directory -Force -Path $StateDir | Out-Null
Push-Location $PSScriptRoot
try {
    & go build -o $taskWorker ./cmd/trusted-device
    if ($LASTEXITCODE -ne 0) { throw 'Worker build failed.' }
    & go build -o $taskDashboard ./cmd/dashboard
    if ($LASTEXITCODE -ne 0) { throw 'Dashboard build failed.' }
} finally { Pop-Location }
$taskArgs = @('-dir',$StateDir,'-worker',$taskWorker,'-adi-dll',$ADILibrary,'-python',$Python,'-port',[string]$Port)
$taskQuotedArgs = @($taskArgs | ForEach-Object { '"' + (($_ -replace '(\\*)"', '$1$1\"') -replace '(\\+)$', '$1$1') + '"' })
$taskProcess = Start-Process -FilePath $taskDashboard -ArgumentList ($taskQuotedArgs -join ' ') `
    -WorkingDirectory $PSScriptRoot -WindowStyle Hidden -PassThru `
    -RedirectStandardOutput (Join-Path $StateDir 'dashboard.out.log') `
    -RedirectStandardError (Join-Path $StateDir 'dashboard.err.log')
$taskDeadline = [DateTime]::UtcNow.AddSeconds(10)
while ([DateTime]::UtcNow -lt $taskDeadline) {
    if ($taskProcess.HasExited) { throw 'Dashboard startup failed. Check state/dashboard.err.log.' }
    try { $taskReady = Invoke-WebRequest -Uri $taskURL -UseBasicParsing -TimeoutSec 1 } catch { $taskReady = $null }
    if ($taskReady -and $taskReady.Headers['X-AppleServices-UI'] -eq '1') {
        Write-Host ('Dashboard ready: ' + $taskURL)
        if (-not $NoOpen) { Start-Process $taskURL }
        return
    }
    Start-Sleep -Milliseconds 200
}
throw 'Dashboard is not responding yet. Check the startup log.'
