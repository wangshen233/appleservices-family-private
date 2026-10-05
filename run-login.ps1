param(
    [string]$AppleID,
    [ValidateSet('trusted-device', 'sms')]
    [string]$TwoFactor = 'trusted-device',
    [string]$StateDir = (Join-Path $PSScriptRoot 'state'),
    [string]$Anisette,
    [switch]$Logout
)

$ErrorActionPreference = 'Stop'
$taskExecutable = Join-Path $PSScriptRoot 'bin\login.exe'
if (-not (Test-Path -LiteralPath $taskExecutable)) {
    New-Item -ItemType Directory -Force -Path (Join-Path $PSScriptRoot 'bin') | Out-Null
    Push-Location $PSScriptRoot
    try {
        & go build -o $taskExecutable ./cmd/login
        if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }
    } finally {
        Pop-Location
    }
}

$taskArgs = @('-dir', $StateDir, '-twofactor', $TwoFactor)
if ($Anisette) { $taskArgs += @('-anisette', $Anisette) }
if ($Logout) {
    & $taskExecutable @taskArgs -logout
    if ($LASTEXITCODE -ne 0) { throw 'Clearing the local session failed.' }
    return
}
if (-not $AppleID) { $AppleID = Read-Host 'Apple ID' }
$taskPassword = Read-Host 'Apple ID password' -AsSecureString
$taskOldPassword = $env:APPLESERVICES_PASSWORD
$taskBstr = [IntPtr]::Zero
try {
    $taskBstr = [System.Runtime.InteropServices.Marshal]::SecureStringToBSTR($taskPassword)
    $env:APPLESERVICES_PASSWORD = [System.Runtime.InteropServices.Marshal]::PtrToStringBSTR($taskBstr)
    & $taskExecutable @taskArgs -appleid $AppleID
    if ($LASTEXITCODE -ne 0) { throw 'Login failed. See the error above.' }
} finally {
    $env:APPLESERVICES_PASSWORD = $taskOldPassword
    if ($taskBstr -ne [IntPtr]::Zero) {
        [System.Runtime.InteropServices.Marshal]::ZeroFreeBSTR($taskBstr)
    }
    $taskPassword.Dispose()
}
