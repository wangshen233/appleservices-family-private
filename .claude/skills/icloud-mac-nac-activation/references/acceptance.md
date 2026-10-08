# Acceptance checklist

A run is **activated** only if all of these are true for the same account, fresh PET, and same device identity:

1. GSA password authentication completed, with any required 2FA accepted.
2. Terms update, if required, returned explicit `success=true`; the terms cookie is added only after that acknowledgement.
3. NAC validation completed on one context: certificate, `NACInit`, Apple session-info response, `NACKeyEstablishment`, and `NACSign`.
4. v2 delegate response parsed successfully, including gzip decoding when necessary.
5. Top-level status is 0.
6. `com.apple.mobileme` status is 0.
7. `service-data.tokens.mmeAuthToken` is present and nonempty.
8. A read-only storage/quota request succeeds with that MME token.

Report each failed or untested item separately. Do not call a run successful because an earlier standalone probe succeeded if the actual worker used a different cached session or headers.

## Existing trusted-device 2FA and accepted-session checks

- When the owner cannot use SMS and the account has an existing trusted device, `accountsetupcheck -trusted-device-code -sms-code-file <new-private-file>` requests a device code and submits it through the trusted-device verifier. The file flag also serves device mode; it does not imply SMS. Do not combine this with SMS request flags, phone ID or local generated code. Delete the supplied code after use.
- After code verification, Apple may issue a PET directly in response headers. Use that completed GSA checkpoint to evaluate terms and delegate acceptance before replacing it with another login. A later new login can yield different delegate results; keep the evidence for each session separate.
- `accountsetupcheck -icloud-resume <accepted-private-checkpoint> -storage-plan` verifies the accepted MME by reading quota, then performs a GET-only offer lookup. It requires the same account directory and native identity; it performs no new GSA login, delegate exchange, terms acceptance, Store form submission or payment. A failure does not trigger an automatic purchase or token refresh.
- `-delegate-only -storage-plan` also reads offers after successful delegate and quota. An available plan proves an offer was returned, not that Store authentication, payment or purchase would succeed.
- A current-device detail with `trustedDevice=true` and matching local serial establishes the device's reported trust. `supportsVerificationCodes` and `pushDevice` are separate flags; test actual code validation and push delivery only when requested, and do not infer those capabilities from trust alone.

Useful failure interpretations:

- `ICLOUD_INVALID_DEVICE_TOKEN` / reason 3005: request identity, token, body, or validation data is not accepted; do not claim a unique cause.
- `ICLOUD_UNSUPPORTED_DEVICE` / reason 3001: compare standalone and worker headers, terms cookie, GSA freshness, serial override, device registration, and endpoint profile.
- gzip response followed by “not a plist”: decompress before parsing; preserve the raw response privately.
