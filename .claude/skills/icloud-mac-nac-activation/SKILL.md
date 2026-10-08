---
name: icloud-mac-nac-activation
description: Diagnose or validate Apple iCloud Mac v2/NAC activation from Windows using background protocol work, native ADI, and open-source NAC emulation. Use when the task involves MME tokens, iCloud delegate login, Apple validation data, or Windows-only Mac-style iCloud activation.
metadata:
  short-description: Validate iCloud Mac v2/NAC activation from Windows
---

# iCloud Mac v2/NAC activation

Use this skill for protocol-level iCloud activation work from Windows. The target result is an Apple-accepted `mmeAuthToken` followed by a successful read-only quota request.

## Boundaries

- Work through installed binaries, local APIs, source, and protocol clients. Do not launch or control the official iCloud GUI.
- Never put Apple passwords, SMS codes, PETs, MME tokens, cookies, raw authenticated responses, or credential-bearing URLs in logs, documentation, commits, or chat.
- Keep private responses and checkpoints in owner-only files. Refuse to overwrite an existing private output unless the caller explicitly chooses a new path.
- Do not use donor Mac hardware, another person’s identifiers, or silently invent ROM/MLB values. Synthetic hardware is allowed only in an explicitly labelled diagnostic run and cannot be reported as an official device identity.
- Treat GSA password success, device registration, terms acceptance, HTTP 200, NAS header presence, and a Store handoff as partial results. Activation requires both a real MME token and a successful read-only quota response.
- Do not submit a purchase unless the user separately authorizes it. A quota result does not prove a purchase.

## Workflow

1. **Map the current state.** Identify the account directory, device identity, native ADI profile, saved GSA session, accepted terms state, and any prior delegate response. Exclude archived or unrelated accounts. Preserve uncommitted changes.
   - When the user requires a separate Mac per account, compare UUID, serial, network address, effective ROM/MLB, ADI MID and APS token between the selected accounts. Different UUIDs alone do not establish isolation. Matching model/board names are normal for separate instances of one Mac model.
   - Read [references/mac-isolation.md](references/mac-isolation.md) for this project's legacy shared-address/shared-Windows-push issue and the verified repair path.
2. **Use the right implementation mode.**
   - If the repository already has `accountsetupcheck`, `mac-nac-check.py`, `nacexchange`, or equivalent tools, use them and inspect their source before changing behavior.
   - Otherwise inspect installed Windows iCloud/CoreADI/AOSKit binaries read-only and build a small isolated probe.
3. **Validate Windows ADI separately.** Load only the installed/staged `CoreADI64.dll` through its dispatcher. Check production machine provisioning and retrieve machine MID/OTP. Report status and lengths, never raw values. Native anisette proves machine data generation, not iCloud activation.
4. **Generate Mac NAC validation data.**
   - Obtain Apple’s validation certificate through a verified Apple-root TLS chain.
   - Run the open-source x86_64 `IMDAppleServices` emulation with an explicit hardware profile.
   - If plaintext hardware fields exist but `_enc` fields are missing, derive them with the pinned XNU property routine and verify all upstream known-answer vectors before use.
   - Perform `NACInit`, Apple session-info exchange, `NACKeyEstablishment`, and `NACSign` on the same context. Save the resulting bytes privately and record only length, hash, and whether synthetic fields were used.
5. **Submit the Mac v2 delegate request.** Use a fresh GSA login/PET for the account and the same native identity. For `/setup/signin/v2/login`:
   - serialize outer keys as `protocolVersion` and `userInfo`;
   - keep nested `client-id` kebab-case;
   - send `X-Mme-Nas-Qualify` as base64 validation data;
   - preserve the provider model/OS in `X-Mme-Client-Info` and use the iCloudHelper consumer UA;
   - send `termsAccepted=true` only after Apple previously returned explicit terms success and the user authorized acceptance.
6. **Verify the result.** Parse gzip before plist decoding. Require top-level and `com.apple.mobileme` status 0, a nonempty `mmeAuthToken`, and then a successful quota read. Save a private accepted checkpoint only when explicitly requested.
   - Inspect a refused 2FA response privately before interpreting an HTTP code. If Apple explicitly returns `tooManyCodesSent=true` / `-22981`, stop requesting SMS; do not guess a recovery time. `noTrustedDevices=true` does not authorize replacing SMS with a locally generated device code.
7. **Separate results.** Report independent evidence for GSA, terms, NAC, delegate, quota, and worker integration. A standalone probe can succeed while a dashboard worker still fails because its cached session, registration, serial override, cookie, or headers differ.

## Repository integration hints

For this project, the useful diagnostic variables are:

```text
APPLESERVICES_DELEGATE_PATH=v2
APPLESERVICES_NAC_VALIDATION_FILE=<private validation-data file>
APPLESERVICES_TERMS_ACCEPTED=1             # only after verified terms success
APPLESERVICES_FRESH_GSA=1                  # isolated comparison only
APPLESERVICES_SKIP_DEVICE_REGISTRATION=1   # isolated delegate comparison only
```

`APPLESERVICES_FRESH_GSA` and `APPLESERVICES_SKIP_DEVICE_REGISTRATION` are comparison controls, not general fixes. Keep the successful standalone `accountsetupcheck` evidence separate from the strict worker result.

Read [references/open-source.md](references/open-source.md) when choosing an implementation. Read [references/acceptance.md](references/acceptance.md) when deciding whether a result counts as activation.
