param(
    [string]$AppleID,
    [string]$StateDir = (Join-Path $PSScriptRoot 'state'),
    [string]$ADILibrary = (Join-Path $PSScriptRoot '.cache\openbubbles-store\rust_lib_bluebubbles.dll'),
    [string]$Python = 'python',
    [switch]$Check
)

$ErrorActionPreference = 'Stop'
$taskExecutable = Join-Path $PSScriptRoot 'bin\trusted-device.exe'
New-Item -ItemType Directory -Force -Path (Join-Path $PSScriptRoot 'bin') | Out-Null
Push-Location $PSScriptRoot
try {
    & go build -o $taskExecutable ./cmd/trusted-device
    if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }
} finally { Pop-Location }

$taskArgs = @('-native-adi', '-dir', $StateDir, '-adi-dll', $ADILibrary, '-python', $Python, '-twofactor', 'sms')
if ($Check) {
    & $taskExecutable @taskArgs -check
    if ($LASTEXITCODE -ne 0) { throw 'Local input check failed.' }
    return
}
if (-not $AppleID) { $AppleID = Read-Host 'Apple ID' }
$taskPassword = Read-Host 'Apple ID password' -AsSecureString
$taskOldAppleID = $env:APPLESERVICES_APPLEID
$taskOldPassword = $env:APPLESERVICES_PASSWORD
$taskBstr = [IntPtr]::Zero
try {
    $taskBstr = [System.Runtime.InteropServices.Marshal]::SecureStringToBSTR($taskPassword)
    $env:APPLESERVICES_APPLEID = $AppleID
    $env:APPLESERVICES_PASSWORD = [System.Runtime.InteropServices.Marshal]::PtrToStringBSTR($taskBstr)
    & $taskExecutable @taskArgs
    if ($LASTEXITCODE -ne 0) { throw 'Trusted device stopped. See the error above.' }
} finally {
    $env:APPLESERVICES_APPLEID = $taskOldAppleID
    $env:APPLESERVICES_PASSWORD = $taskOldPassword
    if ($taskBstr -ne [IntPtr]::Zero) { [System.Runtime.InteropServices.Marshal]::ZeroFreeBSTR($taskBstr) }
    $taskPassword.Dispose()
}
