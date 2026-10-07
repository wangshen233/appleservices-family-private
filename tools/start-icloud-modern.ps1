param([switch]$Restart, [ValidateRange(1024,65535)][int]$DiagnosticPort = 0)
$ErrorActionPreference = 'Stop'
$package = Get-AppxPackage AppleInc.iCloud | Select-Object -First 1
if (-not $package) { throw 'Official iCloud for Windows is not installed for this user.' }
$officialExe = Join-Path $package.InstallLocation 'iCloud\iCloudHome.exe'
$runtimeRoot = Join-Path ${env:ProgramFiles(x86)} 'Microsoft\EdgeWebView\Application'
$runtime = Get-ChildItem -LiteralPath $runtimeRoot -Directory | Where-Object {
    $_.Name -match '^\d+\.\d+\.\d+\.\d+$' -and (Test-Path -LiteralPath (Join-Path $_.FullName 'msedgewebview2.exe'))
} | Sort-Object { [version]$_.Name } -Descending | Select-Object -First 1
if (-not $runtime) { throw 'An installed Microsoft WebView2 runtime is required.' }
$signature = Get-AuthenticodeSignature -FilePath (Join-Path $runtime.FullName 'msedgewebview2.exe')
if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch 'O=Microsoft Corporation') {
    throw 'Microsoft WebView2 signature validation failed.'
}
$running = @(Get-Process iCloudHome -ErrorAction SilentlyContinue | Where-Object Path -eq $officialExe)
if ($running -and -not $Restart) { throw 'iCloud is running. Use -Restart to restart only its main interface.' }
foreach ($process in $running) { Stop-Process -Id $process.Id -Force }
# Scope the override to this launch; keep the old WebView profile untouched so
# a normal launch can still use the bundled runtime and original profile.
$profile = Join-Path $PSScriptRoot '..\state\icloud-modern-webview'
$profile = [IO.Path]::GetFullPath($profile)
$oldBrowser = $env:WEBVIEW2_BROWSER_EXECUTABLE_FOLDER
$oldProfile = $env:WEBVIEW2_USER_DATA_FOLDER
$oldArguments = $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS
try {
    $env:WEBVIEW2_BROWSER_EXECUTABLE_FOLDER = $runtime.FullName
    $env:WEBVIEW2_USER_DATA_FOLDER = $profile
    if ($DiagnosticPort) {
        $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = '--remote-debugging-address=127.0.0.1 --remote-debugging-port=' + $DiagnosticPort
    }
    Start-Process -FilePath $officialExe -WorkingDirectory (Split-Path $officialExe) -WindowStyle Hidden | Out-Null
} finally {
    $env:WEBVIEW2_BROWSER_EXECUTABLE_FOLDER = $oldBrowser
    $env:WEBVIEW2_USER_DATA_FOLDER = $oldProfile
    $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = $oldArguments
}
Write-Output ('iCloud launched with a process-scoped WebView2 override: ' + $runtime.Name)
Write-Output 'Open its window normally. Runtime use and account verification still need to be checked.'
