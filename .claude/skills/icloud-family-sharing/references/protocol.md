# Family Sharing protocol evidence

This reference records the currently verified request shapes and the remaining implementation boundary.

## Current URL bag

Fetch `https://setup.icloud.com/configurations/init?context=settings` with the bound Mac ADI headers. On the tested Windows route, HTTP/2 stalled while HTTP/1.1 returned the Apple plist; the client pins ALPN to `http/1.1` for this bag request.

Relevant keys observed on 2026-10-29 include:

- `getFamilyDetails` → `/setup/familykit/getFamilyDetails`
- `checkFamilyEligibility` → `/setup/family/checkEligibility`
- `addFamilyMember` → `/setup/familykit/macos/addFamilyMember`
- `removeFromFamily` → `/setup/family/removeFromFamily`
- `deleteFamily` → `/setup/family/v2/ios/deleteFamily`
- `acceptFamilyInvitation` / `rejectFamilyInvitation`
- `updateFamilyMember`, `updateFamilyMemberFlags`, and invitation cancellation endpoints

Always use the current bag value and validate that it is Apple HTTPS. These paths can change.

## Authentication and Fresno binding

Apple's current `FAFamilyDetailsRequest` adds:

- Basic iCloud authorization using numeric iCloud DSID and `mmeAuthToken`;
- native ADI/anisette headers and AppleAccount client identity;
- `X-MMe-LoggedIn-AppleID`, `X-MMe-FMFAllowed`, client time and optional APS token;
- GrandSlam iCloud and family tokens;
- a plist Fresno payload containing `iCloudAccountDetails` and `iTunesAccountDetails`, each bound to Apple ID, DSID, AltDSID and `dsFamilyToken`.

The response fields include `family-members`, `family-invitations`, `pending-members`, `transfer-requests` and `family-meta-info`. Members carry organizer, self, child-account, purchase-sharing and ask-to-buy flags. Treat names, Apple IDs and DSIDs as private.

The repository has verified the configuration bag and eligibility request live. Three tested accounts returned `eligible=true` and no current family details. A successful live member-list response still requires an account that actually belongs to a family.

## Verified organizer creation and iCloud+ sharing

On 2026-10-29, the current `setupFamilyUI` resource exposed the native macOS family client contract. Family creation is:

```text
POST https://setup.icloud.com/setup/mac/family/createFamily
Content-Type: application/json

organizerAppleId=<current Apple Account>
organizerAppleIdForPurchases=<same current Apple Account>
organizerShareMyLocationEnabledDefault=false
organizerShareMyPurchasesEnabledDefault=false
```

Bind both account fields to the current session; never accept a caller-supplied organizer. After HTTP 200, re-read family details and require exactly one current member with `member-is-organizer=true` and `is-me=true`, with no invitation or pending member. This creates an empty organizer family and deliberately leaves purchase and location sharing off.

The iCloud+ detail page exposes a separate storage-sharing mutation:

```text
POST https://setup.icloud.com/setup/family/v2/macos/updateStorage
Content-Type: application/json

action=START_FAMILY_SHARING
path=APP_ICLOUD_STORAGE
familyId=<family-id from current family details>
organizerDsId=<member-dsid of current self organizer>
```

Apple may decode `member-dsid` as an integer. Accept only a positive decimal value and bind it to the unique member for which both `is-me` and `member-is-organizer` are true. Do not reuse an older family ID or DSID. Before the mutation, `/setup/family/v2/macos/storage` should show the current commerce plan and purchaser state. Completion requires a fresh response with `isParticipatingInFamilyStorage=true` and `isEnabled=true`, and a fresh `getFamilySubscriptions` response placing `ICLOUD_STORAGE` in the shared group. Enabling iCloud+ family storage does not authorize App Store Purchase Sharing.

## Verified local-pool member flow

Apple's `add-member-flow` source map defines the in-person request builder. `authenticateUser` must run ephemerally in the organizer's native Mac context; a PET issued under the target's independent device context, or the raw password copied directly into `addFamilyMember`, is rejected as an incorrect account/password. The verified sequence is:

```text
ephemeral GSA login:
  device identity = organizer Mac ADI and APS token
  account/password = target pool account
  cookies = target's current GSA cookies
  2FA = one fresh trusted-device code in the same ephemeral session

POST /setup/mac/family/addFamilyMember
  appleId=<target>
  password=<PET from the ephemeral response>
  credentialSource=pet
  shareMyLocationEnabledDefault=false
  shareMyPurchasesEnabledDefault=false
  appleIdForPurchases=<target>
  preferredAppleId=<target>
```

Before this mutation, `checkiCloudMember` must confirm a valid iCloud account, accepted terms, and `skipPaymentVerification=true`; stop for PVK, CVV, child-account or transfer requirements. Completion requires a fresh organizer view with the target present and a fresh target view with `is-me=true`.

The target's free storage is a separate opt-in. Apple's `storage-sharing` source map defines:

```text
POST /setup/family/v2/macos/updateStorage
  action=JOIN_FAMILY
  path=APP_ICLOUD_STORAGE
  familyId=<current target family ID>
  organizerDsId=<current unique organizer DSID>
```

Require a fresh target storage response with `isParticipatingInFamilyStorage=true`, `familyStorageAvailable=true`, `isEnabled=true`, and the shared plan size. Do not infer participation merely from family membership or `familyStorageAvailable=true`.

The verified Windows implementation now generates the FamilyKit 2FA code only after `RequestTrustedDeviceCode` succeeds, using the target account directory’s own `device.json`, `native-adi` profile and APS token. The code stays in process memory and is submitted to the same ephemeral session. `family-code.request` plus `family-code.handoff` remains the fallback when local generation is unavailable.

`addFamilyMember` has been observed committing server-side while the POST response exceeded the client deadline. On any transport error, do not replay the POST. Re-read organizer family details with a fresh bounded context and require the exact target Apple ID, then read from the target account and require `is-me=true`. Only those current reads convert an indeterminate transport result into business success.

The helper’s `-safe-json` output is a single `FAMILY_STATUS_JSON=` record containing only counts, booleans, role and storage capacity. It must never include Apple IDs, names, DSIDs, family IDs, invite codes, tokens, cookies or raw responses. Dashboard and worker integrations parse only this marked record.

`buildInvite` plus `registerInviteSent` is the macOS system share-sheet path. `registerInviteSent` accepts the native bridge's actual `SharingWithContextResult`; do not fabricate a Messages result. The server returned an empty HTTP 500 when that callback was simulated on Windows, even with valid target eligibility and an Apple-issued invite code.

## Known mutation shapes

Current FamilyCircle code builds member removal as an Apple plist request to the `removeFromFamily` bag endpoint:

```text
member-dsid=<numeric DSID returned by the current family-members response>
```

The current invite builder posts JSON to the Apple-provided add-member URL with top-level `inviteRecipients`. Invite completion records `transportMode`, `inviteRecipients` and `completionStatus`. Recipient acceptance or rejection uses the Apple invite URL/code and `userAction=accept|decline`. Pending invitation cancellation uses `email` plus the current `familyId`.

Do not guess recipient subfields or submit a mutation until a live family organizer response supplies the server-supported action and identifiers. After any write, re-fetch family details; HTTP 200 or an invitation payload alone is insufficient.
