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

## Known mutation shapes

Current FamilyCircle code builds member removal as an Apple plist request to the `removeFromFamily` bag endpoint:

```text
member-dsid=<numeric DSID returned by the current family-members response>
```

The current invite builder posts JSON to the Apple-provided add-member URL with top-level `inviteRecipients`. Invite completion records `transportMode`, `inviteRecipients` and `completionStatus`. Recipient acceptance or rejection uses the Apple invite URL/code and `userAction=accept|decline`. Pending invitation cancellation uses `email` plus the current `familyId`.

Do not guess recipient subfields or submit a mutation until a live family organizer response supplies the server-supported action and identifiers. After any write, re-fetch family details; HTTP 200 or an invitation payload alone is insufficient.
