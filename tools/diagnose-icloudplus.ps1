param(
    [string]$LogRoot = "$env:LOCALAPPDATA\Packages\AppleInc.iCloud_nzyj5cx40ttqa\LocalCache\Local\Logs",
    [ValidateRange(1,168)][int]$SinceHours = 24,
    [switch]$Json
)
$ErrorActionPreference = 'Stop'
if (-not (Test-Path -LiteralPath $LogRoot)) { throw 'Official iCloud log directory not found.' }
$cutoff = [DateTime]::UtcNow.AddHours(-$SinceHours)
$culture = [Globalization.CultureInfo]::InvariantCulture
$events = @(foreach ($file in Get-ChildItem -LiteralPath $LogRoot -Recurse -File |
    Where-Object { $_.Name -match '^(AOSKit-)?iCloudHome\..*\.log$' -and $_.LastWriteTimeUtc -ge $cutoff }) {
    $stamp = $null
    $number = 0
    foreach ($line in Get-Content -LiteralPath $file.FullName) {
        $number++
        if ($line -match '@ \w+ (?<stamp>\w{3} \d{2} \d{4} \d{2}:\d{2}:\d{2}\.\d{3})\]') {
            $parsed = [DateTime]::MinValue
            if ([DateTime]::TryParseExact($Matches.stamp, 'MMM dd yyyy HH:mm:ss.fff', $culture,
                [Globalization.DateTimeStyles]::AssumeLocal, [ref]$parsed)) { $stamp = $parsed.ToUniversalTime() }
        }
        if (-not $stamp -or $stamp -lt $cutoff) { continue }
        $kind = ''
        $status = $null
        $code = $null
        if ($line -match 'MOBILEME_TERMS_OF_SERVICE_UPDATE|TOS not accepted') { $kind = 'terms_update_required' }
        elseif ($line -match 'httpStatusCode=(?<status>\d{3})') { $kind = 'http_response'; $status = [int]$Matches.status }
        elseif ($line -match '"HttpStatusCode".*value = \+(?<status>\d{3})') { $kind = 'http_response'; $status = [int]$Matches.status }
        elseif ($line -match 'Account needs validation') { $kind = 'account_validation_required' }
        elseif ($line -match 'trusted Device list.*code: (?<status>\d{3})') { $kind = 'device_list_response'; $status = [int]$Matches.status }
        elseif ($line -match 'Account info refreshed\.') { $kind = 'account_info_refreshed' }
        elseif ($line -match 'AKValidateAppleIDCredentials returned (?<code>-?\d+)') { $kind = 'authkit_result'; $code = [int]$Matches.code }
        elseif ($line -match 'Heartbeat token is empty') { $kind = 'authkit_heartbeat_missing' }
        elseif ($line -match 'Successfully stored heartbeat token') { $kind = 'authkit_heartbeat_saved' }
        elseif ($line -match 'Successfully stored identity token') { $kind = 'authkit_identity_saved' }
        elseif ($line -match 'Failed to store (heartbeat|identity) token.*error: (?<code>-?\d+)') { $kind = 'authkit_token_save_failed'; $code = [int]$Matches.code }
        elseif ($line -match 'Silent reauth failed or we need full UI for 2FA code') { $kind = 'authkit_interactive_verification_required' }
        elseif ($line -match 'Re-authentication with ck has failed') { $kind = 'authkit_ck_reauthentication_failed' }
        elseif ($line -match 'Verification completion: User canceled.*Err:(?<code>-?\d+)') { $kind = 'authkit_verification_canceled'; $code = [int]$Matches.code }
        elseif ($line -match 'ExceptionWasThrown.*buy\.itunes\.apple\.com') { $kind = 'purchase_page_script_error' }
        if ($kind) {
            # Metadata only: never output raw lines, URLs or authentication values.
            [pscustomobject]@{time_utc=$stamp.ToString('o');file=$file.Name;line=$number;kind=$kind;http_status=$status;code=$code}
        }
    }
})
$events = @($events | Sort-Object time_utc -Descending)
$report = [pscustomobject]@{
    generated_at_utc = [DateTime]::UtcNow.ToString('o')
    since_hours = $SinceHours
    terms_update_reported = [bool]($events | Where-Object kind -eq 'terms_update_required')
    unauthorized_reported = [bool]($events | Where-Object http_status -eq 401)
    purchase_script_error_reported = [bool]($events | Where-Object kind -eq 'purchase_page_script_error')
    events = @($events | Select-Object -First 40)
}
if ($Json) { $report | ConvertTo-Json -Depth 5; exit 0 }
$report.events | Format-Table -AutoSize
if ($report.terms_update_reported) {
    Write-Output 'Apple reported an iCloud terms update. Finish the official terms flow before retrying account refresh.'
}
if ($report.unauthorized_reported) { Write-Output 'An official iCloud request returned HTTP 401 within this time range.' }
Write-Output 'These are dated observations, not proof of current login state or the cause of the purchase-page HTTP 400.'
Write-Output 'Read-only diagnostic. No account identifiers or authentication values are printed.'
