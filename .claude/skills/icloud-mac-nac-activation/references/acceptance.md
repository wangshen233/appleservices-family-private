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

Useful failure interpretations:

- `ICLOUD_INVALID_DEVICE_TOKEN` / reason 3005: request identity, token, body, or validation data is not accepted; do not claim a unique cause.
- `ICLOUD_UNSUPPORTED_DEVICE` / reason 3001: compare standalone and worker headers, terms cookie, GSA freshness, serial override, device registration, and endpoint profile.
- gzip response followed by “not a plist”: decompress before parsing; preserve the raw response privately.
