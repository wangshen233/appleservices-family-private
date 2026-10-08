# Account-isolated diagnostic Mac profiles

Apply this when the user asks for an independent Mac identity for each account.

## Verified legacy issues in this project

- `nativeadi/helper.py` formerly assigned the same fixed locally administered network address to every generated Mac profile. `mac-nac-check.py` used that address as diagnostic ROM when ROM was absent. UUIDs, serials and ADI states were already separate, but ROM/network identity was shared.
- `cmd/trusted-device` formerly forced the installed Windows APS daemon whenever `-native-adi` was selected. Its token was shared by all accounts. A distinct UUID and ADI MID did not fix that push identity reuse.

## Repair and verification

1. Retain a previously accepted account's configuration and private success artifacts. Prepare owner-only copies for the affected accounts, backed up before installation. Use each account's own UUID, serial and hardware; never copy another account's credentials.
2. The repository's `nativeadi.SyntheticMACAddress` derives a stable local unicast address from the device identifier. Use it for explicitly diagnostic profiles. Fill missing ROM/MLB only under the approved synthetic diagnostic mode, derive corresponding encrypted fields, and record `appleservices_diagnostic.synthetic_board_fields=true`. MAC, ROM, MLB and their encrypted values must agree.
3. Reprovision ADI in each isolated copy and verify it with `accountsetupcheck -native-profile <private-copy> -native-only`. This does not log in, request SMS or prove account activation.
4. `tools/mac-push-check.py --profile <private-copy> --library <pinned-library> --go-state-out <new-private-push.json>` uses the verified module's Mac APS setup, with no account credentials. It creates a certificate/token for a profile lacking its own keypair and reuses that pair on later runs. It prints statuses and lengths only. Never manually invent a push token.
5. `tools/macpushcheck -dir <bound-account> -profile <private-copy> -state <private-push.json>` verifies certificate/key agreement, token/device binding, direct Go courier connection and token stability. Preserve the Mac APS keypair when updating profile state; do not replace it with a Windows daemon token.
6. Stop only the affected project's worker before installing a verified profile. Install the matching native state, hardware, account `push.json` and regenerated NAC. Keep rollback copies. Do not alter a previously successful account to make it look different.
7. Recheck separation using equality booleans rather than raw identifiers: UUID, serial, MAC, ROM, MLB, ADI MID, certificate/key and APS token. One Mac model can legitimately be used by multiple independent profiles.

The worker now uses account `push.json` for Mac ADI by default; Windows daemon push requires an explicit `-native-push` selection. APS connection, independent fingerprints and NAC generation remain partial evidence. Complete GSA/2FA, account terms if required, v2 delegate and read-only quota verification before declaring activation.
