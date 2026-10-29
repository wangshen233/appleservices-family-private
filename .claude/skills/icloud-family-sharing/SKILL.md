---
name: icloud-family-sharing
description: Inspect and manage Apple iCloud Family Sharing membership through the account's native protocol session. Use for family eligibility, member/invitation listing, inviting an account, accepting or rejecting an invitation, removing a member, leaving a family, or deleting a family. Do not use for CloudKit document sharing or App Store sandbox test families.
---

# iCloud Family Sharing

Use the account's own GSA app tokens, accepted iCloud session, diagnostic Mac identity and Apple-returned URL bag. Start with read-only discovery; family membership writes require an exact current member or invitation binding and a post-action re-read.

## Read-only discovery

Run from this repository:

```powershell
go run ./tools/familyinspect `
  -dir state/accounts/<account> `
  -gsa <fresh-private-gsa-checkpoint> `
  -dump <new-private-family-response>
```

The tool uses direct Apple HTTPS, keeps raw family data in an owner-only ignored file, and prints only counts, roles and eligibility. It does not submit a family mutation.

Require all of these bindings before any write:

- account directory `session.json` Apple ID and ADSID;
- the same account's `icloud-accepted.json` numeric iCloud DSID and live MME token;
- current `com.apple.gs.icloud.auth` and `com.apple.gs.icloud.family.auth` app tokens from the same GSA identity;
- the account's own native ADI and APS token;
- endpoints from the current `configurations/init?context=settings` response.

If family details returns Apple UI error but `checkFamilyEligibility` returns `eligible=true`, report that the account has no readable current family and is eligible to create or join one. Do not treat this as authentication failure or invent members.

## Membership mutations

Read [references/protocol.md](references/protocol.md) before implementing or submitting add, remove, accept, reject, leave or delete operations.

For removal, bind the target to the latest `family-members` response and require the acting account to be the organizer. Refuse self, organizer and child-account removal unless Apple explicitly returns a supported action for that exact member. Submit the Apple-returned numeric `member-dsid` once, then re-fetch family details and require the member to be absent.

For invitations, validate the recipient identifier, use the current `addFamilyMember` endpoint and preserve the server invitation code/URL. Completion requires the recipient to accept and a fresh family-details response showing the member. An invitation response alone is incomplete.

Leaving or deleting a family affects purchase sharing, subscriptions, child accounts and shared services. Bind the action to the current account's role and current family ID, then obtain explicit authorization for the concrete family and action immediately before submission.

Never log Apple IDs, names, phone numbers, member DSIDs, family IDs, invite codes, tokens, cookies or raw responses. Never reuse a member or invite identifier from another account or an older family snapshot.
