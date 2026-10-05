param(
    [string]$PackagePath = (Join-Path $PSScriptRoot '.cache\openbubbles-store.msix')
)

$ErrorActionPreference = 'Stop'
$taskExpectedHash = 'ca88e816dda449d7ba24d54a5ed709a2d392daeae28c25a61dcd2e7496e44cfe'
$taskPublisher = 'CN=A7258527-3356-42FA-AABD-F6BAA9420BDE'
$taskPackage = (Resolve-Path -LiteralPath $PackagePath).Path
$taskSignature = Get-AuthenticodeSignature -LiteralPath $taskPackage
if ($taskSignature.Status -ne 'Valid' -or $taskSignature.SignerCertificate.Subject -ne $taskPublisher) {
    throw 'The OpenBubbles Store package signature or publisher did not match.'
}

Add-Type -AssemblyName System.IO.Compression.FileSystem
$taskArchive = [IO.Compression.ZipFile]::OpenRead($taskPackage)
$taskTempPath = $null
try {
    $taskManifestEntry = $taskArchive.GetEntry('AppxManifest.xml')
    if (-not $taskManifestEntry) { throw 'Package manifest unavailable.' }
    $taskReader = [IO.StreamReader]::new($taskManifestEntry.Open())
    try { [xml]$taskManifest = $taskReader.ReadToEnd() } finally { $taskReader.Dispose() }
    $taskIdentity = $taskManifest.Package.Identity
    if ($taskIdentity.Name -ne 'OpenBubbles.OpenBubbles' -or $taskIdentity.Version -ne '1.18.800.0' -or
        $taskIdentity.ProcessorArchitecture -ne 'x64' -or $taskIdentity.Publisher -ne $taskPublisher) {
        throw 'This bridge requires the exact OpenBubbles 1.18.800.0 x64 Store package.'
    }
    $taskEntries = @($taskArchive.Entries | Where-Object { $_.Name -eq 'rust_lib_bluebubbles.dll' })
    if ($taskEntries.Count -ne 1) { throw 'Native module missing or ambiguous.' }
    $taskDirectory = Join-Path $PSScriptRoot '.cache\openbubbles-store'
    New-Item -ItemType Directory -Force -Path $taskDirectory | Out-Null
    $taskTempPath = Join-Path $taskDirectory (([guid]::NewGuid().ToString()) + '.tmp')
    [IO.Compression.ZipFileExtensions]::ExtractToFile($taskEntries[0], $taskTempPath, $false)
    if ((Get-FileHash -LiteralPath $taskTempPath -Algorithm SHA256).Hash.ToLowerInvariant() -ne $taskExpectedHash) {
        throw 'Native module hash mismatch. No executable module was installed.'
    }
    $taskDestination = Join-Path $taskDirectory 'rust_lib_bluebubbles.dll'
    if ((Test-Path -LiteralPath $taskDestination) -and
        (Get-FileHash -LiteralPath $taskDestination -Algorithm SHA256).Hash.ToLowerInvariant() -eq $taskExpectedHash) {
        Write-Host 'Signature and module hash verified; the local module is already ready.'
    } else {
        Move-Item -LiteralPath $taskTempPath -Destination $taskDestination -Force
        $taskTempPath = $null
        Write-Host 'Signature and module hash verified; the local module is ready.'
    }
} finally {
    $taskArchive.Dispose()
    if ($taskTempPath -and (Test-Path -LiteralPath $taskTempPath)) { Remove-Item -LiteralPath $taskTempPath }
}
